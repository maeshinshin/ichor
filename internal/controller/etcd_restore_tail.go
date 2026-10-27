/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// This file is the destructive part of an etcd restore: wipe etcd data on every control
// plane, then recover the snapshot on one. It is the part that takes the Kubernetes API away, so it
// is deliberately independent of Kubernetes: a restoreJob holds everything it needs in memory (Talos
// dialers, the object store, the key), keeps its progress in the marker object in the backup bucket,
// and runs as a goroutine that outlives the controller manager. Leader election is lost the moment
// the API goes away, which stops the manager; main waits for these jobs before it exits.
package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/maeshinshin/ichor/internal/etcdbackup"
)

const (
	// etcdRestoreTailTimeout bounds the wipe and the recovery together, from the marker's start.
	etcdRestoreTailTimeout = 60 * time.Minute

	// etcdRestoreResendAfter is how long a reset that was sent is trusted to be underway before the
	// node is asked again (it is only asked again while it still has etcd data).
	etcdRestoreResendAfter = 10 * time.Minute
)

// restoreTailInterval is the pause between two steps of a job. A variable so tests can shorten it.
var restoreTailInterval = 10 * time.Second

// restoreNode is one control plane the job reaches: its name and a way to dial it that needs
// nothing from Kubernetes (the bundle and the machine were read before the job started).
type restoreNode struct {
	name string
	dial func(ctx context.Context) (TalosClient, error)
}

// restoreJob is the destructive tail of one etcd restore. step is the whole logic and is safe to call
// again after any interruption: where it stands is the marker (written to the bucket before each
// irreversible action) and what the nodes report.
type restoreJob struct {
	key        types.NamespacedName // the operation
	backup     types.NamespacedName
	store      etcdbackup.Store
	prefix     string
	passphrase string
	scrub      scrubber

	// order is the control planes to wipe: all of them, except the one the controller itself runs on
	// when there is another to take the snapshot (that one is wiped by the reconcile once etcd is
	// back, because wiping it stops the controller). target is one of them.
	order  []restoreNode
	target restoreNode

	marker etcdbackup.RestoreMarker

	now      func() time.Time
	sent     map[string]time.Time
	verified bool
	// note is the last transient problem, for the log and for the failure message when the deadline passes.
	note string
}

// setStep records, in the bucket, that the restore reached step. Nothing irreversible follows a step
// that was not recorded: a failed write is retried by the next call.
func (j *restoreJob) setStep(ctx context.Context, step string) error {
	next := j.marker
	next.Step = step

	if err := etcdbackup.PutMarker(ctx, j.store, j.prefix, &next); err != nil {
		return fmt.Errorf("recording step %s in the backup storage: %w", step, err)
	}

	j.marker = next

	return nil
}

// step does the next piece of work. done is true once etcd is recovered and the marker says Rejoining;
// a non-nil error is final (the job records it in the marker and stops); everything transient is a
// false, nil with j.note saying why.
func (j *restoreJob) step(ctx context.Context) (done bool, err error) {
	if j.marker.Step == etcdbackup.MarkerRejoining {
		return true, nil
	}

	if j.now().Sub(j.marker.StartedAt) > etcdRestoreTailTimeout {
		return false, fmt.Errorf("the wipe and recovery did not finish within %s (last problem: %s)", etcdRestoreTailTimeout, j.note)
	}

	if j.marker.Step == etcdbackup.MarkerWiping {
		wiped, err := j.wipe(ctx)
		if err != nil || !wiped {
			return false, err
		}

		if err := j.setStep(ctx, etcdbackup.MarkerRecovering); err != nil {
			j.note = err.Error()

			return false, nil
		}
	}

	return j.recoverTarget(ctx)
}

// wipe asks every control plane that still has etcd data to wipe it, all in the same pass, and
// returns true when none has any. They must go together: a node wiped while the others still run
// would rejoin the old etcd by itself and have data again. A node that cannot be reached counts as
// not wiped yet: it is rebooting, or it is a problem the deadline reports.
func (j *restoreJob) wipe(ctx context.Context) (bool, error) {
	var notes []string

	for _, n := range j.order {
		c, err := n.dial(ctx)
		if err != nil {
			notes = append(notes, fmt.Sprintf("dialing %s: %v", n.name, err))

			continue
		}

		present, err := c.EtcdDataPresent(ctx)
		if err != nil {
			closeTalosClient(c)

			notes = append(notes, fmt.Sprintf("%s: reading its etcd data: %v", n.name, err))

			continue
		}

		if !present {
			closeTalosClient(c)

			continue
		}

		if at, ok := j.sent[n.name]; ok && j.now().Sub(at) < etcdRestoreResendAfter {
			closeTalosClient(c)

			notes = append(notes, n.name+": wipe underway")

			continue
		}

		err = c.ResetEtcdData(ctx)

		closeTalosClient(c)

		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: wiping etcd data: %v", n.name, j.scrub.clean(err.Error())))

			continue
		}

		j.sent[n.name] = j.now()
		notes = append(notes, n.name+": wipe requested")
	}

	j.note = strings.Join(notes, "; ")

	return len(notes) == 0, nil
}

// recoverTarget uploads the pinned snapshot to the target and bootstraps etcd from it, then waits for
// that etcd to answer. The target having etcd data is the sign that bootstrapping already happened.
func (j *restoreJob) recoverTarget(ctx context.Context) (bool, error) {
	c, err := j.target.dial(ctx)
	if err != nil {
		j.note = fmt.Sprintf("dialing %s: %v", j.target.name, err)

		return false, nil
	}

	defer closeTalosClient(c)

	present, err := c.EtcdDataPresent(ctx)
	if err != nil {
		j.note = fmt.Sprintf("%s: reading its etcd data: %v", j.target.name, err)

		return false, nil
	}

	if present {
		// The Bootstrap call returned; etcd has to come up from the snapshot before the others can join.
		if _, err := c.EtcdStatus(ctx); err != nil {
			j.note = fmt.Sprintf("%s: etcd is not answering yet: %v", j.target.name, j.scrub.clean(err.Error()))

			return false, nil
		}

		if err := j.setStep(ctx, etcdbackup.MarkerRejoining); err != nil {
			j.note = err.Error()

			return false, nil
		}

		return true, nil
	}

	if !j.verified {
		if _, err := etcdbackup.VerifyObject(ctx, j.store, etcdbackup.PinnedKey(j.prefix), j.passphrase); err != nil {
			return false, fmt.Errorf("the pinned snapshot no longer passes its check, so nothing was uploaded: %s", j.scrub.clean(err.Error()))
		}

		j.verified = true
	}

	rd, err := etcdbackup.Reader(ctx, j.store, etcdbackup.PinnedKey(j.prefix), j.passphrase)
	if err != nil {
		j.note = j.scrub.clean(err.Error())

		return false, nil
	}

	err = c.EtcdRecover(ctx, rd)
	_ = rd.Close()

	if err != nil {
		return j.recoveryCallFailed("uploading the snapshot", err)
	}

	err = c.BootstrapRecover(ctx)

	switch {
	case err == nil, errors.Is(err, ErrEtcdDataExists):
		j.note = j.target.name + ": etcd is starting from the snapshot"

		return false, nil
	}

	return j.recoveryCallFailed("bootstrapping etcd from the snapshot", err)
}

// recoveryCallFailed is a node that is not ready yet (wait) or a refusal (final).
func (j *restoreJob) recoveryCallFailed(what string, err error) (bool, error) {
	if errors.Is(err, ErrRecoveryNotReady) {
		j.note = fmt.Sprintf("%s: not ready for recovery yet: %v", j.target.name, j.scrub.clean(err.Error()))

		return false, nil
	}

	return false, fmt.Errorf("%s on %s: %s", what, j.target.name, j.scrub.clean(err.Error()))
}

// fail records a final error in the marker so that the controller (this process or the next) shows it.
func (j *restoreJob) fail(ctx context.Context, cause error) {
	next := j.marker
	next.Failure = j.scrub.clean(cause.Error())

	if err := etcdbackup.PutMarker(ctx, j.store, j.prefix, &next); err == nil {
		j.marker = next
	}
}

// run steps until done, failed or cancelled.
func (j *restoreJob) run(ctx context.Context) {
	log := logf.Log.WithName("etcd-restore").WithValues("operation", j.key.String())

	for {
		done, err := j.step(ctx)

		switch {
		case err != nil:
			log.Error(errors.New(j.scrub.clean(err.Error())), "the etcd restore stopped")
			j.fail(ctx, err)

			return
		case done:
			log.Info("etcd is recovered; the other control planes can join", "target", j.target.name)

			return
		}

		log.V(1).Info("etcd restore waiting", "step", j.marker.Step, "note", j.note)

		select {
		case <-ctx.Done():
			return
		case <-time.After(restoreTailInterval):
		}
	}
}

// restoreTails is the set of jobs this process runs. Its WaitGroup is what main waits on.
type restoreTails struct {
	mu       sync.Mutex
	running  map[types.NamespacedName]bool
	inflight map[types.NamespacedName]types.NamespacedName // operation -> its TalosEtcdBackup
	wg       sync.WaitGroup
}

var defaultRestoreTails = &restoreTails{running: map[types.NamespacedName]bool{}, inflight: map[types.NamespacedName]types.NamespacedName{}}

// start runs run in a goroutine unless one runs for the operation already. The goroutine is not tied
// to any reconcile or manager context.
func (t *restoreTails) start(key, backup types.NamespacedName, run func()) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.inflight[key] = backup

	if t.running[key] {
		return
	}

	t.running[key] = true
	t.wg.Add(1)

	go func() {
		defer t.wg.Done()
		defer func() {
			t.mu.Lock()
			delete(t.running, key)
			t.mu.Unlock()
		}()

		run()
	}()
}

func (t *restoreTails) isRunning(key types.NamespacedName) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.running[key]
}

// backupOf returns the TalosEtcdBackup of an operation this process has seen start.
func (t *restoreTails) backupOf(key types.NamespacedName) (types.NamespacedName, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	b, ok := t.inflight[key]

	return b, ok
}

func (t *restoreTails) forget(key types.NamespacedName) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.inflight, key)
}

// WaitForRestoreTails blocks until every etcd restore job of this process has finished or timeout
// passed. main calls it after the manager stopped: the manager stops when the Kubernetes API goes
// away (leader election is lost), which is exactly when a restore is mid-way and must be finished,
// not abandoned.
func WaitForRestoreTails(timeout time.Duration) {
	done := make(chan struct{})

	go func() {
		defaultRestoreTails.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
	}
}

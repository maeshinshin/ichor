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
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
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
	passphrase etcdbackup.Key
	scrub      scrubber

	// order is the control planes to wipe: all of them, except the one the controller itself runs on
	// when there is another to take the snapshot (that one is wiped by the reconcile once etcd is
	// back, because wiping it stops the controller). target is one of them.
	order  []restoreNode
	target restoreNode

	// own is the controller's own control plane (marker Own), nil when the controller runs elsewhere. The
	// job wipes it itself, through Talos alone, once peers (the Kubernetes node names of the other
	// control planes) are members of the recovered etcd: from that node the Kubernetes API is not
	// reachable (its etcd and kube-proxy are stale), so the reconcile could not.
	own   *restoreNode
	peers []string

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
	if err := j.update(ctx, func(m *etcdbackup.RestoreMarker) { m.Step = step }); err != nil {
		return fmt.Errorf("recording step %s in the backup storage: %w", step, err)
	}

	return nil
}

// update stores the marker as change leaves it and keeps it in memory only once the store has it.
func (j *restoreJob) update(ctx context.Context, change func(*etcdbackup.RestoreMarker)) error {
	next := j.marker
	change(&next)

	if err := etcdbackup.PutMarker(ctx, j.store, j.prefix, j.passphrase, &next); err != nil {
		return err
	}

	j.marker = next

	return nil
}

// step does the next piece of work. done is true once etcd is recovered and the marker says Rejoining;
// a non-nil error is final (the job records it in the marker and stops); everything transient is a
// false, nil with j.note saying why.
func (j *restoreJob) step(ctx context.Context) (done bool, err error) {
	if j.marker.Step == ichorv1alpha1.EtcdRestoreStepRejoining {
		return j.ownStep(ctx)
	}

	if j.now().Sub(j.marker.StartedAt) > etcdRestoreTailTimeout {
		return false, fmt.Errorf("the wipe and recovery did not finish within %s (last problem: %s)", etcdRestoreTailTimeout, j.note)
	}

	if j.marker.Step == ichorv1alpha1.EtcdRestoreStepWiping {
		if !j.wipe(ctx) {
			return false, nil
		}

		if err := j.setStep(ctx, ichorv1alpha1.EtcdRestoreStepRecovering); err != nil {
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
func (j *restoreJob) wipe(ctx context.Context) bool {
	var notes []string

	for _, n := range j.order {
		if note := j.wipeNode(ctx, n); note != "" {
			notes = append(notes, note)
		}
	}

	j.note = strings.Join(notes, "; ")

	return len(notes) == 0
}

// wipeNode is one node's part of wipe: "" when it has no etcd data, else what is still going on.
func (j *restoreJob) wipeNode(ctx context.Context, n restoreNode) string {
	c, err := n.dial(ctx)
	if err != nil {
		return fmt.Sprintf("dialing %s: %v", n.name, err)
	}

	defer closeTalosClient(c)

	present, err := c.EtcdDataPresent(ctx)
	if err != nil {
		return fmt.Sprintf("%s: reading its etcd data: %v", n.name, err)
	}

	if !present {
		return ""
	}

	if at, ok := j.sent[n.name]; ok && j.now().Sub(at) < etcdRestoreResendAfter {
		return n.name + ": wipe underway"
	}

	if err := c.ResetEtcdData(ctx); err != nil {
		return fmt.Sprintf("%s: wiping etcd data: %v", n.name, j.scrub.clean(err.Error()))
	}

	j.sent[n.name] = j.now()

	return n.name + ": wipe requested"
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

		if err := j.setStep(ctx, ichorv1alpha1.EtcdRestoreStepRejoining); err != nil {
			j.note = err.Error()

			return false, nil
		}

		return j.ownStep(ctx)
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

	src := &readFailure{r: rd}
	err = c.EtcdRecover(ctx, src)
	_ = rd.Close()

	if src.err != nil {
		// The download failed, so the upload is not the node's verdict: ask again.
		j.note = j.scrub.clean(fmt.Sprintf("reading the pinned snapshot: %v", src.err))

		return false, nil
	}

	if err != nil {
		return j.recoveryCallFailed("uploading the snapshot", err)
	}

	err = c.BootstrapRecover(ctx)

	if err == nil || errors.Is(err, ErrEtcdDataExists) {
		j.note = j.target.name + ": etcd is starting from the snapshot"

		return false, nil
	}

	return j.recoveryCallFailed("bootstrapping etcd from the snapshot", err)
}

// ownStep wipes the controller's own control plane, last, once every other control plane is a member of
// the recovered etcd. It uses the Talos API only. The marker records the intent before the wipe is sent
// (OwnWipeAt) and the end after (OwnDone), so a process that dies at any point is continued by the next
// one: an unreachable node after a recorded wipe is waited for, a node that still has data is asked
// again after etcdRestoreResendAfter, a node that has none or is a member is done. It reports done once
// there is nothing left for the job.
func (j *restoreJob) ownStep(ctx context.Context) (bool, error) {
	if j.own == nil || j.marker.OwnDone {
		return true, nil
	}

	if j.now().Sub(j.marker.StartedAt) > etcdRestoreOverallTimeout {
		return false, fmt.Errorf("the controller's own control plane %s was not wiped within %s (last problem: %s)",
			j.own.name, etcdRestoreOverallTimeout, j.note)
	}

	tc, err := j.target.dial(ctx)
	if err != nil {
		j.note = fmt.Sprintf("dialing %s: %v", j.target.name, err)

		return false, nil
	}

	members, err := tc.EtcdMemberList(ctx)

	closeTalosClient(tc)

	if err != nil {
		j.note = fmt.Sprintf("etcd on %s does not answer yet: %s", j.target.name, j.scrub.clean(err.Error()))

		return false, nil
	}

	for _, p := range j.peers {
		if !etcdMemberPresent(members, p) {
			j.note = fmt.Sprintf("waiting for control plane %s to rejoin etcd before wiping %s, where this controller runs", p, j.own.name)

			return false, nil
		}
	}

	if name := j.marker.Nodes[j.marker.Own]; name != "" && etcdMemberPresent(members, name) {
		return j.ownDone(ctx)
	}

	c, err := j.own.dial(ctx)
	if err != nil {
		j.note = fmt.Sprintf("dialing %s: %v", j.own.name, err)

		return false, nil
	}

	defer closeTalosClient(c)

	present, err := c.EtcdDataPresent(ctx)
	if err != nil {
		j.note = fmt.Sprintf("%s: reading its etcd data: %v", j.own.name, err)

		return false, nil
	}

	if !present {
		return j.ownDone(ctx)
	}

	if at := j.marker.OwnWipeAt; !at.IsZero() && j.now().Sub(at) < etcdRestoreResendAfter {
		j.note = j.own.name + ": wipe underway"

		return false, nil
	}

	// The intent is stored before the wipe is sent: this controller dies with the node.
	if err := j.update(ctx, func(m *etcdbackup.RestoreMarker) { m.OwnWipeAt = j.now().UTC() }); err != nil {
		j.note = fmt.Sprintf("recording the wipe of %s in the backup storage: %v", j.own.name, j.scrub.clean(err.Error()))

		return false, nil
	}

	if err := c.ResetEtcdData(ctx); err != nil {
		// Not sent: ask again at the next pass rather than after the resend interval.
		j.marker.OwnWipeAt = time.Time{}
		j.note = fmt.Sprintf("%s: wiping etcd data: %v", j.own.name, j.scrub.clean(err.Error()))

		return false, nil
	}

	j.note = j.own.name + ": wipe requested (this controller runs there and stops with it)"

	return false, nil
}

// ownDone records that the controller's own node needs nothing more.
func (j *restoreJob) ownDone(ctx context.Context) (bool, error) {
	if err := j.update(ctx, func(m *etcdbackup.RestoreMarker) { m.OwnDone = true }); err != nil {
		j.note = err.Error()

		return false, nil
	}

	return true, nil
}

// recoveryCallFailed is a node that is not ready yet (wait) or a refusal (final).
func (j *restoreJob) recoveryCallFailed(what string, err error) (bool, error) {
	if errors.Is(err, ErrRecoveryNotReady) {
		j.note = fmt.Sprintf("%s: not ready for recovery yet: %v", j.target.name, j.scrub.clean(err.Error()))

		return false, nil
	}

	return false, fmt.Errorf("%s on %s: %s", what, j.target.name, j.scrub.clean(err.Error()))
}

// readFailure keeps the first error its reader returned, so that a failed download can be told from the
// node refusing the upload (the node sees only the stream's error).
type readFailure struct {
	r   io.Reader
	err error
}

func (f *readFailure) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && f.err == nil {
		f.err = err
	}

	return n, err
}

// fail records a final error in the marker so that the controller (this process or the next) shows it.
func (j *restoreJob) fail(ctx context.Context, cause error) {
	_ = j.update(ctx, func(m *etcdbackup.RestoreMarker) { m.Failure = j.scrub.clean(cause.Error()) })
}

// recordNote keeps the job's last transient problem in the marker when it changed, so that a process
// that is killed (or runs out of memory) leaves the reason behind and the next start can show it.
// Best effort: a failed write is tried again with the next note.
func (j *restoreJob) recordNote(ctx context.Context) {
	note := j.scrub.clean(j.note)
	if len(note) > 500 {
		note = note[:500]
	}

	if note == j.marker.LastNote || note == "" {
		return
	}

	_ = j.update(ctx, func(m *etcdbackup.RestoreMarker) { m.LastNote = note })
}

// stepSafely is step, with a panic turned into a final error: a crash must not leave the marker
// saying nothing happened.
func (j *restoreJob) stepSafely(ctx context.Context) (done bool, err error) {
	defer func() {
		if p := recover(); p != nil {
			done, err = false, fmt.Errorf("the restore job crashed: %v", p)
		}
	}()

	return j.step(ctx)
}

// run steps until done, failed or cancelled.
func (j *restoreJob) run(ctx context.Context) {
	log := logf.Log.WithName("etcd-restore").WithValues("operation", j.key.String())

	for {
		done, err := j.stepSafely(ctx)

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
		j.recordNote(ctx)

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

	t.wg.Go(func() {
		defer func() {
			t.mu.Lock()
			delete(t.running, key)
			t.mu.Unlock()
		}()

		run()
	})
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
// not abandoned. The manager's health probe server went with it, and a failing liveness probe would
// have the kubelet kill the process, so while a job runs the same probe address is served here.
func WaitForRestoreTails(timeout time.Duration, probeAddr string) {
	if !defaultRestoreTails.any() {
		return
	}

	if probeAddr != "" && probeAddr != "0" {
		stop := serveProbes(probeAddr)
		defer stop()
	}

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

// RestoreTailsRunning reports whether an etcd restore job of this process is mid-way.
func RestoreTailsRunning() bool { return defaultRestoreTails.any() }

func (t *restoreTails) any() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return len(t.running) > 0
}

// serveProbes answers /healthz and /readyz with 200 on addr until the returned func is called. The
// address was the manager's a moment ago, so binding it is retried for a few seconds.
func serveProbes(addr string) (stop func()) {
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }
	mux.HandleFunc("/healthz", ok)
	mux.HandleFunc("/readyz", ok)

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		for range 30 {
			l, err := net.Listen("tcp", addr)
			if err == nil {
				_ = srv.Serve(l)

				return
			}

			time.Sleep(time.Second)
		}
	}()

	return func() { _ = srv.Close() }
}

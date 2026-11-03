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

// Package clusterlock serializes disruptive per-cluster operations (design
// principle 6: upgrades, reboot-causing applies, CA rotation, topology
// changes) through a single coordination.k8s.io/v1 Lease per cluster. The
// Lease shares the TalosCluster's name and namespace, so two reconcilers
// working on different clusters never contend and two working on the same
// one always do - that identity mapping is the whole derivation, no registry
// needed.
//
// The Kubernetes API server treats holderIdentity, leaseDurationSeconds,
// acquireTime and renewTime as opaque: it stores and returns them but never
// reads them to decide anything. Expiry and "who holds it" are conventions
// this package interprets client-side, the same way client-go's own
// leaderelection package does for its Lease-backed lock (see
// k8s.io/client-go/tools/leaderelection/resourcelock.LeaseLock). What the API
// server does enforce is optimistic concurrency on Update/Delete via
// resourceVersion, which is what makes Acquire's "exactly one winner" promise
// hold under a real race.
package clusterlock

import (
	"context"
	"errors"
	"fmt"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DefaultDuration is how long a held lock survives without renewal before a
// different identity may take it over.
//
// Reboot-causing operations go one node at a time, and each step - cordon,
// drain, Talos reboot, rejoin, verify - takes minutes on real hardware, not
// seconds (architecture.md's "Configuration reconcile" section). The
// reconciler is expected to call Renew well inside this window on every
// step, so DefaultDuration only has to outlive the gap between two
// renewals, not the whole operation. Five minutes gives a slow drain or a
// sluggish reboot room to finish one step and renew again before the lease
// expires, while still bounding the damage a crash does: if the holder dies
// mid-step without releasing, the cluster is blocked for at most
// DefaultDuration, never for the rest of the operation. Both failure modes
// scale with this one number, which is why it is a parameter to New rather
// than a constant wired into the logic.
const DefaultDuration = 5 * time.Minute

// HeldError is returned by Acquire and Renew when a different, unexpired
// identity holds the lock.
type HeldError struct {
	// Holder is the identity currently holding the lock, when known.
	Holder string
}

func (e *HeldError) Error() string {
	if e.Holder == "" {
		return "clusterlock: held by another reconcile (holder not visible yet)"
	}
	return fmt.Sprintf("clusterlock: held by %q", e.Holder)
}

// Lock is a mutex over one cluster's disruptive operations, backed by a
// Lease. The zero value is not usable; construct one with New.
type Lock struct {
	client   client.Client
	reader   client.Reader
	key      types.NamespacedName
	duration time.Duration
}

// WithReader makes every read of the Lease go through r instead of the client. A manager's client
// reads from a cache that lags the lock's own writes: a Renew straight after the Acquire that
// created the Lease sees NotFound, and a renewal races the previous one's resourceVersion. Pass
// the manager's uncached API reader so the lock decides on the live object. Writes still use the
// client.
func (l *Lock) WithReader(r client.Reader) *Lock {
	if r != nil {
		l.reader = r
	}
	return l
}

// New returns a Lock for cluster. The Lease is read, created and updated at
// the same namespace and name as cluster.
//
// duration is how long the lock survives without a Renew call; duration <= 0
// uses DefaultDuration.
func New(c client.Client, cluster types.NamespacedName, duration time.Duration) *Lock {
	if duration <= 0 {
		duration = DefaultDuration
	}
	return &Lock{client: c, reader: c, key: cluster, duration: duration}
}

// Acquire takes the lock for identity. It succeeds if the lock is free, if
// it has expired, or if identity already holds it - that last case is
// resumption (design principle 7: progress resumes idempotently after a
// restart or a leader change), so the new process picks its own lock back
// up instead of being told it conflicts with itself.
//
// identity must be stable across restarts for the same logical holder - a
// fixed name for "the active ichor-controller", for instance - not a
// per-process random value or a Kubernetes pod name, both of which change on
// every restart and would make resumption always look like a fresh,
// conflicting acquire.
//
// If a different identity holds the lock and it has not expired, Acquire
// returns a *HeldError and makes no change.
func (l *Lock) Acquire(ctx context.Context, identity string) error {
	if identity == "" {
		return errors.New("clusterlock: identity must not be empty")
	}

	var lease coordinationv1.Lease
	err := l.reader.Get(ctx, l.key, &lease)
	switch {
	case apierrors.IsNotFound(err):
		return l.create(ctx, identity)
	case err != nil:
		return err
	}

	holder := ptr.Deref(lease.Spec.HolderIdentity, "")
	if holder == identity {
		return l.renew(ctx, &lease)
	}
	if !expired(&lease, l.duration) {
		return &HeldError{Holder: holder}
	}
	return l.takeOver(ctx, &lease, identity)
}

// Renew extends the lock for identity without changing who holds it. It
// fails with a *HeldError, and changes nothing, if identity is not the
// current holder - including when the lock has already expired and someone
// else took it over - so the caller learns immediately that it no longer
// owns the operation it thinks it is running.
func (l *Lock) Renew(ctx context.Context, identity string) error {
	if identity == "" {
		return errors.New("clusterlock: identity must not be empty")
	}

	var lease coordinationv1.Lease
	if err := l.reader.Get(ctx, l.key, &lease); err != nil {
		if apierrors.IsNotFound(err) {
			return &HeldError{}
		}
		return err
	}
	if holder := ptr.Deref(lease.Spec.HolderIdentity, ""); holder != identity {
		return &HeldError{Holder: holder}
	}
	return l.renew(ctx, &lease)
}

// Release frees the lock if identity holds it. If identity does not hold
// it - wrong identity, already expired and taken over, or already free -
// Release changes nothing and returns nil. A reconciler that errored
// halfway through an operation must be able to call Release unconditionally
// (a defer, typically) without risking that it deletes a lock someone else
// has since legitimately acquired; silently doing nothing is what makes
// that safe, since there is nothing destructive to report.
func (l *Lock) Release(ctx context.Context, identity string) error {
	if identity == "" {
		return errors.New("clusterlock: identity must not be empty")
	}

	var lease coordinationv1.Lease
	if err := l.reader.Get(ctx, l.key, &lease); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if ptr.Deref(lease.Spec.HolderIdentity, "") != identity {
		return nil
	}

	rv := lease.ResourceVersion
	err := l.client.Delete(ctx, &lease, client.Preconditions(metav1.Preconditions{ResourceVersion: &rv}))
	if err != nil && (apierrors.IsNotFound(err) || apierrors.IsConflict(err)) {
		// Freed or taken over by someone else between our Get and our
		// Delete: nothing left for us to release.
		return nil
	}
	return err
}

// Holder reports the identity currently holding the lock. held is false,
// with an empty identity, if the lock is free, expired or was never
// created.
func (l *Lock) Holder(ctx context.Context) (identity string, held bool, err error) {
	var lease coordinationv1.Lease
	if err := l.reader.Get(ctx, l.key, &lease); err != nil {
		if apierrors.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if expired(&lease, l.duration) {
		return "", false, nil
	}
	return ptr.Deref(lease.Spec.HolderIdentity, ""), true, nil
}

// create makes the Lease for the first time. If it already exists, someone
// else won the race between our caller's Get and this Create, so that is
// reported as held rather than retried - two racing Acquire calls must
// produce exactly one winner, not a retry loop that could flip the outcome.
func (l *Lock) create(ctx context.Context, identity string) error {
	lease := &coordinationv1.Lease{
		Name: l.key.Name, Namespace: l.key.Namespace,
		Spec: heldSpec(identity, l.duration, 0),
	}
	if err := l.client.Create(ctx, lease); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return &HeldError{Holder: l.currentHolderBestEffort(ctx)}
		}
		return err
	}
	return nil
}

// renew updates lease, which the caller has already confirmed identity
// holds, to extend its expiry. lease carries the resourceVersion that was
// just read, so a concurrent change - a takeover that slipped in after
// expiry, or a release - surfaces as a conflict instead of being silently
// overwritten.
func (l *Lock) renew(ctx context.Context, lease *coordinationv1.Lease) error {
	lease.Spec.RenewTime = ptr.To(metav1.NowMicro())
	lease.Spec.LeaseDurationSeconds = leaseSeconds(l.duration)
	return l.update(ctx, lease)
}

// takeOver claims an expired lease for identity. lease carries the
// resourceVersion just read, so if two identities race to take over the
// same expired lease, only the first Update lands; the second gets a
// conflict and is told the (new) holder instead of corrupting its state.
func (l *Lock) takeOver(ctx context.Context, lease *coordinationv1.Lease, identity string) error {
	transitions := int32(1)
	if lease.Spec.LeaseTransitions != nil {
		transitions = *lease.Spec.LeaseTransitions + 1
	}
	lease.Spec = heldSpec(identity, l.duration, transitions)
	return l.update(ctx, lease)
}

// update writes lease, reporting a lost resourceVersion race as held.
func (l *Lock) update(ctx context.Context, lease *coordinationv1.Lease) error {
	if err := l.client.Update(ctx, lease); err != nil {
		if apierrors.IsConflict(err) {
			return &HeldError{Holder: l.currentHolderBestEffort(ctx)}
		}
		return err
	}
	return nil
}

func leaseSeconds(d time.Duration) *int32 { return ptr.To(max(int32(d.Seconds()), 1)) }

// currentHolderBestEffort re-reads the lease to report who holds it after
// losing a create/update race. A failure here just means a less specific
// HeldError, not a lost or corrupted lock, so the error is swallowed.
func (l *Lock) currentHolderBestEffort(ctx context.Context) string {
	var lease coordinationv1.Lease
	if err := l.reader.Get(ctx, l.key, &lease); err != nil {
		return "unknown"
	}
	return ptr.Deref(lease.Spec.HolderIdentity, "")
}

// heldSpec builds the spec for identity freshly holding the lock, with
// acquireTime and renewTime both reset to now.
func heldSpec(identity string, duration time.Duration, transitions int32) coordinationv1.LeaseSpec {
	now := ptr.To(metav1.NowMicro())
	return coordinationv1.LeaseSpec{
		HolderIdentity:       &identity,
		LeaseDurationSeconds: leaseSeconds(duration),
		AcquireTime:          now,
		RenewTime:            now,
		LeaseTransitions:     &transitions,
	}
}

// expired reports whether lease's holder (if any) no longer owns it. A
// lease with no recorded holder or renewal is treated as expired, since
// there is nothing valid to protect. fallback is used in place of the
// lease's own leaseDurationSeconds when that field was never set.
func expired(lease *coordinationv1.Lease, fallback time.Duration) bool {
	if lease.Spec.HolderIdentity == nil || lease.Spec.RenewTime == nil {
		return true
	}
	d := fallback
	if lease.Spec.LeaseDurationSeconds != nil {
		d = time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second
	}
	return time.Now().After(lease.Spec.RenewTime.Add(d))
}

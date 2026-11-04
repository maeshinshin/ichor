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

package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
)

// machineLockIdentityPrefix starts the cluster lock identity of every TalosMachine step that takes
// the lock: a control-plane join and a machine removal.
const machineLockIdentityPrefix = controllerIdentity + "/machine/"

// machineLockIdentity is the identity m takes the cluster lock under. Like operationLockIdentity it
// comes from the namespace and name, so it is the same across reconciles and restarts (the holder
// re-enters its own lock) and different for every other machine, which therefore waits:
// clusterlock.Acquire lets only the same identity back in, and two etcd membership changes must
// never run together.
func machineLockIdentity(m *ichorv1alpha1.TalosMachine) string {
	return machineLockIdentityPrefix + m.Namespace + "/" + m.Name
}

// describeLockHolder names a lock holder this package made up ("TalosOperation \"ns/name\"",
// "TalosMachine \"ns/name\""); empty for any other identity.
func describeLockHolder(holder string) string {
	if nn, ok := strings.CutPrefix(holder, operationLockIdentityPrefix); ok {
		return fmt.Sprintf("TalosOperation %q", nn)
	}

	if nn, ok := strings.CutPrefix(holder, machineLockIdentityPrefix); ok {
		return fmt.Sprintf("TalosMachine %q", nn)
	}

	return ""
}

// lockHeldBy words a refused Acquire for a machine: it names the TalosOperation or TalosMachine that
// holds the lock; any other holder keeps clusterlock's wording.
func lockHeldBy(held *clusterlock.HeldError) string {
	if who := describeLockHolder(held.Holder); who != "" {
		return "the cluster lock is held by " + who
	}

	return held.Error()
}

// releaseIfHolderStopped frees the lease when acquireErr says it is held by a holder that will never
// renew it, and reports whether the caller should Acquire again. Any other acquireErr (including nil)
// is left to the caller.
func releaseIfHolderStopped(
	ctx context.Context, reader client.Reader, lock *clusterlock.Lock, acquireErr error,
) (retry bool, err error) {
	held, ok := errors.AsType[*clusterlock.HeldError](acquireErr)
	if !ok || !lockHolderStopped(ctx, reader, held.Holder) {
		return false, nil
	}

	return true, lock.Release(ctx, held.Holder)
}

// lockHolderStopped reports whether holder is a TalosOperation or TalosMachine that will never renew
// the lock, so nobody has to wait out its five minutes:
//
//   - a TalosOperation that is gone, Completed, or Paused on a failed step (only an explicit retry
//     moves it, and that takes the lock again);
//   - a TalosMachine that is gone (its removal finished, or it was never removed through the
//     finalizer), or that is not being removed and is done with its locked step: a join holds the lock
//     only while it hands the configuration to the node, so a machine whose configuration is stored
//     (status.configStoredAt) or that is Ready has nothing left to do under it.
//
// A machine being removed (deletionTimestamp set) is not stopped: the removal is its locked step and
// runs until the finalizer goes. Any other holder, including the legacy shared "ichor-controller"
// identity a previous build left, may still be working and ages out on its own.
func lockHolderStopped(ctx context.Context, reader client.Reader, holder string) bool {
	if nn, ok := strings.CutPrefix(holder, operationLockIdentityPrefix); ok {
		return operationHolderStopped(ctx, reader, nn)
	}

	if nn, ok := strings.CutPrefix(holder, machineLockIdentityPrefix); ok {
		return machineHolderStopped(ctx, reader, nn)
	}

	return false
}

func operationHolderStopped(ctx context.Context, reader client.Reader, namespacedName string) bool {
	namespace, name, _ := strings.Cut(namespacedName, "/")

	var o ichorv1alpha1.TalosOperation
	if err := reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &o); err != nil {
		return apierrors.IsNotFound(err)
	}

	return o.Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted ||
		(o.Status.Phase == ichorv1alpha1.TalosOperationPhasePaused && o.Status.FailureReason != "" && !o.Spec.Paused)
}

func machineHolderStopped(ctx context.Context, reader client.Reader, namespacedName string) bool {
	namespace, name, _ := strings.Cut(namespacedName, "/")

	var m ichorv1alpha1.TalosMachine
	if err := reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &m); err != nil {
		return apierrors.IsNotFound(err)
	}

	if !m.DeletionTimestamp.IsZero() {
		return false
	}

	return m.Status.ConfigStoredAt != nil || apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionReady)
}

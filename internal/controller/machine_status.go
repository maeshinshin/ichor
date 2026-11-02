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
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// machineStatusBackoff retries a conflicting status write a few times, long enough for a cached
// reader to catch up with the write that caused the conflict.
var machineStatusBackoff = wait.Backoff{Steps: 6, Duration: 50 * time.Millisecond, Factor: 2, Jitter: 0.1}

// updateMachineStatus applies mutate to a fresh copy of m and persists its status. The executors
// read m before long calls (dial, dry run, image pre-pull, apply, a reboot) while the machine
// reconciler writes the same status in parallel, so a plain Status().Update of that copy loses
// with "the object has been modified" and pauses the operation. Instead the machine is re-read
// (rd, preferably an uncached reader) and only mutate's change is applied to the fresh copy,
// retrying on conflict; the other writer's fields survive. m is refreshed from the result.
func updateMachineStatus(
	ctx context.Context, rd client.Reader, c client.Client, m *ichorv1alpha1.TalosMachine,
	mutate func(*ichorv1alpha1.TalosMachine),
) error {
	return retry.RetryOnConflict(machineStatusBackoff, func() error {
		var fresh ichorv1alpha1.TalosMachine
		if err := rd.Get(ctx, client.ObjectKeyFromObject(m), &fresh); err != nil {
			return err
		}

		mutate(&fresh)

		if err := c.Status().Update(ctx, &fresh); err != nil {
			return err
		}

		fresh.DeepCopyInto(m)

		return nil
	})
}

// setMachineCondition sets cond on m's status and persists it (see updateMachineStatus).
func setMachineCondition(
	ctx context.Context, rd client.Reader, c client.Client, m *ichorv1alpha1.TalosMachine, cond metav1.Condition,
) error {
	return updateMachineStatus(ctx, rd, c, m, func(fresh *ichorv1alpha1.TalosMachine) {
		apimeta.SetStatusCondition(&fresh.Status.Conditions, cond)
	})
}

// recordMaintenanceFingerprint pins the maintenance certificate fingerprint a discovery saw on m
// (ADR 0108). It is a no-op for an empty or unchanged fingerprint.
func recordMaintenanceFingerprint(
	ctx context.Context, rd client.Reader, c client.Client, m *ichorv1alpha1.TalosMachine, fingerprint string,
) error {
	if fingerprint == "" || m.Status.MaintenanceFingerprint == fingerprint {
		return nil
	}

	return updateMachineStatus(ctx, rd, c, m, func(fresh *ichorv1alpha1.TalosMachine) {
		fresh.Status.MaintenanceFingerprint = fingerprint
	})
}

// recordObservedEndpoint stores the address m answers at in its status (never in spec: the
// controller does not mutate spec; ADR 0086) and persists it.
func recordObservedEndpoint(
	ctx context.Context, rd client.Reader, c client.Client, m *ichorv1alpha1.TalosMachine, address string,
) error {
	return updateMachineStatus(ctx, rd, c, m, func(fresh *ichorv1alpha1.TalosMachine) {
		fresh.Status.ObservedEndpoint = address
		fresh.Status.ObservedEndpointFor = fresh.Spec.Endpoint
	})
}

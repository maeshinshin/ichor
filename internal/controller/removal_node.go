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

// The Node step of a removal that wipes the machine: after Talos accepted the Reset the
// Kubernetes Node is waited on until it is NotReady, and then deleted. The wiped machine has no
// credentials left, so its kubelet cannot register the Node again; the wait is only for the moment
// between the request and the wipe.

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/reclaim"
)

const (
	// resetPowerOffAnnotation carries TalosOperation.spec.node.powerOff of a reset to the removal flow:
	// with "true" the node powers off after the wipe instead of rebooting into maintenance mode.
	resetPowerOffAnnotation = deleteguard.ResetPowerOffAnnotation

	// reasonNodeNotDeleted is the Event reason when the Node was still Ready after nodeDownWait.
	reasonNodeNotDeleted = "KubernetesNodeNotDeleted"

	// reasonWipeNotConfirmedPoweredOff is the Normal Event of a power-off removal: the machine cannot answer in
	// maintenance mode once it is off, so the wipe cannot be confirmed. A limit of the path, not a fault.
	reasonWipeNotConfirmedPoweredOff = "WipeNotConfirmedPoweredOff"

	// nodeWaitInterval is how often the removal looks again at a Node that is still Ready.
	nodeWaitInterval = 10 * time.Second
)

// nodeDownWait bounds the wait for the Node to go NotReady after the reset was accepted, and for a rebooting
// machine to answer in maintenance mode. Kubernetes marks a silent kubelet NotReady after 40 s, so this is
// generous. A var so tests can shrink it.
var nodeDownWait = 5 * time.Minute

// resetMaintenanceProbe reports whether the machine at address answers in maintenance mode with uuid:
// the only evidence that a reset wiped it. A var so tests can answer for the machine.
var resetMaintenanceProbe = func(ctx context.Context, address, uuid string) bool {
	// Bounded like reclaim's WaitMaintenance: a node still rebooting is asked again on the next requeue.
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	got, err := reclaim.MaintenanceUUID(pctx, reclaim.HostOnly(address))

	return err == nil && uuid != "" && strings.EqualFold(got, uuid)
}

// powerOffRequested reports whether m's reset powers the node off after the wipe.
func powerOffRequested(m *ichorv1alpha1.TalosMachine) bool {
	return m.Annotations[resetPowerOffAnnotation] == resetOnDeleteValue
}

// removalWaitError is reconcileRemoval's verdict while a Node is still Ready after its machine was told to
// reset: a requeue after nodeWaitInterval, not an error for the backoff.
type removalWaitError struct{ msg string }

func (e *removalWaitError) Error() string { return e.msg }

// nodeStep is what the Node step did.
type nodeStep struct {
	// unwiped is set when the node did not answer, so no Reset was sent: it says why.
	unwiped string
	// note says what happened to the Kubernetes Node.
	note string
	// left is true when the Node was not deleted because it was still Ready after nodeDownWait.
	left bool
	// unconfirmed is set when the machine did not answer in maintenance mode within nodeDownWait of the reset.
	unconfirmed bool
	// poweredOff is set when the Reset powers the machine off: it cannot answer in maintenance mode, so the
	// wipe is not confirmed and the removal does not wait for it.
	poweredOff bool
}

// nodeDown reports whether the Kubernetes Node nodeName is missing or not Ready.
func (r *TalosMachineReconciler) nodeDown(ctx context.Context, nodeName string) bool {
	if nodeName == "" {
		return true
	}

	var node corev1.Node
	if err := r.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
		return apierrors.IsNotFound(err)
	}

	return !nodeIsReady(&node)
}

// resetAccepted reports whether the removal already got Talos to accept the Reset (resetNode's record).
func resetAccepted(m *ichorv1alpha1.TalosMachine) bool {
	c := apimeta.FindStatusCondition(m.Status.Conditions, conditionRemoval)

	return c != nil && c.Status == metav1.ConditionTrue && c.Reason == reasonResetRequested
}

// finishReset completes the removal of a machine that was told to wipe. A machine that reboots is confirmed
// wiped by answering in maintenance mode with its UUID, not by its Node: a machine that rebooted with its disk
// intact goes NotReady too. Until it answers, the removal waits, and after nodeDownWait it completes unconfirmed.
// A machine that powers off cannot answer, so the removal does not wait for that: it completes on the Node going
// NotReady, and says the wipe is not confirmed because the machine was powered off. Then the Kubernetes Node is
// deleted once it is NotReady. While it is Ready the removal asks for a requeue; after nodeDownWait it leaves the
// Node, marked as reset, and the Event names the manual step.
func (r *TalosMachineReconciler) finishReset(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, nodeName string, step nodeStep,
) error {
	// A machine that was never sent a reset (unwiped) has no wipe to confirm.
	if step.unwiped == "" {
		if powerOffRequested(m) {
			step.poweredOff = true
		} else if !resetMaintenanceProbe(ctx, m.DialEndpoint(), m.Spec.Identity.UUID) {
			if !step.expired(m) {
				return &removalWaitError{msg: fmt.Sprintf("waiting for %s to answer in maintenance mode with UUID %s", m.Name, m.Spec.Identity.UUID)}
			}

			step.unconfirmed = true
		}
	}

	var node corev1.Node

	found := nodeName != ""
	if found {
		if err := r.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}

			found = false
		}
	}

	switch {
	case !found:
		step.note = "it had no Kubernetes Node to delete"
	case !nodeIsReady(&node):
		if err := r.Delete(ctx, &node, client.Preconditions{UID: &node.UID}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}

		step.note = fmt.Sprintf("the Kubernetes Node %s was deleted", nodeName)
	case !step.expired(m):
		return &removalWaitError{msg: fmt.Sprintf("waiting for the Kubernetes Node %s to go NotReady before deleting it", nodeName)}
	default:
		r.markNodeReset(ctx, nodeName)

		step.left = true
		step.note = fmt.Sprintf("the Kubernetes Node %[1]s was still Ready after %[2]s, so it was not deleted; "+
			"once the node is off or rebooted, run kubectl delete node %[1]s", nodeName, nodeDownWait)
	}

	return r.completeRemoval(ctx, m, true, step)
}

// expired reports whether the wait that began when the Reset was accepted is over.
func (nodeStep) expired(m *ichorv1alpha1.TalosMachine) bool {
	c := apimeta.FindStatusCondition(m.Status.Conditions, conditionRemoval)

	return c != nil && time.Since(c.LastTransitionTime.Time) >= nodeDownWait
}

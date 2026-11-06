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

// The Node step of a removal that does not reset: after the drain and the etcd member
// removal the machine is shut down, the Kubernetes Node is waited on until it is NotReady, and then
// deleted. A powered-off kubelet cannot register the Node again, which is why the Node used to stay.

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	// nodeShutdownRequestedAnnotation marks the Kubernetes Node of a machine the removal asked to
	// shut down, with the time it did. A resumed removal sees it and goes straight to the wait,
	// without draining a node that is already off. It lives on the Node, not on the TalosMachine.
	nodeShutdownRequestedAnnotation = "ichor.maesh.dev/removal-shutdown-requested"
	// nodeShutdownUnsentAnnotation carries why the shutdown could not be sent (the node was unreachable).
	nodeShutdownUnsentAnnotation = "ichor.maesh.dev/removal-shutdown-unsent"

	reasonNodeShutdownRequested = "NodeShutdownRequested"

	// nodeWaitInterval is how often the removal looks again at a Node that is still Ready.
	nodeWaitInterval = 10 * time.Second
)

// nodeDownWait bounds the wait for the Node to go NotReady after the shutdown was requested. Kubernetes
// marks a silent kubelet NotReady after 40 s, so this is generous. A var so tests can shrink it.
var nodeDownWait = 5 * time.Minute

// removalWaitError is reconcileRemoval's verdict while a Node is still Ready after its machine was asked
// to shut down: a requeue after nodeWaitInterval, not an error for the backoff.
type removalWaitError struct{ msg string }

func (e *removalWaitError) Error() string { return e.msg }

// nodeStep is what the Node step did.
type nodeStep struct {
	// note says it in words for the Removal condition and the Event.
	note string
	// left is true when the Node was not deleted because it was still Ready after nodeDownWait.
	left bool
}

// shutdownAndDeleteNode runs the Node step for m, whose Kubernetes Node is nodeName. It returns a
// *removalWaitError while the Node is Ready and the wait is not over.
func (r *TalosMachineReconciler) shutdownAndDeleteNode(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, nodeName string,
) (nodeStep, error) {
	node := &corev1.Node{}

	found := nodeName != ""
	if found {
		if err := r.Get(ctx, types.NamespacedName{Name: nodeName}, node); err != nil {
			if !apierrors.IsNotFound(err) {
				return nodeStep{}, err
			}

			found = false
		}
	}

	if !found || node.Annotations[nodeShutdownRequestedAnnotation] == "" {
		sendErr := r.sendShutdown(ctx, cluster, m)
		if found {
			if err := r.markShutdownRequested(ctx, node, sendErr); err != nil {
				return nodeStep{}, err
			}
		}

		if !found {
			return nodeStep{note: unsentNote(sendErr, "powered off") + "; it had no Kubernetes Node"}, nil
		}
	}

	return r.awaitNodeDown(ctx, m, node)
}

// sendShutdown asks m's own node to power off, forced: the drain and the etcd leave already happened.
func (r *TalosMachineReconciler) sendShutdown(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) error {
	if r.TalosClient == nil {
		return fmt.Errorf("no Talos API connectivity is configured for this controller")
	}

	bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, cluster)
	if err != nil {
		return fmt.Errorf("loading secrets bundle to shut %q down: %w", m.Name, err)
	}

	callCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	talos, err := r.TalosClient(callCtx, cluster, m, bundle)
	if err != nil {
		return fmt.Errorf("dialing %q to shut it down: %w", m.Name, err)
	}
	defer closeTalosClient(talos)

	if err := talos.Shutdown(callCtx, true); err != nil {
		return fmt.Errorf("shutdown request to %q failed: %w", m.Name, err)
	}

	return nil
}

func (r *TalosMachineReconciler) markShutdownRequested(ctx context.Context, node *corev1.Node, sendErr error) error {
	patch := client.MergeFrom(node.DeepCopy())

	metav1.SetMetaDataAnnotation(&node.ObjectMeta, nodeShutdownRequestedAnnotation, time.Now().UTC().Format(time.RFC3339))

	if sendErr != nil {
		metav1.SetMetaDataAnnotation(&node.ObjectMeta, nodeShutdownUnsentAnnotation, sendErr.Error())
	}

	if err := r.Patch(ctx, node, patch); err != nil {
		return fmt.Errorf("marking node %q as asked to shut down: %w", node.Name, err)
	}

	return nil
}

// awaitNodeDown deletes node once it is not Ready, leaves it when the wait is over, and otherwise asks for
// a requeue.
func (r *TalosMachineReconciler) awaitNodeDown(ctx context.Context, m *ichorv1alpha1.TalosMachine, node *corev1.Node) (nodeStep, error) {
	var unsent error
	if why := node.Annotations[nodeShutdownUnsentAnnotation]; why != "" {
		unsent = fmt.Errorf("%s", why)
	}

	if !nodeIsReady(node) {
		if err := r.Delete(ctx, node, client.Preconditions{UID: &node.UID}); err != nil && !apierrors.IsNotFound(err) {
			return nodeStep{}, err
		}

		return nodeStep{note: unsentNote(unsent, "powered off") + fmt.Sprintf("; the Kubernetes Node %s was deleted", node.Name)}, nil
	}

	sent, _ := time.Parse(time.RFC3339, node.Annotations[nodeShutdownRequestedAnnotation])
	if time.Since(sent) >= nodeDownWait {
		return nodeStep{
			left: true,
			note: unsentNote(unsent, "asked to power off") + fmt.Sprintf(
				"; the Kubernetes Node %[1]s was still Ready after %s, so it was not deleted: once the machine is off, run kubectl delete node %[1]s",
				node.Name, nodeDownWait),
		}, nil
	}

	msg := fmt.Sprintf("waiting for the Kubernetes Node %s to go NotReady before deleting it", node.Name)
	if err := r.removalCondition(ctx, m, metav1.ConditionFalse, reasonNodeShutdownRequested, msg); err != nil {
		return nodeStep{}, err
	}

	return nodeStep{}, &removalWaitError{msg: msg}
}

// unsentNote words the shutdown: done, or not sent because the node did not answer (then it is down anyway).
func unsentNote(sendErr error, done string) string {
	if sendErr == nil {
		return "the machine was " + done
	}

	return fmt.Sprintf("the shutdown could not be sent (%v), so the machine is treated as down", sendErr)
}

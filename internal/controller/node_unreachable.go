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
	"cmp"
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	// conditionNodeReachable is present (False) only while the periodic read of the node fails; its
	// lastTransitionTime is when the failures began. It is removed by the first successful read.
	conditionNodeReachable = "NodeReachable"
	reasonNodeUnreachable  = ichorv1alpha1.TalosMachineReasonNodeUnreachable

	// nodeUnreachableGrace is how long reads may fail (a reboot, a restart) before Ready says the
	// node is unreachable and names the endpoint.
	nodeUnreachableGrace = 60 * time.Second

	maxDialErrorLen = 200
)

// readFailed handles a failed read of the node's current configuration that nothing else explained.
// Within the grace it reports the usual CurrentConfigUnavailable; once the reads have failed for longer
// it reports Ready=False NodeUnreachable with the endpoint, the dial error and, when the Kubernetes Node
// of this machine shows another InternalIP, the likely new address. spec.endpoint is never edited.
func (r *TalosMachineReconciler) readFailed(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, readErr error, now time.Time,
) (ctrl.Result, error) {
	cond := apimeta.FindStatusCondition(m.Status.Conditions, conditionNodeReachable)
	if cond == nil {
		apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
			Type: conditionNodeReachable, Status: metav1.ConditionFalse, Reason: reasonNodeUnreachable,
			Message: "the node's current configuration cannot be read",
		})

		return r.retryLater(ctx, m, reasonCurrentConfigUnavailable, "reading the node's current configuration failed")
	}

	down := now.Sub(cond.LastTransitionTime.Time)
	if down < nodeUnreachableGrace {
		return r.retryLater(ctx, m, reasonCurrentConfigUnavailable, "reading the node's current configuration failed")
	}

	msg := fmt.Sprintf("the node at %s has not answered for %s: %s", m.DialEndpoint(), down.Round(time.Second), shortError(readErr))
	if ip := r.kubernetesNodeIP(ctx, m); ip != "" {
		if host, port, err := net.SplitHostPort(m.DialEndpoint()); err == nil && host != ip {
			msg += fmt.Sprintf("; its Kubernetes Node reports InternalIP %s, so spec.endpoint may need updating to %s", ip, net.JoinHostPort(ip, port))
		}
	}

	return r.retryLater(ctx, m, reasonNodeUnreachable, msg)
}

// shortError is err's text on one line, cut to maxDialErrorLen.
func shortError(err error) string {
	s := strings.Join(strings.Fields(err.Error()), " ")
	if len(s) > maxDialErrorLen {
		s = s[:maxDialErrorLen] + "..."
	}

	return s
}

// kubernetesNodeIP is the InternalIP of the Kubernetes Node of m, found by its hostname or, failing
// that, by its machine UUID (the Node's systemUUID); "" when there is none.
func (r *TalosMachineReconciler) kubernetesNodeIP(ctx context.Context, m *ichorv1alpha1.TalosMachine) string {
	var node *corev1.Node

	if name := cmp.Or(m.Spec.Hostname, m.Status.NodeName); name != "" {
		var n corev1.Node
		if err := r.Get(ctx, client.ObjectKey{Name: name}, &n); err == nil {
			node = &n
		}
	}

	if node == nil && m.Spec.Identity.UUID != "" {
		var nodes corev1.NodeList
		if err := r.List(ctx, &nodes); err == nil {
			for i := range nodes.Items {
				if strings.EqualFold(nodes.Items[i].Status.NodeInfo.SystemUUID, m.Spec.Identity.UUID) {
					node = &nodes.Items[i]

					break
				}
			}
		}
	}

	if node == nil {
		return ""
	}

	for _, a := range node.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			return a.Address
		}
	}

	return ""
}

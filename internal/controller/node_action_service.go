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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// talosServiceRunning is the state Talos reports for a service that is up.
const talosServiceRunning = "Running"

// workerRole is the Spec.Role of a worker machine.
const workerRole = "worker"

// serviceVerified reports whether the restarted service is Running (and healthy when it has a
// health check). why says what is missing otherwise.
func (n *nodeRun) serviceVerified() (ok bool, why string) {
	state, hasHealth, healthy, err := n.talos.ServiceState(n.dial, n.spec.Service)
	if err != nil {
		return false, fmt.Sprintf("cannot read the state of service %s: %v", n.spec.Service, err)
	}

	if state != talosServiceRunning {
		return false, fmt.Sprintf("service %s is %s, not Running yet", n.spec.Service, state)
	}

	if hasHealth && !healthy {
		return false, fmt.Sprintf("service %s is Running but not healthy yet", n.spec.Service)
	}

	return true, ""
}

// lastWorkerGuard refuses to take the last schedulable, Ready worker away: with it gone nothing
// but control planes could run workloads. It applies to a worker machine only; a control plane is
// held by controlPlaneGuard's quorum rules.
func (n *nodeRun) lastWorkerGuard(ctx context.Context, node, doing string) error {
	if n.m.Spec.Role != workerRole {
		return nil
	}

	var nodes []corev1.Node

	if n.r.KubeClient != nil {
		list, err := n.r.KubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("listing nodes to check for another worker: %w", err)
		}

		nodes = list.Items
	} else {
		var list corev1.NodeList
		if err := readerOrClient(n.r.APIReader, n.r.Client).List(ctx, &list); err != nil {
			return fmt.Errorf("listing nodes to check for another worker: %w", err)
		}

		nodes = list.Items
	}

	for i := range nodes {
		o := &nodes[i]
		if o.Name == node || o.Spec.Unschedulable || !nodeIsReady(o) {
			continue
		}

		if _, cp := o.Labels["node-role.kubernetes.io/control-plane"]; cp {
			continue
		}

		return nil
	}

	// Override: the creator held the verb on the machine when the webhook
	// admitted spec.node.overrideReason, so the guard is waived and the reason recorded.
	if reason := n.spec.EffectiveOverrideReason(); reason != "" {
		n.r.operationEvent(n.op, corev1.EventTypeWarning, "Override",
			fmt.Sprintf("%s of %q goes ahead though it is the last schedulable Ready worker, under override: %s", doing, n.m.Name, reason))

		return nil
	}

	return fmt.Errorf("refusing to %s %q: it is the last schedulable Ready worker, and no workload could run without it; "+
		"override with --override <reason> (needs the override verb on the machine)", doing, n.m.Name)
}

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

// Package deleteguard holds the deletion-protection check shared by every
// place that can delete a TalosCluster or a TalosMachine: the webhook and,
// later, the UI backend (design principle 10).
package deleteguard

import (
	"context"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// AllowDeleteAnnotation, set to AllowDeleteValue, permits deleting a
// TalosCluster or TalosMachine. Its absence stops an Argo CD prune from
// cascading into node resets.
const AllowDeleteAnnotation = "ichor.maesh.dev/allow-delete"

// AllowDeleteValue is the only AllowDeleteAnnotation value that permits deletion.
const AllowDeleteValue = "true"

// CheckDelete returns an error unless annotations carries
// AllowDeleteAnnotation set to AllowDeleteValue.
func CheckDelete(annotations map[string]string) error {
	if annotations[AllowDeleteAnnotation] == AllowDeleteValue {
		return nil
	}
	return fmt.Errorf("delete refused: set the annotation %s=%q to allow it", AllowDeleteAnnotation, AllowDeleteValue)
}

// CheckControlPlaneQuorum refuses deleting a control-plane TalosMachine when
// doing so would leave etcd without quorum or without any control plane at
// all. currentControlPlanes counts every control-plane TalosMachine the
// cluster has right now, including the one being deleted; the caller (the
// webhook today, a UI backend later -- design principle 10) does the
// counting and passes only the number in, so this stays a pure function like
// CheckDelete. Talos itself enforces neither rule (docs/design/architecture.md,
// "Nodes"), and this check is stronger than CheckDelete: the allow-delete
// annotation does not override it.
//
// etcd quorum for n members is (n/2)+1. Removing one member from n leaves
// n-1, which only fails to keep that quorum when n is 1 or 2 -- from n=3
// upward, n-1 always meets (n/2)+1. n=1 is deliberately not special-cased:
// the result (n-1=0) always triggers "last control plane" below, so a
// single-node cluster's only control plane cannot be deleted this way either;
// removing the whole cluster goes through deleting the TalosCluster instead.
func CheckControlPlaneQuorum(currentControlPlanes int) error {
	after := currentControlPlanes - 1
	if after < 1 {
		return fmt.Errorf("delete refused: last control plane (%d -> %d); delete the TalosCluster instead",
			currentControlPlanes, after)
	}
	if quorum := currentControlPlanes/2 + 1; after < quorum {
		return fmt.Errorf("delete refused: control planes %d -> %d, below etcd quorum of %d for %d members",
			currentControlPlanes, after, quorum, currentControlPlanes)
	}
	return nil
}

// CheckControlPlaneCount refuses removing a control plane when doing so
// would leave fewer control planes than desiredCount declares
// (docs/design/architecture.md, "Nodes": refused "if the count would drop
// below spec.controlPlane.count"). currentControlPlanes counts the same way
// CheckControlPlaneQuorum does: every control-plane TalosMachine the
// cluster has right now, including the one being removed.
//
// This is what makes replacing a control plane add-then-remove rather than
// remove-then-add: shrinking from desiredCount to desiredCount-1 is always
// refused here, so getting back to desiredCount after a removal requires
// approving a replacement first (desiredCount+1 -> desiredCount), never the
// other order (architecture.md: "a 3-node cluster replacing a member goes
// 3->4->3, never 3->2").
func CheckControlPlaneCount(currentControlPlanes int, desiredCount int) error {
	after := currentControlPlanes - 1
	if after < desiredCount {
		return fmt.Errorf("delete refused: control planes %d -> %d, below the declared count of %d; "+
			"either approve a replacement control plane first (then remove this one), "+
			"or lower spec.controlPlane.count first (to 1, 3 or 5) if you mean to shrink the cluster",
			currentControlPlanes, after, desiredCount)
	}
	return nil
}

// CountApprovedControlPlanes lists approved control-plane TalosMachines in
// namespace belonging to the TalosCluster named clusterName, for
// CheckControlPlaneQuorum/CheckControlPlaneCount's currentControlPlanes
// argument.
//
// reader must be a direct, uncached read -- a manager's APIReader
// (mgr.GetAPIReader()), never its cached Client (mgr.GetClient()). This
// count gates two independent delete paths (the webhook's admission check
// and removal.go's own recount under the cluster lock), and both used the
// cached client until a load-dependent test failure traced back to it: a
// cached read can still report a control plane that was deleted moments
// ago, drifting the count *high*. High is the dangerous direction here --
// it is what lets a second, concurrent deletion look safe when it would
// actually break quorum -- unlike undercounting, which only over-refuses.
// See's appendix.
//
// This is meant to be the only place in the codebase that lists
// TalosMachines to count control planes; enforced not here (this package
// structurally has only one List call site after the fix, so a guard
// scanning only it could never observe a regression) but in the two
// packages a second count could actually reappear in --
// internal/controller.TestTalosMachineListCountingStaysCentralized, which
// also scans internal/webhook/v1alpha1. If a
// quorum/removal test ever flakes, suspect this function's read path
// before the environment -- that is exactly how the cached-read bug above
// was found.
func CountApprovedControlPlanes(ctx context.Context, reader client.Reader, namespace, clusterName string) (int, error) {
	return countApproved(ctx, reader, namespace, clusterName, "controlplane")
}

// CountApprovedWorkers is CountApprovedControlPlanes for role "worker", for
// CheckLastWorker. The same direct-read rule applies: a stale high count is
// the dangerous direction (it lets the last worker's removal look safe).
func CountApprovedWorkers(ctx context.Context, reader client.Reader, namespace, clusterName string) (int, error) {
	return countApproved(ctx, reader, namespace, clusterName, "worker")
}

func countApproved(ctx context.Context, reader client.Reader, namespace, clusterName, role string) (int, error) {
	var siblings ichorv1alpha1.TalosMachineList
	if err := reader.List(ctx, &siblings, client.InNamespace(namespace)); err != nil {
		return 0, err
	}

	count := 0
	for _, m := range siblings.Items {
		if m.Spec.Role == role && m.Spec.IsApproved() && m.Spec.ClusterRef.Name == clusterName {
			count++
		}
	}

	return count, nil
}

// CheckControlPlaneApproval refuses approving one more control plane when
// that would leave more than desiredCount+1 approved. currentControlPlanes
// excludes the machine being approved. The +1 is the replacement slack
// (architecture.md "Nodes": 3->4->3, add before remove): desiredCount is odd
// (1, 3 or 5), so desiredCount+1 is the one even, "transitioning" count the
// design allows, and a second extra approval has no replacement to belong to.
// Covers the race this admission-time check cannot close.
func CheckControlPlaneApproval(currentControlPlanes int, desiredCount int) error {
	if currentControlPlanes > desiredCount {
		return fmt.Errorf("approve refused: %d control planes are already approved against spec.controlPlane.count %d; "+
			"one extra is allowed only while replacing a member, so remove one first or raise spec.controlPlane.count (to 1, 3 or 5)",
			currentControlPlanes, desiredCount)
	}
	return nil
}

// CheckLastWorker refuses leaving a cluster whose control planes are
// unschedulable (schedulable == "false") with no approved worker: nothing
// could run workloads. workersAfter is the approved-worker count after the
// operation. Used when schedulable turns false and when a worker leaves.
func CheckLastWorker(schedulable string, workersAfter int) error {
	if schedulable == "false" && workersAfter < 1 {
		return errors.New("refused: spec.controlPlane.schedulable is \"false\" and the cluster would have no approved worker; " +
			"approve a worker first, or set schedulable to \"true\"")
	}
	return nil
}

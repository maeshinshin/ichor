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
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

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
// cluster has right now, including the one being deleted; the caller does the
// counting, so this stays a pure function like CheckDelete. Talos enforces
// neither rule (docs/design/architecture.md, "Nodes"), and the allow-delete
// annotation does not override this check.
//
// etcd quorum for n members is (n/2)+1; removing one member only breaks it
// when n is 1 or 2. n=1 is not special-cased: n-1=0 triggers "last control
// plane", so a single-node cluster goes through deleting the TalosCluster.
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
// argument. A control plane already being deleted is not counted (see
// MachineFilter); the caller deciding that machine's own removal counts it back
// in through ApprovedMachines' KeepDeleting.
//
// reader must be a direct, uncached read -- a manager's APIReader
// (mgr.GetAPIReader()), never its cached Client. This count gates two
// independent delete paths (the webhook's admission check and removal.go's
// recount under the cluster lock): a cached read can still report a control
// plane deleted moments ago, drifting the count *high*, which is the dangerous
// direction (a second, concurrent deletion looks safe when it would break
// quorum). Undercounting only over-refuses. See ADR 0010's appendix.
//
// This is meant to be the only place that lists TalosMachines to count control
// planes; internal/controller.TestTalosMachineListCountingStaysCentralized
// enforces that (ADR 0010's appendix). If a quorum/removal test ever flakes,
// suspect this read path before the environment.
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
	machines, err := ApprovedMachines(ctx, reader, namespace, clusterName, MachineFilter{Role: role})
	return len(machines), err
}

// MachineFilter narrows ApprovedMachines. The zero value is every approved,
// not-being-deleted machine of the cluster.
type MachineFilter struct {
	// Role keeps only machines of this role; empty keeps every role.
	Role string
	// IncludeDeleting keeps machines that carry a DeletionTimestamp. A machine
	// in that state is on its way out (its removal finalizer is still running),
	// so by default it is not counted as staying: counting it would let a second
	// removal drop the cluster below spec.controlPlane.count while the first is
	// unfinished (ADR 0054 addendum).
	IncludeDeleting bool
	// KeepDeleting names one machine kept even though it is being deleted: the
	// machine whose own removal is being decided, which is counted "including the
	// one being removed" (CheckControlPlaneQuorum, CheckControlPlaneCount).
	KeepDeleting string
}

// ApprovedMachines is the one place that lists TalosMachines and narrows them:
// the approved machines of clusterName in namespace, by f. Every count here and
// ControlPlaneOverCap go through it, and so should any caller that needs "the
// machines of this cluster that count" (ADR 0054's addendum). reader must be a
// direct read where the result gates a disruptive action; a cached client is
// fine for picking a machine to act on.
func ApprovedMachines(
	ctx context.Context, reader client.Reader, namespace, clusterName string, f MachineFilter,
) ([]ichorv1alpha1.TalosMachine, error) {
	var siblings ichorv1alpha1.TalosMachineList
	if err := reader.List(ctx, &siblings, client.InNamespace(namespace)); err != nil {
		return nil, err
	}

	var out []ichorv1alpha1.TalosMachine

	for _, m := range siblings.Items {
		if m.Spec.ClusterRef.Name != clusterName || !m.Spec.IsApproved() || (f.Role != "" && m.Spec.Role != f.Role) {
			continue
		}

		if !m.DeletionTimestamp.IsZero() && !f.IncludeDeleting && m.Name != f.KeepDeleting {
			continue
		}

		out = append(out, m)
	}

	return out, nil
}

// approvedAt is when m was approved: the recorded decision time, or, for a
// machine approved by a writer that recorded none (a GitOps apply), the time
// the object was created.
func approvedAt(m *ichorv1alpha1.TalosMachine) time.Time {
	if d := m.Spec.ApprovalDecision; d != nil && !d.At.IsZero() {
		return d.At.Time
	}

	return m.CreationTimestamp.Time
}

// ControlPlaneOverCap reports whether the control plane named machineName is
// held because more than desiredCount+1 control planes are approved (ADR
// 0054's controller-side twin of CheckControlPlaneApproval, closing the race
// the webhook cannot). Approved control planes are ordered by approval time,
// then by name for a tie; the first desiredCount+1 are kept and later ones
// held. total is the approved control-plane count. It lists through the same
// ApprovedMachines as CountApprovedControlPlanes, so it needs the same direct reader.
func ControlPlaneOverCap(
	ctx context.Context, reader client.Reader, namespace, clusterName, machineName string, desiredCount int,
) (held bool, total int, err error) {
	cps, err := ApprovedMachines(ctx, reader, namespace, clusterName, MachineFilter{Role: "controlplane"})
	if err != nil {
		return false, 0, err
	}

	slices.SortFunc(cps, func(a, b ichorv1alpha1.TalosMachine) int {
		return cmp.Or(approvedAt(&a).Compare(approvedAt(&b)), strings.Compare(a.Name, b.Name))
	})

	at := slices.IndexFunc(cps, func(m ichorv1alpha1.TalosMachine) bool { return m.Name == machineName })

	return at > desiredCount, len(cps), nil
}

// CheckControlPlaneApproval refuses approving one more control plane when
// that would leave more than desiredCount+1 approved. currentControlPlanes
// excludes the machine being approved. The +1 is the replacement slack
// (architecture.md "Nodes": 3->4->3, add before remove): desiredCount is odd
// (1, 3 or 5), so desiredCount+1 is the one even, "transitioning" count the
// design allows, and a second extra approval has no replacement to belong to.
// ADR 0054 covers the race this admission-time check cannot close.
func CheckControlPlaneApproval(currentControlPlanes int, desiredCount int) error {
	if currentControlPlanes > desiredCount {
		return fmt.Errorf("approve refused: %d control planes are already approved against spec.controlPlane.count %d; "+
			"one extra is allowed only while replacing a member, "+
			"so remove one first or raise spec.controlPlane.count (to 1, 3 or 5)",
			currentControlPlanes, desiredCount)
	}
	return nil
}

// CheckLastWorker refuses leaving a cluster whose control planes are
// unschedulable (spec.controlPlane.schedulable is "false") with no approved
// worker: nothing could run workloads. workersAfter is the approved-worker
// count after the operation. Used when schedulable turns false and when a
// worker leaves. override is true when the caller holds the override verb
// on the object (ADR 0065, amended): the refusal is then waived.
func CheckLastWorker(unschedulable bool, workersAfter int, override bool) error {
	if unschedulable && workersAfter < 1 && !override {
		return errors.New("refused: spec.controlPlane.schedulable is \"false\" " +
			"and the cluster would have no approved worker; approve a worker first, set schedulable to \"true\", " +
			"or override under override (needs the \"override\" verb on the resource)")
	}
	return nil
}

// ResetOnDeleteAnnotation, set to "true", makes the removal of a TalosMachine also reset its node (ADR 0042,
// 0135); ResetPowerOffAnnotation, set to "true", makes the node power off after the wipe instead of rebooting.
const (
	ResetOnDeleteAnnotation = "ichor.maesh.dev/reset-on-delete"
	ResetPowerOffAnnotation = "ichor.maesh.dev/reset-power-off"
)

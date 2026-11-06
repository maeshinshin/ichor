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

package uiapi

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	hostWorkerA  = "worker-a"
	machineUUIDA = "uuid-a"
	ichorV2      = "v0.2.0"
	ichorV3      = "v0.3.0"
)

func TestOperationSummaryPerKind(t *testing.T) {
	cluster := &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{
		TalosVersion: opNewTalos, KubernetesVersion: "v1.36.6",
		Schematic: &ichorv1alpha1.TalosClusterSchematic{ID: "abcdef0123456789abcdef"},
	}}
	install := &ichorv1alpha1.IchorInstallation{
		Spec: ichorv1alpha1.IchorInstallationSpec{Version: ichorV3}, Status: ichorv1alpha1.IchorInstallationStatus{InstalledVersion: ichorV2},
	}
	node := func(action, service string) *ichorv1alpha1.TalosOperationNodeSpec {
		return &ichorv1alpha1.TalosOperationNodeSpec{
			MachineRef: corev1.LocalObjectReference{Name: "talos-02"}, Action: action, Service: service,
		}
	}

	tests := []struct {
		name string
		spec ichorv1alpha1.TalosOperationSpec
		st   ichorv1alpha1.TalosOperationStatus
		want string
	}{
		{"talos", ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindTalos}, ichorv1alpha1.TalosOperationStatus{},
			"Upgrade Talos to v1.14.5 (schematic abcdef012345…)"},
		{"k8s", ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindKubernetes}, ichorv1alpha1.TalosOperationStatus{},
			"Upgrade Kubernetes to v1.36.6"},
		{"talos from", ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindTalos}, ichorv1alpha1.TalosOperationStatus{},
			"Upgrade Talos v1.13.2 → v1.14.5 (schematic abcdef012345…)"},
		{"k8s from", ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindKubernetes}, ichorv1alpha1.TalosOperationStatus{},
			"Upgrade Kubernetes v1.36.2 → v1.36.6"},
		{"ichor", ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindIchor}, ichorv1alpha1.TalosOperationStatus{},
			"Upgrade Ichor v0.2.0 → v0.3.0"},
		{"ca", ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindCARotation}, ichorv1alpha1.TalosOperationStatus{},
			"Rotate the cluster CAs"},
		{"shutdown", ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindShutdown}, ichorv1alpha1.TalosOperationStatus{},
			"Shut down the cluster"},
		{"config", ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindConfig},
			ichorv1alpha1.TalosOperationStatus{Machines: []ichorv1alpha1.TalosOperationMachineStatus{{Name: "a"}}},
			"Apply configuration to 1 machine"},
		{"reboot", ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindNode, Node: node(ichorv1alpha1.NodeActionReboot, "")},
			ichorv1alpha1.TalosOperationStatus{}, "Reboot node talos-02"},
		{"restart", ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindNode, Node: node(ichorv1alpha1.NodeActionRestartService, "kubelet")},
			ichorv1alpha1.TalosOperationStatus{}, "Restart kubelet on node talos-02"},
		{"restore", ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindEtcdRestore, EtcdRestore: &ichorv1alpha1.TalosOperationEtcdRestoreSpec{
			Snapshot: "snap-1", BackupRef: corev1.LocalObjectReference{Name: "nightly"},
		}}, ichorv1alpha1.TalosOperationStatus{}, "Restore etcd from snap-1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			op := &ichorv1alpha1.TalosOperation{
				Name: "op", Namespace: opNS, Annotations: map[string]string{ichorv1alpha1.OperationStartedByAnnotation: userAlice},
				Spec: tc.spec, Status: tc.st,
			}
			if from := map[string]string{"talos from": opOldTalos, "k8s from": fixtureK8s}[tc.name]; from != "" {
				op.Annotations[ichorv1alpha1.OperationFromVersionAnnotation] = from
			}

			out := &pb.Operation{}
			describeOperation(out, op, cluster, install)

			if out.GetSummary() != tc.want {
				t.Fatalf("summary = %q, want %q", out.GetSummary(), tc.want)
			}

			if out.GetStartedBy() != userAlice {
				t.Fatalf("started_by = %q", out.GetStartedBy())
			}

			if len(out.GetDetails()) == 0 {
				t.Fatal("no details")
			}
		})
	}

	t.Run("without a cluster the versions are left out", func(t *testing.T) {
		out := &pb.Operation{}
		describeOperation(out, &ichorv1alpha1.TalosOperation{Spec: ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindTalos}}, nil, nil)

		if out.GetSummary() != "Upgrade Talos" {
			t.Fatalf("summary = %q", out.GetSummary())
		}
	})
}

func TestListOperationEventsFiltersByObjectAndTime(t *testing.T) {
	created := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	finished := created.Add(10 * time.Minute)
	op := &ichorv1alpha1.TalosOperation{
		Name: "talos-x", Namespace: opNS, CreationTimestamp: metav1.NewTime(created),
		Spec: ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindTalos},
		Status: ichorv1alpha1.TalosOperationStatus{
			FinishedAt: &metav1.Time{Time: finished},
			Machines:   []ichorv1alpha1.TalosOperationMachineStatus{{Name: "w1", Phase: "Completed"}},
		},
	}

	event := func(name, kind, obj, reason string, at time.Time, typ string) *corev1.Event {
		return &corev1.Event{
			Name: name, Namespace: opNS,
			InvolvedObject: corev1.ObjectReference{Kind: kind, Name: obj, Namespace: opNS},
			Reason:         reason, Message: reason + " message", Type: typ, LastTimestamp: metav1.NewTime(at),
		}
	}

	s, _ := opServer(t, op,
		event("late", "TalosOperation", "talos-x", "MachineFinished", created.Add(2*time.Minute), "Normal"),
		event("early", "TalosOperation", "talos-x", "MachineStarted", created.Add(time.Minute), "Normal"),
		event("machine", "TalosMachine", "w1", "Upgraded", created.Add(90*time.Second), "Warning"),
		event("other-machine", "TalosMachine", "w9", "Upgraded", created.Add(time.Minute), "Normal"),
		event("other-op", "TalosOperation", "talos-y", "Complete", created.Add(time.Minute), "Normal"),
		event("before", "TalosOperation", "talos-x", "Old", created.Add(-time.Hour), "Normal"),
		event("after", "TalosOperation", "talos-x", "Later", finished.Add(time.Hour), "Normal"),
	)

	resp, err := s.ListOperationEvents(context.Background(), connect.NewRequest(&pb.ListOperationEventsRequest{Operation: objectRef(opNS, "talos-x")}))
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(resp.Msg.GetEvents()))
	for _, e := range resp.Msg.GetEvents() {
		got = append(got, e.GetReason())
	}

	want := []string{"MachineStarted", "Upgraded", "MachineFinished"}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}

func TestIchorSummaryKeepsTheFromVersionAfterTheUpgrade(t *testing.T) {
	// Completed: installedVersion has caught up with the target, only the stamp remembers the from.
	install := &ichorv1alpha1.IchorInstallation{
		Spec: ichorv1alpha1.IchorInstallationSpec{Version: ichorV3}, Status: ichorv1alpha1.IchorInstallationStatus{InstalledVersion: ichorV3},
	}
	op := &ichorv1alpha1.TalosOperation{
		Annotations: map[string]string{ichorv1alpha1.OperationFromVersionAnnotation: ichorV2},
		Spec:        ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindIchor},
	}
	out := &pb.Operation{}
	describeOperation(out, op, nil, install)

	if out.GetSummary() != "Upgrade Ichor v0.2.0 → v0.3.0" {
		t.Fatalf("summary = %q", out.GetSummary())
	}
}

func TestCreatingAnIchorUpgradeStampsFromVersionAndStartedBy(t *testing.T) {
	objs := append(opObjects(), ichorInstallation(ichorOld, ichorOld))
	srv, k8s := opServer(t, objs...)
	srv.Actor = userAlice

	if _, err := srv.CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_ICHOR, ichorNew)); err != nil {
		t.Fatal(err)
	}

	ops := listOps(t, k8s)
	if len(ops) != 1 {
		t.Fatalf("ops = %d", len(ops))
	}

	if got := ops[0].Annotations[ichorv1alpha1.OperationFromVersionAnnotation]; got != ichorOld {
		t.Fatalf("from-version = %q", got)
	}

	if got := ops[0].Annotations[ichorv1alpha1.OperationStartedByAnnotation]; got != userAlice {
		t.Fatalf("started-by = %q", got)
	}
}

func TestGetOperationNamesMachinesByHostnameThenNodeName(t *testing.T) {
	withHost := opMachine(machineUUIDA, "worker", nil, nil)
	withHost.Spec.Hostname = hostWorkerA

	withNode := opMachine("uuid-b", "worker", nil, nil)
	withNode.Status.NodeName = "node-b"

	bare := opMachine("uuid-c", "worker", nil, nil)

	op := &ichorv1alpha1.TalosOperation{
		Name: "talos-x", Namespace: opNS,
		Spec: ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindTalos},
		Status: ichorv1alpha1.TalosOperationStatus{Machines: []ichorv1alpha1.TalosOperationMachineStatus{
			{Name: machineUUIDA}, {Name: "uuid-b"}, {Name: "uuid-c"},
		}},
	}

	s, _ := opServer(t, append(opObjects(), withHost, withNode, bare, op)...)

	resp, err := s.GetOperation(context.Background(), connect.NewRequest(&pb.GetOperationRequest{Operation: objectRef(opNS, "talos-x")}))
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(resp.Msg.GetOperation().GetMachines()))
	for _, m := range resp.Msg.GetOperation().GetMachines() {
		got = append(got, m.GetName()+"="+m.GetDisplayName())
	}

	want := []string{"uuid-a=worker-a", "uuid-b=node-b", "uuid-c="}
	if !slices.Equal(got, want) {
		t.Fatalf("machines = %v, want %v", got, want)
	}
}

func TestNodeActionSummaryNamesTheMachineByHostname(t *testing.T) {
	m := opMachine(machineUUIDA, "worker", nil, nil)
	m.Spec.Hostname = hostWorkerA

	op := &ichorv1alpha1.TalosOperation{
		Name: "node-x", Namespace: opNS,
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindNode,
			Node: &ichorv1alpha1.TalosOperationNodeSpec{MachineRef: corev1.LocalObjectReference{Name: machineUUIDA}, Action: ichorv1alpha1.NodeActionReboot},
		},
		Status: ichorv1alpha1.TalosOperationStatus{Machines: []ichorv1alpha1.TalosOperationMachineStatus{{Name: machineUUIDA}}},
	}

	s, _ := opServer(t, append(opObjects(), m, op)...)

	resp, err := s.GetOperation(context.Background(), connect.NewRequest(&pb.GetOperationRequest{Operation: objectRef(opNS, "node-x")}))
	if err != nil {
		t.Fatal(err)
	}

	if got := resp.Msg.GetOperation().GetSummary(); got != "Reboot node worker-a" {
		t.Fatalf("summary = %q", got)
	}
}

// A freshly created, paused node operation has no status.machines yet; the machine is named from
// spec.node.machineRef.
func TestPausedNodeActionWithoutStatusNamesTheMachineByHostname(t *testing.T) {
	m := opMachine(machineUUIDA, "worker", nil, nil)
	m.Spec.Hostname = hostWorkerA

	op := &ichorv1alpha1.TalosOperation{
		Name: "node-y", Namespace: opNS,
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindNode, Paused: true,
			Node: &ichorv1alpha1.TalosOperationNodeSpec{MachineRef: corev1.LocalObjectReference{Name: machineUUIDA}, Action: ichorv1alpha1.NodeActionReboot},
		},
	}

	s, _ := opServer(t, append(opObjects(), m, op)...)

	resp, err := s.GetOperation(context.Background(), connect.NewRequest(&pb.GetOperationRequest{Operation: objectRef(opNS, "node-y")}))
	if err != nil {
		t.Fatal(err)
	}

	o := resp.Msg.GetOperation()
	if o.GetSummary() != "Reboot node worker-a" {
		t.Fatalf("summary = %q", o.GetSummary())
	}

	for _, d := range o.GetDetails() {
		if d.GetKey() == "Machine" && d.GetValue() != hostWorkerA {
			t.Fatalf("Machine = %q", d.GetValue())
		}
	}
}

// "Installed Ichor version" is the version before the upgrade (the stamped from-version); once the
// operation has Completed it is the old one, so the row goes (the summary keeps from → to).
func TestIchorDetailsDropTheInstalledVersionOnceCompleted(t *testing.T) {
	install := &ichorv1alpha1.IchorInstallation{
		Spec: ichorv1alpha1.IchorInstallationSpec{Version: ichorV3}, Status: ichorv1alpha1.IchorInstallationStatus{InstalledVersion: ichorV3},
	}
	detail := func(phase string) string {
		op := &ichorv1alpha1.TalosOperation{
			Annotations: map[string]string{ichorv1alpha1.OperationFromVersionAnnotation: ichorV2},
			Spec:        ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindIchor},
			Status:      ichorv1alpha1.TalosOperationStatus{Phase: phase},
		}
		out := &pb.Operation{}
		describeOperation(out, op, nil, install)

		for _, d := range out.GetDetails() {
			if d.GetKey() == "Installed Ichor version" {
				return d.GetValue()
			}
		}

		return ""
	}

	if got := detail(ichorv1alpha1.TalosOperationPhaseInProgress); got != ichorV2 {
		t.Errorf("in progress: installed = %q, want %s", got, ichorV2)
	}

	if got := detail(ichorv1alpha1.TalosOperationPhaseCompleted); got != "" {
		t.Errorf("completed: installed = %q, want no row", got)
	}
}

// A completed Ichor upgrade records only its from-version; the installation's live target may belong to
// a later upgrade, so the summary and details leave the target out.
func TestCompletedIchorUpgradeDoesNotShowTheLiveInstallationTarget(t *testing.T) {
	install := &ichorv1alpha1.IchorInstallation{
		Spec: ichorv1alpha1.IchorInstallationSpec{Version: ichorV3}, Status: ichorv1alpha1.IchorInstallationStatus{InstalledVersion: ichorV3},
	}
	op := &ichorv1alpha1.TalosOperation{
		Annotations: map[string]string{ichorv1alpha1.OperationFromVersionAnnotation: ichorV2},
		Spec:        ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindIchor},
		Status:      ichorv1alpha1.TalosOperationStatus{Phase: ichorv1alpha1.TalosOperationPhaseCompleted},
	}
	out := &pb.Operation{}
	describeOperation(out, op, nil, install)

	if out.GetSummary() != "Upgrade Ichor from v0.2.0" {
		t.Fatalf("summary = %q", out.GetSummary())
	}

	for _, d := range out.GetDetails() {
		if d.GetKey() == "Target Ichor version" {
			t.Fatalf("target shown: %q", d.GetValue())
		}
	}
}

func TestListedNodeActionNamesTheMachineByHostname(t *testing.T) {
	m := opMachine(machineUUIDA, "worker", nil, nil)
	m.Spec.Hostname = hostWorkerA

	op := &ichorv1alpha1.TalosOperation{
		Name: "node-z", Namespace: opNS,
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindNode,
			Node: &ichorv1alpha1.TalosOperationNodeSpec{MachineRef: corev1.LocalObjectReference{Name: machineUUIDA}, Action: ichorv1alpha1.NodeActionReboot},
		},
	}

	s, _ := opServer(t, append(opObjects(), m, op)...)

	resp, err := s.ListOperations(context.Background(), connect.NewRequest(&pb.ListOperationsRequest{}))
	if err != nil {
		t.Fatal(err)
	}

	if len(resp.Msg.GetOperations()) != 1 || resp.Msg.GetOperations()[0].GetSummary() != "Reboot node "+hostWorkerA {
		t.Fatalf("operations = %v", resp.Msg.GetOperations())
	}
}

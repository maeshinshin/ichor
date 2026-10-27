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
	"testing"
	"time"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

func TestOperationSummaryPerKind(t *testing.T) {
	cluster := &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{
		TalosVersion: opNewTalos, KubernetesVersion: "v1.37.1",
		Schematic: &ichorv1alpha1.TalosClusterSchematic{ID: "abcdef0123456789abcdef"},
	}}
	install := &ichorv1alpha1.IchorInstallation{
		Spec: ichorv1alpha1.IchorInstallationSpec{Version: "v0.3.0"}, Status: ichorv1alpha1.IchorInstallationStatus{InstalledVersion: "v0.2.0"},
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
			"Upgrade Talos to v1.14.4 (schematic abcdef012345…)"},
		{"k8s", ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindKubernetes}, ichorv1alpha1.TalosOperationStatus{},
			"Upgrade Kubernetes to v1.37.1"},
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
				Name: "op", Namespace: opNS, Annotations: map[string]string{ichorv1alpha1.OperationStartedByAnnotation: "alice"},
				Spec: tc.spec, Status: tc.st,
			}
			out := &pb.Operation{}
			describeOperation(out, op, cluster, install)

			if out.GetSummary() != tc.want {
				t.Fatalf("summary = %q, want %q", out.GetSummary(), tc.want)
			}

			if out.GetStartedBy() != "alice" {
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

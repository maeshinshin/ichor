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

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	ichorOld = "v0.1.0"
	ichorNew = "v0.2.0"
)

func ichorInstallation(mode, version, installed string) *ichorv1alpha1.IchorInstallation {
	inst := &ichorv1alpha1.IchorInstallation{
		Name: ichorv1alpha1.IchorInstallationName,
		Spec: ichorv1alpha1.IchorInstallationSpec{ManagementMode: mode, Version: version},
	}
	inst.Status.InstalledVersion = installed

	return inst
}

func ichorCreate(target string) *connect.Request[pb.CreateOperationRequest] {
	return connect.NewRequest(&pb.CreateOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_ICHOR, Target: target,
	})
}

// An Ichor upgrade writes spec.version of the IchorInstallation, then creates the operation. The
// retired external mode is treated as ichor.
func TestIchorUpgradeWritesTheTargetAndCreatesTheOperation(t *testing.T) {
	for _, mode := range []string{ichorv1alpha1.IchorManagementModeIchor, ichorv1alpha1.IchorManagementModeExternal} {
		t.Run(mode, func(t *testing.T) {
			srv, k8s := opServer(t, append(opObjects(), ichorInstallation(mode, ichorOld, ichorOld))...)

			res, err := srv.CreateOperation(context.Background(), ichorCreate(ichorNew))
			if err != nil {
				t.Fatal(err)
			}

			if !res.Msg.GetSpecChanged() || res.Msg.GetOperation() == nil {
				t.Fatalf("response = %v", res.Msg)
			}

			var inst ichorv1alpha1.IchorInstallation
			if err := k8s.Get(context.Background(), types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
				t.Fatal(err)
			}

			if inst.Spec.Version != ichorNew {
				t.Fatalf("spec.version = %s, want %s", inst.Spec.Version, ichorNew)
			}

			if ops := listOps(t, k8s); len(ops) != 1 || ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindIchor {
				t.Fatalf("operations = %+v", ops)
			}
		})
	}
}

func TestGetIchorInstallation(t *testing.T) {
	running := &ichorv1alpha1.TalosOperation{
		Name: "up", Namespace: opNS,
		Spec:   ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindIchor},
		Status: ichorv1alpha1.TalosOperationStatus{Phase: ichorv1alpha1.TalosOperationPhaseInProgress},
	}
	talosRunning := running.DeepCopy()
	talosRunning.Spec.Kind = ichorv1alpha1.TalosOperationKindTalos

	for name, tc := range map[string]struct {
		objs                 []client.Object
		installed, target    string
		upgrading, available bool
	}{
		"newer target known":        {[]client.Object{ichorInstallation(ichorv1alpha1.IchorManagementModeIchor, ichorNew, ichorOld)}, ichorOld, ichorNew, false, true},
		"up to date":                {[]client.Object{ichorInstallation(ichorv1alpha1.IchorManagementModeIchor, ichorOld, ichorOld)}, ichorOld, ichorOld, false, false},
		"older target":              {[]client.Object{ichorInstallation(ichorv1alpha1.IchorManagementModeIchor, ichorOld, ichorNew)}, ichorNew, ichorOld, false, false},
		"upgrade running":           {[]client.Object{ichorInstallation(ichorv1alpha1.IchorManagementModeIchor, ichorNew, ichorOld), running}, ichorOld, ichorNew, true, false},
		"other kind running":        {[]client.Object{ichorInstallation(ichorv1alpha1.IchorManagementModeIchor, ichorNew, ichorOld), talosRunning}, ichorOld, ichorNew, false, true},
		"installed version unknown": {[]client.Object{ichorInstallation(ichorv1alpha1.IchorManagementModeIchor, ichorNew, "")}, "", ichorNew, false, false},
		"no installation":           {nil, "", "", false, false},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := opServer(t, tc.objs...)

			res, err := srv.GetIchorInstallation(context.Background(), connect.NewRequest(&pb.GetIchorInstallationRequest{}))
			if err != nil {
				t.Fatal(err)
			}

			m := res.Msg
			if m.GetInstalledVersion() != tc.installed || m.GetTargetVersion() != tc.target ||
				m.GetUpgrading() != tc.upgrading || m.GetUpdateAvailable() != tc.available {
				t.Fatalf("got %v", m)
			}
		})
	}
}

// A rolled-back upgrade leaves spec.version at the failed target while the controller still
// reports the version that runs: the plan's current version is the one that runs.
func TestIchorPlanAfterARolledBackUpgradeReportsTheRunningVersion(t *testing.T) {
	srv, _ := opServer(t, append(opObjects(), ichorInstallation(ichorv1alpha1.IchorManagementModeIchor, ichorNew, ichorOld))...)

	plan, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_ICHOR,
	}))
	if err != nil {
		t.Fatal(err)
	}

	if got := plan.Msg.GetCurrent(); got != ichorOld {
		t.Fatalf("current = %q, want the running %q, not the failed target", got, ichorOld)
	}
}

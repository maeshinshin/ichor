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
	"strings"
	"testing"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// When Argo CD owns the IchorInstallation, an Ichor upgrade is a pull
// request that changes spec.version in Git; nothing is written to the cluster.

func ichorInstallation(mode, version, installed string, tracked bool) *ichorv1alpha1.IchorInstallation {
	inst := &ichorv1alpha1.IchorInstallation{
		Name: ichorv1alpha1.IchorInstallationName,
		Spec: ichorv1alpha1.IchorInstallationSpec{ManagementMode: mode, Version: version},
	}
	inst.Status.InstalledVersion = installed

	if tracked {
		inst.Annotations = map[string]string{argoTrackingAnnot: "prod-root:ichor.maesh.dev/IchorInstallation:/ichor"}
	}

	return inst
}

func ichorCreate(target string) *connect.Request[pb.CreateOperationRequest] {
	return connect.NewRequest(&pb.CreateOperationRequest{
		Cluster: objectRef(trackedTestNS, "c"), Kind: pb.OperationKind_OPERATION_KIND_ICHOR, Target: target,
	})
}

func (e *prEnv) installation(t *testing.T) ichorv1alpha1.IchorInstallation {
	t.Helper()

	var inst ichorv1alpha1.IchorInstallation
	if err := e.k8s.Get(context.Background(), types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
		t.Fatal(err)
	}

	return inst
}

func (e *prEnv) operations(t *testing.T) int {
	t.Helper()

	var l ichorv1alpha1.TalosOperationList
	if err := e.k8s.List(context.Background(), &l); err != nil {
		t.Fatal(err)
	}

	return len(l.Items)
}

func TestIchorUpgradeOfATrackedInstallationOpensAPR(t *testing.T) {
	e := newPREnv(t, prOpts{})
	if err := e.k8s.Create(context.Background(), ichorInstallation("ichor", "v0.1.0", "v0.1.0", true)); err != nil {
		t.Fatal(err)
	}

	plan, err := e.srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(trackedTestNS, "c"), Kind: pb.OperationKind_OPERATION_KIND_ICHOR, Target: "v0.2.0",
	}))
	if err != nil || plan.Msg.GetBlocked() != "" || !plan.Msg.GetOpensPullRequest() {
		t.Fatalf("plan err=%v msg=%v", err, plan.Msg)
	}

	res, err := e.srv.CreateOperation(context.Background(), ichorCreate("v0.2.0"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if res.Msg.GetPullRequestUrl() != prPRURL || res.Msg.GetOperation() != nil || res.Msg.GetSpecChanged() {
		t.Fatalf("response = %v", res.Msg)
	}

	if got := e.installation(t).Spec.Version; got != "v0.1.0" {
		t.Fatalf("the cluster's spec.version was written: %s", got)
	}

	if e.operations(t) != 0 {
		t.Fatal("an operation was created")
	}

	edited := fileOnBranch(t, e.bare, prAppPath+"/ichor.yaml")
	if !strings.Contains(edited, "version: v0.2.0") || strings.Contains(edited, "version: v0.1.0") {
		t.Fatalf("spec.version was not bumped in Git:\n%s", edited)
	}

	for _, want := range []string{"Upgrade Ichor to v0.2.0", "v0.1.0"} {
		if !strings.Contains(e.host.body["title"]+e.host.body["body"], want) {
			t.Errorf("the PR text lacks %q: %v", want, e.host.body)
		}
	}
}

func TestIchorUpgradeOfATrackedInstallationNeedsAConnectedRepository(t *testing.T) {
	e := newPREnv(t, prOpts{noRepo: true})
	if err := e.k8s.Create(context.Background(), ichorInstallation("ichor", "v0.1.0", "v0.1.0", true)); err != nil {
		t.Fatal(err)
	}

	_, err := e.srv.CreateOperation(context.Background(), ichorCreate("v0.2.0"))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "tracked") {
		t.Fatalf("err = %v", err)
	}

	if e.host.hits != 0 || e.installation(t).Spec.Version != "v0.1.0" || e.operations(t) != 0 {
		t.Fatal("something was written")
	}
}

func TestIchorUpgradeWithoutATargetStillCreatesTheOperation(t *testing.T) {
	e := newPREnv(t, prOpts{})
	if err := e.k8s.Create(context.Background(), ichorInstallation("ichor", "v0.2.0", "v0.1.0", true)); err != nil {
		t.Fatal(err)
	}

	res, err := e.srv.CreateOperation(context.Background(), ichorCreate(""))
	if err != nil || res.Msg.GetPullRequestUrl() != "" || res.Msg.GetOperation() == nil {
		t.Fatalf("err=%v res=%v", err, res.Msg)
	}

	if e.host.hits != 0 {
		t.Fatal("a pull request was opened")
	}
}

func TestExternalInstallationWithoutGitCannotBeUpgradedFromHere(t *testing.T) {
	e := newPREnv(t, prOpts{})
	if err := e.k8s.Create(context.Background(), ichorInstallation("external", "v0.1.0", "v0.1.0", false)); err != nil {
		t.Fatal(err)
	}

	_, err := e.srv.CreateOperation(context.Background(), ichorCreate("v0.2.0"))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "external") {
		t.Fatalf("err = %v", err)
	}

	if e.installation(t).Spec.Version != "v0.1.0" || e.operations(t) != 0 {
		t.Fatal("something was written")
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
		objs                           []client.Object
		installed, target              string
		upgrading, available, wantNone bool
	}{
		"newer target known":        {[]client.Object{ichorInstallation("ichor", "v0.2.0", "v0.1.0", false)}, "v0.1.0", "v0.2.0", false, true, false},
		"up to date":                {[]client.Object{ichorInstallation("ichor", "v0.1.0", "v0.1.0", false)}, "v0.1.0", "v0.1.0", false, false, false},
		"older target":              {[]client.Object{ichorInstallation("ichor", "v0.1.0", "v0.2.0", false)}, "v0.2.0", "v0.1.0", false, false, false},
		"upgrade running":           {[]client.Object{ichorInstallation("ichor", "v0.2.0", "v0.1.0", false), running}, "v0.1.0", "v0.2.0", true, false, false},
		"other kind running":        {[]client.Object{ichorInstallation("ichor", "v0.2.0", "v0.1.0", false), talosRunning}, "v0.1.0", "v0.2.0", false, true, false},
		"installed version unknown": {[]client.Object{ichorInstallation("ichor", "v0.2.0", "", false)}, "", "v0.2.0", false, false, false},
		"no installation":           {nil, "", "", false, false, true},
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

			if tc.wantNone && m.GetManagementMode() != "" {
				t.Fatalf("mode = %q", m.GetManagementMode())
			}
		})
	}
}

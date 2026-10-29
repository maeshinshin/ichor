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

package cilium

import (
	"context"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/release"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/maeshinshin/ichor/pkg/cni"
)

func agentDaemonSet(image string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ReleaseNamespace, Name: ReleaseName},
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: agentContainerName, Image: image}},
		}}},
	}
}

func preflightInput(t *testing.T, kube *fake.Clientset) PreflightInput {
	t.Helper()

	vals := effective(t, nil)

	return PreflightInput{
		Kube: kube, Get: newLiveCluster(t, vals).get, IchorNamespace: ichorNS,
		Effective: vals, UserValues: vals, KubeProxy: cni.KubeProxyEnabled, Tunnel: tunnelVxlan,
	}
}

func TestPreflight_CaseCNoRecordPasses(t *testing.T) {
	kube := fake.NewSimpleClientset(agentDaemonSet("quay.io/cilium/cilium:v" + ChartVersion))

	rep, err := Preflight(t.Context(), preflightInput(t, kube))
	if err != nil {
		t.Fatal(err)
	}

	if rep.Foreign != nil {
		t.Errorf("Foreign = %v, want none", rep.Foreign)
	}

	if err := rep.Err(true, false); err != nil {
		t.Errorf("Err = %v, want nil (owned-key changes accepted, diff empty)", err)
	}
}

func TestPreflight_CaseBReadsTheRecordAndGatesIt(t *testing.T) {
	kube := fake.NewSimpleClientset(agentDaemonSet("quay.io/cilium/cilium:v" + ChartVersion))
	storeRecord(t, kube, kubeSystem, foreignRelease(t, 1, release.StatusDeployed, ChartVersion,
		map[string]any{"ipam": map[string]any{"mode": "cluster-pool"}, "mine": "kept"}))

	rep, err := Preflight(t.Context(), preflightInput(t, kube))
	if err != nil {
		t.Fatal(err)
	}

	if rep.Foreign == nil || rep.Foreign.Latest().Release.Version != 1 {
		t.Fatalf("Foreign = %v", rep.Foreign)
	}

	if rep.UserValues["mine"] != "kept" {
		t.Errorf("UserValues = %v, want the record's own keys", rep.UserValues)
	}

	if c := findConflict(rep.Conflicts, "ipam.mode"); c == nil || c.Level != Blocking {
		t.Fatalf("ipam.mode conflict = %v, want blocking", c)
	}

	// A blocking conflict is never accepted.
	if err := rep.Err(true, true); err == nil || !strings.Contains(err.Error(), "ipam.mode") {
		t.Errorf("Err = %v, want the ipam.mode refusal", err)
	}
}

func TestPreflight_RefusesFailedRecordAndOldVersion(t *testing.T) {
	kube := fake.NewSimpleClientset(agentDaemonSet("quay.io/cilium/cilium:v1.1.0"))
	storeRecord(t, kube, kubeSystem, foreignRelease(t, 1, release.StatusFailed, "1.1.0", nil))

	rep, err := Preflight(t.Context(), preflightInput(t, kube))
	if err != nil {
		t.Fatal(err)
	}

	if rep.ForeignErr == nil || rep.VersionErr == nil {
		t.Errorf("ForeignErr = %v, VersionErr = %v, want both", rep.ForeignErr, rep.VersionErr)
	}
}

func TestPreflight_DiffGateNeedsAcceptance(t *testing.T) {
	kube := fake.NewSimpleClientset(agentDaemonSet("quay.io/cilium/cilium:v" + ChartVersion))
	in := preflightInput(t, kube)
	in.Get = func(ctx context.Context, ref ObjectRef) (map[string]any, error) {
		return nil, nil // nothing exists live: every object is missing
	}

	rep, err := Preflight(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	if len(rep.Differences) == 0 {
		t.Fatal("want ObjectMissing differences")
	}

	if rep.Err(true, false) == nil || rep.Err(true, true) != nil {
		t.Errorf("Err(.., false) = %v, Err(.., true) = %v", rep.Err(true, false), rep.Err(true, true))
	}
}

func TestPreflight_NoDaemonSetIsAnError(t *testing.T) {
	if _, err := Preflight(t.Context(), preflightInput(t, fake.NewSimpleClientset())); err == nil {
		t.Error("want an error without a cilium DaemonSet")
	}
}

func TestFindInlineManifest(t *testing.T) {
	ds := "apiVersion: apps/v1\nkind: DaemonSet\nmetadata:\n  name: cilium\n  namespace: kube-system\n"
	other := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cilium\n  namespace: kube-system\n"
	wrongNS := "apiVersion: apps/v1\nkind: DaemonSet\nmetadata:\n  name: cilium\n  namespace: other\n"

	got, err := FindInlineManifest(map[string]string{
		"a-config": other, "wrong-ns": wrongNS, "junk": ": : not yaml [", "my-cni": other + "---\n" + ds,
	})
	if err != nil || got != "my-cni" {
		t.Errorf("got %q, %v; want my-cni", got, err)
	}

	if got, err := FindInlineManifest(map[string]string{"a": other}); err != nil || got != "" {
		t.Errorf("got %q, %v; want none", got, err)
	}

	if _, err := FindInlineManifest(map[string]string{"a": ds, "b": ds}); err == nil {
		t.Error("want an error for two matches")
	}
}

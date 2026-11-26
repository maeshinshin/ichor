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
	"errors"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/release"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/maeshinshin/ichor/pkg/cni"
)

const modeClusterPool = "cluster-pool"

func agentDaemonSet(image string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		Namespace: ReleaseNamespace, Name: ReleaseName,
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: agentLabels},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: agentContainerName, Image: image}},
			}},
		},
		Status: appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, UpdatedNumberScheduled: 3, NumberReady: 3},
	}
}

var (
	agentLabels    = map[string]string{"k8s-app": ReleaseName}
	operatorLabels = map[string]string{"app.kubernetes.io/name": operatorName}
)

func operatorDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		Namespace: ReleaseNamespace, Name: operatorName,
		Spec:   appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: operatorLabels}},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
}

// ciliumPod is an agent pod on node, Ready or not.
func ciliumPod(node string, ready bool) *corev1.Pod {
	st := corev1.ConditionFalse
	if ready {
		st = corev1.ConditionTrue
	}

	return &corev1.Pod{
		Namespace: ReleaseNamespace, Name: "cilium-" + node, Labels: agentLabels,
		Spec:   corev1.PodSpec{NodeName: node},
		Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: st}}},
	}
}

func preflightInput(t *testing.T, kube *fake.Clientset) PreflightInput {
	t.Helper()

	vals := effective(t, nil)

	return PreflightInput{
		Kube: kube, Get: newLiveCluster(t, vals).get, IchorNamespace: ichorNS,
		Effective: vals, UserValues: vals, KubeProxy: cni.KubeProxyEnabled, Tunnel: tunnelVxlan,
		KubePrism: func(context.Context) error { return nil },
	}
}

func TestPreflight_CaseCNoRecordPasses(t *testing.T) {
	kube := fake.NewSimpleClientset(agentDaemonSet("quay.io/cilium/cilium:v"+ChartVersion), operatorDeployment())

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
	kube := fake.NewSimpleClientset(agentDaemonSet("quay.io/cilium/cilium:v"+ChartVersion), operatorDeployment())
	storeRecord(t, kube, kubeSystem, foreignRelease(t, 1, release.StatusDeployed, ChartVersion,
		map[string]any{"ipam": map[string]any{"mode": modeClusterPool}, "mine": "kept"}))

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
	kube := fake.NewSimpleClientset(agentDaemonSet("quay.io/cilium/cilium:v1.1.0"), operatorDeployment())
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
	kube := fake.NewSimpleClientset(agentDaemonSet("quay.io/cilium/cilium:v"+ChartVersion), operatorDeployment())
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

// ADR 0103 precondition 5, the agents: one node whose agent is not Ready refuses the takeover and
// the report names the node.
func TestPreflight_AgentNotReadyOnOneNodeRefuses(t *testing.T) {
	ds := agentDaemonSet("quay.io/cilium/cilium:v" + ChartVersion)
	ds.Status.NumberReady = 2
	kube := fake.NewSimpleClientset(ds, operatorDeployment(),
		ciliumPod("node-a", true), ciliumPod("node-b", false), ciliumPod("node-c", true))

	rep, err := Preflight(t.Context(), preflightInput(t, kube))
	if err != nil {
		t.Fatal(err)
	}

	err = rep.Err(true, true)
	if err == nil || !strings.Contains(err.Error(), "on node node-b") || !strings.Contains(err.Error(), "2 of 3 agents") {
		t.Fatalf("Err = %v, want a refusal naming node-b and the 2 of 3 count", err)
	}
}

func TestPreflight_OperatorNotReadyRefuses(t *testing.T) {
	op := operatorDeployment()
	op.Status.ReadyReplicas = 0
	kube := fake.NewSimpleClientset(agentDaemonSet("quay.io/cilium/cilium:v"+ChartVersion), op)

	rep, err := Preflight(t.Context(), preflightInput(t, kube))
	if err != nil {
		t.Fatal(err)
	}

	if err := rep.Err(true, true); err == nil || !strings.Contains(err.Error(), operatorName) {
		t.Fatalf("Err = %v, want a refusal naming the operator", err)
	}
}

// ADR 0103 precondition 5, KubePrism: the probe's verdict is the report's, and no probe is a
// refusal, not a pass.
func TestPreflight_KubePrismNotAnsweringRefuses(t *testing.T) {
	kube := fake.NewSimpleClientset(agentDaemonSet("quay.io/cilium/cilium:v"+ChartVersion), operatorDeployment())

	in := preflightInput(t, kube)
	in.KubePrism = func(context.Context) error { return errors.New("node node-b: KubePrism is not running") }

	rep, err := Preflight(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	if err := rep.Err(true, true); err == nil || !strings.Contains(err.Error(), "node-b") {
		t.Fatalf("Err = %v, want the probe's refusal naming node-b", err)
	}

	in.KubePrism = nil

	if rep, err = Preflight(t.Context(), in); err != nil {
		t.Fatal(err)
	}

	if err := rep.Err(true, true); err == nil {
		t.Fatal("an unprobed KubePrism was accepted")
	}
}

// A rollout in progress (UpdatedNumberScheduled below Desired) is not Ready, even when every old pod is.
func TestCheckAgentsReady_RolloutInProgressIsNotReady(t *testing.T) {
	ds := agentDaemonSet("quay.io/cilium/cilium:v" + ChartVersion)
	ds.Status.UpdatedNumberScheduled = 1
	kube := fake.NewSimpleClientset(ds, operatorDeployment())

	notReady, err := CheckAgentsReady(t.Context(), kube)
	if err != nil {
		t.Fatal(err)
	}

	if notReady == nil || !strings.Contains(notReady.Error(), "1 of 3 agents updated") {
		t.Fatalf("notReady = %v, want a rollout refusal", notReady)
	}
}

func TestPreflight_RefusesAPrivateKeyInTheForeignRecord(t *testing.T) {
	kube := fake.NewSimpleClientset(agentDaemonSet("quay.io/cilium/cilium:v"+ChartVersion), operatorDeployment())
	storeRecord(t, kube, kubeSystem, foreignRelease(t, 1, release.StatusDeployed, ChartVersion,
		keyAt(hubbleServerKey)))

	rep, err := Preflight(t.Context(), preflightInput(t, kube))
	if err != nil {
		t.Fatal(err)
	}

	err = rep.Err(true, true)
	if err == nil || !strings.Contains(err.Error(), hubbleServerKey) ||
		!strings.Contains(err.Error(), "hubble.tls.server.existingSecret") {
		t.Fatalf("Err = %v, want a refusal naming %s and its existingSecret", err, hubbleServerKey)
	}
}

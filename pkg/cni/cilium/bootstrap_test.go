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
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefakeclient "k8s.io/client-go/kubernetes/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
)

func readyNode(name string, ready corev1.ConditionStatus) *corev1.Node {
	return &corev1.Node{
		Name:   name,
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}},
	}
}

// Bootstrap creates ichor-system, installs the release for the cluster and waits for the nodes;
// a second run upgrades instead of installing again.
func TestBootstrap_InstallsThenWaitsForNodes(t *testing.T) {
	cfg := fakeConfig()
	kube := kubefakeclient.NewSimpleClientset(readyNode("a", corev1.ConditionTrue))
	cluster := &ichorv1alpha1.TalosCluster{}

	res, err := bootstrap(t.Context(), kube, cfg, cluster, cni.KubeProxyEnabled, time.Second, time.Millisecond)
	if err != nil || !res.Adopted {
		t.Fatalf("first run: %+v, %v; want a fresh install", res, err)
	}

	if _, err := kube.CoreV1().Namespaces().Get(t.Context(), RecordNamespace, metav1.GetOptions{}); err != nil {
		t.Errorf("namespace %s not created: %v", RecordNamespace, err)
	}

	res, err = bootstrap(t.Context(), kube, cfg, cluster, cni.KubeProxyEnabled, time.Second, time.Millisecond)
	if err != nil || res.Adopted || res.Release.Version != 2 {
		t.Fatalf("second run: %+v, %v; want an upgrade to revision 2", res, err)
	}
}

func TestBootstrap_NodeNeverReadyNamesIt(t *testing.T) {
	kube := kubefakeclient.NewSimpleClientset(readyNode("a", corev1.ConditionTrue),
		readyNode("stuck", corev1.ConditionFalse))

	_, err := bootstrap(t.Context(), kube, fakeConfig(), &ichorv1alpha1.TalosCluster{}, cni.KubeProxyEnabled,
		50*time.Millisecond, 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "stuck") {
		t.Fatalf("err = %v, want one naming the NotReady node", err)
	}
}

// An HA cluster is bootstrapped with one node: the release is made for the nodes that exist, so its
// operator runs one replica and can be rolled later (ADR 0113 amendment 2026-11-04).
func TestBootstrap_OperatorFollowsTheNodesThatExist(t *testing.T) {
	cfg := fakeConfig()
	kube := kubefakeclient.NewSimpleClientset(readyNode("a", corev1.ConditionTrue))
	cluster := &ichorv1alpha1.TalosCluster{}
	cluster.Spec.ControlPlane.Count = 3

	if _, err := bootstrap(t.Context(), kube, cfg, cluster, cni.KubeProxyEnabled, time.Second, time.Millisecond); err != nil {
		t.Fatal(err)
	}

	rel, err := cfg.Releases.Last(ReleaseName)
	if err != nil {
		t.Fatal(err)
	}

	if op, _ := rel.Config["operator"].(map[string]any); fmt.Sprint(op["replicas"]) != "1" {
		t.Errorf("operator = %v, want one replica for the one node", rel.Config["operator"])
	}
}

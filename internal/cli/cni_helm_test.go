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

package cli

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubefakeclient "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

func memoryHelmConfig() *action.Configuration {
	caps := chartutil.DefaultCapabilities.Copy()
	caps.KubeVersion = chartutil.KubeVersion{Version: "v1.36.2", Major: "1", Minor: "36"}

	return &action.Configuration{
		Releases:     storage.Init(driver.NewMemory()),
		KubeClient:   &kubefake.PrintingKubeClient{Out: io.Discard},
		Capabilities: caps,
		Log:          func(string, ...any) {},
	}
}

const cniHelmTestCluster = "home"

func ciliumTestCluster(values string) *ichorv1alpha1.TalosCluster {
	c := &ichorv1alpha1.TalosCluster{Namespace: ichorControllerNamespace, Name: cniHelmTestCluster}
	c.Spec.CNI.Preset = string(detectedCilium)

	if values != "" {
		c.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{Values: &apiextensionsv1.JSON{Raw: []byte(values)}}
	}

	return c
}

// `ichor cni helm apply` adopts on the first run and upgrades on every later one, with the
// values read from the TalosCluster each time.
func TestCNIHelmApplyAdoptsThenUpgrades(t *testing.T) {
	key := types.NamespacedName{Namespace: ichorControllerNamespace, Name: cniHelmTestCluster}
	cfg := memoryHelmConfig()

	run := func(values string) (string, error) {
		k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(ciliumTestCluster(values)).Build()

		var out bytes.Buffer

		err := runCNIHelmApply(t.Context(), k8s, cfg, key, "", 0, false, 0, nil, nil, &out)

		return out.String(), err
	}

	out, err := run("")
	if err != nil || !strings.Contains(out, "adopted: revision 1, status deployed") {
		t.Fatalf("first run: %q, %v; want an adoption at revision 1", out, err)
	}

	out, err = run(`{"debug":{"enabled":true}}`)
	if err != nil || !strings.Contains(out, "upgraded: revision 2, status deployed") {
		t.Fatalf("second run: %q, %v; want an upgrade to revision 2", out, err)
	}

	rel, err := cfg.Releases.Last("cilium")
	if err != nil {
		t.Fatal(err)
	}

	if dbg, _ := rel.Config["debug"].(map[string]any); dbg["enabled"] != true {
		t.Errorf("the stored values are %v, want the TalosCluster's debug.enabled=true", rel.Config)
	}
}

// The controller names the number of Nodes the cluster has (--nodes): an HA cluster that has only its
// first node runs one operator replica, whatever spec.controlPlane.count says.
func TestCNIHelmApplyOperatorFollowsTheNodes(t *testing.T) {
	cluster := ciliumTestCluster("")
	cluster.Spec.ControlPlane.Count = 3
	cfg := memoryHelmConfig()
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster).Build()

	err := runCNIHelmApply(t.Context(), k8s, cfg,
		types.NamespacedName{Namespace: ichorControllerNamespace, Name: cniHelmTestCluster}, "", 0, false, 1, nil, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	rel, err := cfg.Releases.Last("cilium")
	if err != nil {
		t.Fatal(err)
	}

	if op, _ := rel.Config["operator"].(map[string]any); fmt.Sprint(op["replicas"]) != "1" {
		t.Errorf("operator = %v, want one replica for one node", rel.Config["operator"])
	}
}

// A user value for an Ichor-owned key stops the run before Helm is touched.
func TestCNIHelmApplyRefusesOwnedKeys(t *testing.T) {
	cfg := memoryHelmConfig()
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(ciliumTestCluster(`{"ipam":{"mode":"cluster-pool"}}`)).Build()

	err := runCNIHelmApply(t.Context(), k8s, cfg, types.NamespacedName{Namespace: ichorControllerNamespace, Name: "home"}, "", 0, false, 0, nil, nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "ipam.mode") {
		t.Fatalf("error = %v, want one naming ipam.mode", err)
	}

	if _, err := cfg.Releases.Last("cilium"); err == nil {
		t.Error("a release was recorded for refused values")
	}
}

// With --takeover the foreign record is backed up and removed after the install, and nothing
// of its content reaches the output.
func TestCNIHelmApplyTakeoverRetiresTheForeignRecord(t *testing.T) {
	kube := kubefakeclient.NewSimpleClientset()

	foreign := &release.Release{
		Name: cniCilium, Namespace: ciliumNamespace, Version: 3,
		Chart:  &chart.Chart{Metadata: &chart.Metadata{Name: cniCilium, Version: "1.0.0"}},
		Config: map[string]any{"marker": "sensitive-user-value"},
		Info:   &release.Info{Status: release.StatusDeployed},
	}
	if err := driver.NewSecrets(kube.CoreV1().Secrets(ciliumNamespace)).Create("sh.helm.release.v1.cilium.v3", foreign); err != nil {
		t.Fatal(err)
	}

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(ciliumTestCluster("")).Build()

	var out bytes.Buffer

	err := runCNIHelmApply(t.Context(), k8s, memoryHelmConfig(),
		types.NamespacedName{Namespace: ichorControllerNamespace, Name: cniHelmTestCluster}, "", 0, false, 0,
		&takeoverOptions{Kube: kube, IchorNamespace: ichorControllerNamespace}, nil, &out)
	if err != nil {
		t.Fatalf("runCNIHelmApply: %v", err)
	}

	if !strings.Contains(out.String(), "sh.helm.release.v1.cilium.v3") || strings.Contains(out.String(), "sensitive-user-value") {
		t.Errorf("output = %q, want the removed record's name and none of its content", out.String())
	}

	if _, err := kube.CoreV1().Secrets(ciliumNamespace).Get(t.Context(), "sh.helm.release.v1.cilium.v3", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("the foreign record is still there: %v", err)
	}

	if _, err := kube.CoreV1().Secrets(ichorControllerNamespace).Get(t.Context(), cilium.PriorReleaseBackupName, metav1.GetOptions{}); err != nil {
		t.Errorf("no backup Secret: %v", err)
	}
}

func TestCNIHelmIsHidden(t *testing.T) {
	if !cniHelmCmd.Hidden {
		t.Error("ichor cni helm must stay hidden: only the Job runs it")
	}
}

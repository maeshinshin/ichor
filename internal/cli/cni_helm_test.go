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
	"io"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chartutil"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
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

func ciliumTestCluster(values string) *ichorv1alpha1.TalosCluster {
	c := &ichorv1alpha1.TalosCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "ichor-system", Name: "home"}}
	c.Spec.CNI.Preset = "cilium"

	if values != "" {
		c.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{Values: &apiextensionsv1.JSON{Raw: []byte(values)}}
	}

	return c
}

// `ichor cni helm apply` adopts on the first run and upgrades on every later one, with the
// values read from the TalosCluster each time.
func TestCNIHelmApplyAdoptsThenUpgrades(t *testing.T) {
	key := types.NamespacedName{Namespace: "ichor-system", Name: "home"}
	cfg := memoryHelmConfig()

	run := func(values string) (string, error) {
		k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(ciliumTestCluster(values)).Build()

		var out bytes.Buffer

		err := runCNIHelmApply(t.Context(), k8s, cfg, key, 0, &out)

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

// A user value for an Ichor-owned key stops the run before Helm is touched.
func TestCNIHelmApplyRefusesOwnedKeys(t *testing.T) {
	cfg := memoryHelmConfig()
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(ciliumTestCluster(`{"ipam":{"mode":"cluster-pool"}}`)).Build()

	err := runCNIHelmApply(t.Context(), k8s, cfg, types.NamespacedName{Namespace: "ichor-system", Name: "home"}, 0, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "ipam.mode") {
		t.Fatalf("error = %v, want one naming ipam.mode", err)
	}

	if _, err := cfg.Releases.Last("cilium"); err == nil {
		t.Error("a release was recorded for refused values")
	}
}

func TestCNIHelmIsHidden(t *testing.T) {
	if !cniHelmCmd.Hidden {
		t.Error("ichor cni helm must stay hidden: only the Job runs it")
	}
}

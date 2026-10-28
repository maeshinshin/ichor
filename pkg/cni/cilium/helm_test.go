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
	"io"
	"testing"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chartutil"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"

	"github.com/maeshinshin/ichor/pkg/cni"
)

// fakeConfig is a Helm configuration over the memory driver and a client that
// touches no cluster: enough to see which action Apply picks and what it stores.
func fakeConfig() *action.Configuration {
	// The library default claims Kubernetes 1.20, below the chart's kubeVersion.
	caps := chartutil.DefaultCapabilities.Copy()
	caps.KubeVersion = chartutil.KubeVersion{Version: "v1.36.2", Major: "1", Minor: "36"}

	return &action.Configuration{
		Releases:       storage.Init(driver.NewMemory()),
		KubeClient:     &kubefake.PrintingKubeClient{Out: io.Discard},
		Capabilities:   caps,
		Log:            func(string, ...any) {},
		RegistryClient: nil,
	}
}

func effective(t *testing.T, user map[string]any) map[string]any {
	t.Helper()

	v, err := EffectiveValues(user, cni.KubeProxyEnabled, "")
	if err != nil {
		t.Fatal(err)
	}

	return v
}

// No release: Apply adopts (install, revision 1). A release: Apply upgrades
// (revision 2, the new values stored in full, revision 1 superseded).
func TestApply_AdoptsThenUpgrades(t *testing.T) {
	cfg := fakeConfig()

	first, err := Apply(t.Context(), cfg, effective(t, nil), ApplyOptions{})
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	if !first.Adopted || first.Release.Version != 1 || first.Release.Info.Status != release.StatusDeployed {
		t.Fatalf("first Apply: adopted=%t revision=%d status=%s, want adopted revision 1 deployed",
			first.Adopted, first.Release.Version, first.Release.Info.Status)
	}

	if first.Release.Name != ReleaseName || first.Release.Namespace != ReleaseNamespace {
		t.Errorf("release is %s/%s, want %s/%s", first.Release.Namespace, first.Release.Name, ReleaseNamespace, ReleaseName)
	}

	debugOn := map[string]any{keyDebug: map[string]any{keyEnabled: true}}

	second, err := Apply(t.Context(), cfg, effective(t, debugOn), ApplyOptions{})
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	if second.Adopted || second.Release.Version != 2 || second.Release.Info.Status != release.StatusDeployed {
		t.Fatalf("second Apply: adopted=%t revision=%d status=%s, want upgrade to revision 2 deployed",
			second.Adopted, second.Release.Version, second.Release.Info.Status)
	}

	if got, _ := second.Release.Config[keyDebug].(map[string]any); got[keyEnabled] != true {
		t.Errorf("revision 2 stores values %v, want debug.enabled=true", second.Release.Config[keyDebug])
	}

	old, err := cfg.Releases.Get(ReleaseName, 1)
	if err != nil {
		t.Fatal(err)
	}

	if old.Info.Status != release.StatusSuperseded {
		t.Errorf("revision 1 is %s after the upgrade, want superseded", old.Info.Status)
	}

	// A third run with the same values is still an upgrade, not a second adoption.
	third, err := Apply(t.Context(), cfg, effective(t, debugOn), ApplyOptions{})
	if err != nil || third.Adopted || third.Release.Version != 3 {
		t.Fatalf("third Apply: %+v, %v; want upgrade to revision 3", third, err)
	}
}

// A value the user removed goes back to the default: the previous revision's
// values are not inherited.
func TestApply_UpgradeDoesNotInheritRemovedValues(t *testing.T) {
	cfg := fakeConfig()
	debugOn := map[string]any{keyDebug: map[string]any{keyEnabled: true}}

	if _, err := Apply(t.Context(), cfg, effective(t, debugOn), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	res, err := Apply(t.Context(), cfg, effective(t, nil), ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := res.Release.Config[keyDebug].(map[string]any); got[keyEnabled] == true {
		t.Errorf("debug.enabled survived its removal: %v", res.Release.Config[keyDebug])
	}
}

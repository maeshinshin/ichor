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
	"strings"
	"testing"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chartutil"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	helmtime "helm.sh/helm/v3/pkg/time"

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

// setRevisionState rewrites a stored revision's status and age, as a killed Helm run leaves it.
func setRevisionState(t *testing.T, cfg *action.Configuration, rev int, st release.Status, age time.Duration) {
	t.Helper()

	rel, err := cfg.Releases.Get(ReleaseName, rev)
	if err != nil {
		t.Fatal(err)
	}

	rel.Info.Status = st
	rel.Info.LastDeployed = helmtime.Time{Time: time.Now().Add(-age)}

	if err := cfg.Releases.Update(rel); err != nil {
		t.Fatal(err)
	}
}

// A killed install leaves revision 1 pending-install. Once it is older than the wait timeout, or the
// caller is exclusive, Apply marks it failed and upgrades; it never uninstalls.
func TestApply_RepairsPendingInstall(t *testing.T) {
	for name, tc := range map[string]struct {
		age  time.Duration
		opts ApplyOptions
	}{
		"older than the timeout": {age: time.Hour, opts: ApplyOptions{Timeout: time.Minute}},
		"exclusive caller":       {age: 0, opts: ApplyOptions{Timeout: time.Hour, Exclusive: true}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := fakeConfig()
			if _, err := Apply(t.Context(), cfg, effective(t, nil), ApplyOptions{}); err != nil {
				t.Fatal(err)
			}

			setRevisionState(t, cfg, 1, release.StatusPendingInstall, tc.age)

			res, err := Apply(t.Context(), cfg, effective(t, nil), tc.opts)
			if err != nil {
				t.Fatalf("Apply over a pending-install record: %v", err)
			}

			if res.Release.Version != 2 || res.Release.Info.Status != release.StatusDeployed {
				t.Errorf("got revision %d %s, want revision 2 deployed", res.Release.Version, res.Release.Info.Status)
			}

			old, _ := cfg.Releases.Get(ReleaseName, 1)
			if old.Info.Status.IsPending() {
				t.Errorf("revision 1 is still %s", old.Info.Status)
			}
		})
	}
}

func TestApply_RepairsPendingUpgradeAndRollback(t *testing.T) {
	for _, st := range []release.Status{release.StatusPendingUpgrade, release.StatusPendingRollback} {
		t.Run(string(st), func(t *testing.T) {
			cfg := fakeConfig()
			debugOn := map[string]any{keyDebug: map[string]any{keyEnabled: true}}

			for _, v := range []map[string]any{nil, debugOn} {
				if _, err := Apply(t.Context(), cfg, effective(t, v), ApplyOptions{}); err != nil {
					t.Fatal(err)
				}
			}

			setRevisionState(t, cfg, 2, st, time.Hour)

			res, err := Apply(t.Context(), cfg, effective(t, debugOn), ApplyOptions{Timeout: time.Minute})
			if err != nil || res.Release.Version != 3 || res.Release.Info.Status != release.StatusDeployed {
				t.Fatalf("Apply = %+v, %v; want revision 3 deployed", res, err)
			}

			if bad, _ := cfg.Releases.Get(ReleaseName, 2); bad.Info.Status.IsPending() {
				t.Errorf("revision 2 is still %s", bad.Info.Status)
			}
		})
	}
}

// A recent pending record may belong to a run that is still going: Apply leaves it alone and Helm refuses.
func TestApply_LeavesAFreshPendingRecordAlone(t *testing.T) {
	cfg := fakeConfig()
	if _, err := Apply(t.Context(), cfg, effective(t, nil), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	setRevisionState(t, cfg, 1, release.StatusPendingInstall, 0)

	if _, err := Apply(t.Context(), cfg, effective(t, nil), ApplyOptions{Timeout: time.Hour}); err == nil ||
		!strings.Contains(err.Error(), "in progress") {
		t.Fatalf("Apply = %v, want Helm's operation-in-progress refusal", err)
	}

	if rel, _ := cfg.Releases.Get(ReleaseName, 1); rel.Info.Status != release.StatusPendingInstall {
		t.Errorf("revision 1 is %s, want it untouched", rel.Info.Status)
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

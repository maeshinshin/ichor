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

package release

import (
	"strings"
	"testing"

	"golang.org/x/mod/semver"

	"github.com/siderolabs/talos/pkg/machinery/constants"

	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// Versions and case names the table tests repeat.
const (
	talos1132     = "v1.13.2"
	k8s1371       = "v1.37.1"
	talos1124     = "v1.12.4"
	talos1143     = "v1.14.3"
	talos1144     = "v1.14.4"
	talos1500     = "v1.15.0"
	contract113   = "v1.13"
	contract114   = "v1.14"
	k8s1350       = "v1.35.0"
	k8s1362       = "v1.36.2"
	k8s1380       = "v1.38.0"
	caseDowngrade = "downgrade"
)

// The pinned Kubernetes is the one pkg/machinery defaults to, and it is one the pinned Talos supports.
func TestPinnedIsConsistentWithMachinery(t *testing.T) {
	t.Parallel()

	if semver.Compare(Pinned.Kubernetes, "v"+constants.DefaultKubernetesVersion) > 0 {
		t.Errorf("Pinned.Kubernetes = %s is newer than machinery's default %s",
			Pinned.Kubernetes, constants.DefaultKubernetesVersion)
	}

	if Pinned.Kubernetes != cilium.RenderKubernetesVersion {
		t.Errorf("Pinned.Kubernetes = %s, the Cilium bootstrap render targets %s; they must agree",
			Pinned.Kubernetes, cilium.RenderKubernetesVersion)
	}

	if err := upgrade.KubernetesPairing(Pinned.Kubernetes, Pinned.Talos); err != nil {
		t.Errorf("Pinned Kubernetes %s is not supported with Talos %s: %v", Pinned.Kubernetes, Pinned.Talos, err)
	}

	if Pinned.Cilium == "" {
		t.Error("Pinned.Cilium is empty")
	}

	// A first install is refused on a pinned set the table does not hold (ADR 0115).
	if err := TalosKubernetesError(Pinned.Talos, Pinned.Kubernetes); err != nil {
		t.Errorf("pinned Talos and Kubernetes are incompatible: %v", err)
	}

	if err := CiliumKubernetesError(Pinned.Cilium, Pinned.Kubernetes); err != nil {
		t.Errorf("pinned Cilium and Kubernetes are incompatible: %v", err)
	}
}

// A Kubernetes step names the order when the installed Cilium is too old: upgrade Cilium to the
// embedded chart first, or, when this Ichor does not ship one that runs it, a newer Ichor (ADR 0115).
func TestCheckKubernetesStepNamesTheOrder(t *testing.T) {
	t.Parallel()

	if err := CheckKubernetesStep(Pinned.Talos, Pinned.Kubernetes, ""); err != nil {
		t.Errorf("an unobserved Cilium is not checked: %v", err)
	}

	err := CheckKubernetesStep(Pinned.Talos, "v1.32.0", Pinned.Cilium)
	if err == nil {
		t.Fatal("Kubernetes the Talos does not run was accepted")
	}

	// The installed Cilium 1.0.0 runs no Kubernetes in the table, so the message names the embedded
	// chart when it runs the target, and a newer Ichor when it does not.
	err = CheckKubernetesStep(Pinned.Talos, k8s1362, "1.0.0")
	if err == nil {
		t.Fatal("an installed Cilium the table does not run was accepted")
	}

	want := "a newer Ichor release is needed"
	if CiliumKubernetesError(Pinned.Cilium, k8s1362) == nil {
		want = "upgrade Cilium to " + Pinned.Cilium + " first"
	}

	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want it to say %q", err, want)
	}
}

func TestCompare(t *testing.T) {
	t.Parallel()

	cases := []struct {
		current, pinned string
		want            State
	}{
		{talos1124, Pinned.Talos, Behind},
		{Pinned.Talos, Pinned.Talos, UpToDate},
		{talos1500, Pinned.Talos, Ahead},
		{contract113, Pinned.VersionContract, Behind},
		{Pinned.VersionContract, Pinned.VersionContract, UpToDate},
		{"", Pinned.Talos, Unknown},
		{"1.14.3", Pinned.Talos, Unknown},
	}

	for _, c := range cases {
		if got := Compare(c.current, c.pinned); got != c.want {
			t.Errorf("Compare(%q, %q) = %s, want %s", c.current, c.pinned, got, c.want)
		}
	}
}

// CheckUpdate refuses only a downgrade: any other value, older or newer than the pinned one, is
// allowed (ADR 0115).
func TestCheckUpdate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, old, new string
		ok             bool
	}{
		{"unchanged older", talos1124, talos1124, true},
		{"upgrade to the pinned", talos1124, Pinned.Talos, true},
		{"upgrade past the pinned", Pinned.Talos, talos1500, true},
		{caseDowngrade, Pinned.Talos, talos1132, false},
		{"downgrade by a patch", Pinned.Talos, "v1.14.2", false},
		{"unchanged pinned", Pinned.Talos, Pinned.Talos, true},
		{"contract move", contract113, contract114, true},
		{"contract downgrade", contract114, contract113, false},
	}

	for _, c := range cases {
		err := CheckUpdate("talosVersion", c.old, c.new)
		if (err == nil) != c.ok {
			t.Errorf("%s: CheckUpdate(%s -> %s) = %v, want ok=%v", c.name, c.old, c.new, err, c.ok)
		}
	}
}

// A Talos target is a final version newer than the running one that upgrade.TalosPath accepts
// (ADR 0115).
func TestCheckTalosTarget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, running, target string
		ok                    bool
	}{
		{"adjacent minor", talos1132, talos1143, true},
		{"skips a minor within the bounds", talos1124, talos1143, true},
		{"patch", talos1143, talos1144, true},
		{"below the minimum host version", "v1.11.5", talos1143, false},
		{"not newer", talos1143, talos1143, false},
		{caseDowngrade, talos1143, "v1.13.9", false},
		{"pre-release", talos1143, "v1.14.4-rc.1", false},
		{"build metadata", talos1143, "v1.14.4+build", false},
		{"shorthand", talos1143, contract114, false},
		{"minor machinery does not know", talos1143, talos1500, false},
	}

	for _, c := range cases {
		err := CheckTalosTarget(c.running, c.target)
		if (err == nil) != c.ok {
			t.Errorf("%s: CheckTalosTarget(%s -> %s) = %v, want ok=%v", c.name, c.running, c.target, err, c.ok)
		}
	}
}

// A Kubernetes target is a final version newer than the running one, at most one minor up within
// the major (upgrade.KubernetesSkew), and supported by the cluster's Talos (upgrade.KubernetesPairing)
// (ADR 0115).
func TestCheckKubernetesTarget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, running, target, talos string
		ok                           bool
	}{
		{"one minor up", k8s1362, k8s1371, Pinned.Talos, true},
		{"patch", k8s1371, "v1.37.2", Pinned.Talos, true},
		{"two minors up", k8s1350, k8s1371, Pinned.Talos, false},
		{"not supported by the Talos", k8s1371, k8s1380, Pinned.Talos, false},
		{"not newer", k8s1371, k8s1371, Pinned.Talos, false},
		{caseDowngrade, k8s1371, k8s1362, Pinned.Talos, false},
		{"pre-release", k8s1362, "v1.37.1-rc.1", Pinned.Talos, false},
	}

	for _, c := range cases {
		err := CheckKubernetesTarget(c.running, c.target, c.talos)
		if (err == nil) != c.ok {
			t.Errorf("%s: CheckKubernetesTarget(%s -> %s, talos %s) = %v, want ok=%v",
				c.name, c.running, c.target, c.talos, err, c.ok)
		}
	}
}

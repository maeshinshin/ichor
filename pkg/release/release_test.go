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
	"slices"
	"strings"
	"testing"

	"golang.org/x/mod/semver"

	"github.com/siderolabs/talos/pkg/machinery/constants"

	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// Versions older than the pinned set, which the tests start from.
const (
	olderTalos    = "v1.12.4"
	olderContract = "v1.13"
	olderK8s      = "v1.36.0"
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

	// A first install is refused on a pinned set the table does not hold.
	if err := CheckTalosStep(Pinned.Talos, Pinned.Kubernetes); err != nil {
		t.Errorf("pinned Talos and Kubernetes are incompatible: %v", err)
	}

	if err := CiliumKubernetesError(Pinned.Cilium, Pinned.Kubernetes); err != nil {
		t.Errorf("pinned Cilium and Kubernetes are incompatible: %v", err)
	}
}

// A Kubernetes step names the order when the installed Cilium is too old: upgrade Cilium to the
// embedded chart first, or, when this Ichor does not ship one that runs it, a newer Ichor.
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
	err = CheckKubernetesStep(Pinned.Talos, "v1.36.2", "1.0.0")
	if err == nil {
		t.Fatal("an installed Cilium the table does not run was accepted")
	}

	want := "a newer Ichor release is needed"
	if CiliumKubernetesError(Pinned.Cilium, "v1.36.2") == nil {
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
		{"v1.12.4", Pinned.Talos, Behind},
		{Pinned.Talos, Pinned.Talos, UpToDate},
		{"v1.15.0", Pinned.Talos, Ahead},
		{"v1.13", Pinned.VersionContract, Behind},
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
// allowed.
func TestCheckUpdate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, old, new string
		ok             bool
	}{
		{"unchanged older", "v1.12.4", "v1.12.4", true},
		{"upgrade to the pinned", "v1.12.4", Pinned.Talos, true},
		{"upgrade past the pinned", Pinned.Talos, "v1.15.0", true},
		{"downgrade", Pinned.Talos, "v1.13.2", false},
		{"downgrade by a patch", Pinned.Talos, "v1.14.2", false},
		{"unchanged pinned", Pinned.Talos, Pinned.Talos, true},
		{"contract move", "v1.13", "v1.14", true},
		{"contract downgrade", "v1.14", "v1.13", false},
	}

	for _, c := range cases {
		err := CheckUpdate("talosVersion", c.old, c.new)
		if (err == nil) != c.ok {
			t.Errorf("%s: CheckUpdate(%s -> %s) = %v, want ok=%v", c.name, c.old, c.new, err, c.ok)
		}
	}
}

// A Talos target is a final version newer than the running one that upgrade.TalosPath accepts.
func TestCheckTalosTarget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, running, target string
		ok                    bool
	}{
		{"adjacent minor", "v1.13.2", "v1.14.3", true},
		{"skips a minor within the bounds", "v1.12.4", "v1.14.3", true},
		{"patch", "v1.14.3", "v1.14.4", true},
		{"below the minimum host version", "v1.11.5", "v1.14.3", false},
		{"not newer", "v1.14.3", "v1.14.3", false},
		{"downgrade", "v1.14.3", "v1.13.9", false},
		{"pre-release", "v1.14.3", "v1.14.4-rc.1", false},
		{"build metadata", "v1.14.3", "v1.14.4+build", false},
		{"shorthand", "v1.14.3", "v1.14", false},
		{"minor machinery does not know", "v1.14.3", "v1.15.0", false},
	}

	for _, c := range cases {
		err := CheckTalosTarget(c.running, c.target)
		if (err == nil) != c.ok {
			t.Errorf("%s: CheckTalosTarget(%s -> %s) = %v, want ok=%v", c.name, c.running, c.target, err, c.ok)
		}
	}
}

// A Kubernetes target is a final version newer than the running one, at most one minor up within
// the major (upgrade.KubernetesSkew), and supported by the cluster's Talos (upgrade.KubernetesPairing).
func TestCheckKubernetesTarget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, running, target, talos string
		ok                           bool
	}{
		{"one minor up", "v1.36.2", "v1.37.1", Pinned.Talos, true},
		{"patch", "v1.37.1", "v1.37.2", Pinned.Talos, true},
		{"two minors up", "v1.35.0", "v1.37.1", Pinned.Talos, false},
		{"not supported by the Talos", "v1.37.1", "v1.38.0", Pinned.Talos, false},
		{"not newer", "v1.37.1", "v1.37.1", Pinned.Talos, false},
		{"downgrade", "v1.37.1", "v1.36.2", Pinned.Talos, false},
		{"pre-release", "v1.36.2", "v1.37.1-rc.1", Pinned.Talos, false},
	}

	for _, c := range cases {
		err := CheckKubernetesTarget(c.running, c.target, c.talos)
		if (err == nil) != c.ok {
			t.Errorf("%s: CheckKubernetesTarget(%s -> %s, talos %s) = %v, want ok=%v",
				c.name, c.running, c.target, c.talos, err, c.ok)
		}
	}
}

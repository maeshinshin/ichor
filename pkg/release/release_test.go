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

	"github.com/siderolabs/talos/pkg/machinery/constants"

	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// The pinned Kubernetes is the one pkg/machinery defaults to, and it is one the pinned Talos supports.
func TestPinnedIsConsistentWithMachinery(t *testing.T) {
	t.Parallel()

	if got := strings.TrimPrefix(Pinned.Kubernetes, "v"); got != constants.DefaultKubernetesVersion {
		t.Errorf("Pinned.Kubernetes = %s, machinery's default is %s", Pinned.Kubernetes, constants.DefaultKubernetesVersion)
	}

	if err := upgrade.KubernetesPairing(Pinned.Kubernetes, Pinned.Talos); err != nil {
		t.Errorf("Pinned Kubernetes %s is not supported with Talos %s: %v", Pinned.Kubernetes, Pinned.Talos, err)
	}

	if Pinned.Cilium == "" {
		t.Error("Pinned.Cilium is empty")
	}
}

func TestCompare(t *testing.T) {
	t.Parallel()

	cases := []struct {
		current, pinned string
		want            State
	}{
		{"v1.12.4", "v1.14.3", Behind},
		{"v1.14.3", "v1.14.3", UpToDate},
		{"v1.15.0", "v1.14.3", Ahead},
		{"v1.13", "v1.14", Behind},
		{"v1.14", "v1.14", UpToDate},
		{"", "v1.14.3", Unknown},
		{"1.14.3", "v1.14.3", Unknown},
	}

	for _, c := range cases {
		if got := Compare(c.current, c.pinned); got != c.want {
			t.Errorf("Compare(%q, %q) = %s, want %s", c.current, c.pinned, got, c.want)
		}
	}
}

func TestCheckCreate(t *testing.T) {
	t.Parallel()

	if err := CheckCreate("talosVersion", "v1.12.4", "v1.14.3"); err != nil {
		t.Errorf("older version on create refused: %v", err)
	}

	if err := CheckCreate("talosVersion", "v1.15.0", "v1.14.3"); err == nil {
		t.Error("newer version on create accepted")
	}

	if err := CheckCreate("versionContract", "v1.14", "v1.14"); err != nil {
		t.Errorf("pinned contract refused: %v", err)
	}

	if err := CheckCreate("versionContract", "1.14", "v1.14"); err == nil {
		t.Error("contract without a v accepted")
	}
}

func TestCheckUpdate(t *testing.T) {
	t.Parallel()

	const pinned = "v1.14.3"

	cases := []struct {
		name, old, new string
		ok             bool
	}{
		{"unchanged behind", "v1.12.4", "v1.12.4", true},
		{"moved to pinned", "v1.12.4", pinned, true},
		{"moved to other version", "v1.12.4", "v1.13.0", false},
		{"downgrade to older pinned is impossible", pinned, "v1.12.4", false},
		{"unchanged pinned", pinned, pinned, true},
	}

	for _, c := range cases {
		err := CheckUpdate("talosVersion", c.old, c.new, pinned)
		if (err == nil) != c.ok {
			t.Errorf("%s: CheckUpdate(%s -> %s) = %v, want ok=%v", c.name, c.old, c.new, err, c.ok)
		}
	}
}

func TestCheckTarget(t *testing.T) {
	t.Parallel()

	if err := CheckTarget("Talos", "v1.14.3", "v1.14.3"); err != nil {
		t.Errorf("pinned target refused: %v", err)
	}

	if err := CheckTarget("Talos", "v1.13.0", "v1.14.3"); err == nil {
		t.Error("other target accepted")
	}
}

func TestAdoptable(t *testing.T) {
	t.Parallel()

	behind, err := Adoptable("v1.12.4", "v1.33.0")
	if err != nil {
		t.Fatalf("older cluster refused: %v", err)
	}

	if len(behind) != 2 {
		t.Errorf("behind = %v, want Talos and Kubernetes", behind)
	}

	if _, err := Adoptable("v1.15.0", "v1.37.1"); err == nil {
		t.Error("cluster newer than the set adopted")
	}

	if _, err := Adoptable("v1.14.3", "v1.38.0"); err == nil {
		t.Error("Kubernetes newer than the set adopted")
	}

	if behind, err := Adoptable(Pinned.Talos, Pinned.Kubernetes); err != nil || len(behind) != 0 {
		t.Errorf("pinned cluster: behind=%v err=%v", behind, err)
	}
}

func TestSteps(t *testing.T) {
	t.Parallel()

	pinned := Running{Talos: Pinned.Talos, VersionContract: Pinned.VersionContract, Kubernetes: Pinned.Kubernetes}

	if got := Steps(pinned); len(got) != 0 {
		t.Errorf("up-to-date cluster has steps %v", got)
	}

	all := Running{Talos: "v1.13.0", VersionContract: "v1.13", Kubernetes: "v1.36.0", CiliumBehind: true, ConfigPending: true}

	want := []Step{StepTalos, StepKubernetes, StepCilium}
	if got := Steps(all); !slices.Equal(got, want) {
		t.Errorf("Steps(behind everything) = %v, want %v", got, want)
	}

	// Config is applied only once Talos is at the pinned set, and only to unblock Kubernetes.
	k8sOnly := Running{Talos: Pinned.Talos, VersionContract: Pinned.VersionContract, Kubernetes: "v1.36.0", ConfigPending: true}

	want = []Step{StepConfig, StepKubernetes}
	if got := Steps(k8sOnly); !slices.Equal(got, want) {
		t.Errorf("Steps(k8s behind, config pending) = %v, want %v", got, want)
	}

	// A contract bump on its own is a Talos step, not a config step.
	contractOnly := Running{Talos: Pinned.Talos, VersionContract: "v1.13", Kubernetes: Pinned.Kubernetes}
	if got := Steps(contractOnly); !slices.Equal(got, []Step{StepTalos}) {
		t.Errorf("Steps(contract behind) = %v, want [talos]", got)
	}
}

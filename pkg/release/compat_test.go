//nolint:goconst // version tables repeat the same release names on purpose
package release

import (
	"errors"
	"testing"
)

func TestUnknownIsNotIncompatible(t *testing.T) {
	// Talos 1.15 is unknown; Talos 1.14 with a Kubernetes newer than its table is incompatible.
	if err := TalosKubernetesError("v1.15.0", "v1.37.1"); !errors.Is(err, ErrUnknownVersion) {
		t.Errorf("Talos 1.15 = %v, want ErrUnknownVersion", err)
	}

	if err := TalosKubernetesError("v1.14.3", "v1.38.0"); err == nil || errors.Is(err, ErrUnknownVersion) {
		t.Errorf("Talos 1.14 with 1.38.0 = %v, want an incompatibility that is not ErrUnknownVersion", err)
	}

	if err := CiliumKubernetesError("1.21.0", "v1.33.0"); !errors.Is(err, ErrUnknownVersion) {
		t.Errorf("Cilium 1.21 = %v, want ErrUnknownVersion", err)
	}

	if err := CiliumKubernetesError("1.18.0", "v1.34.0"); err == nil || errors.Is(err, ErrUnknownVersion) {
		t.Errorf("Cilium 1.18 with 1.34 = %v, want an incompatibility that is not ErrUnknownVersion", err)
	}

	if !machineryKnows("v1.14.3") || machineryKnows("v1.15.0") {
		t.Error("machineryKnows does not match machinery's table: the 'too old' probe changed")
	}
}

func TestCiliumKubernetesTableLookups(t *testing.T) {
	cases := []struct {
		name, cilium, kubernetes string
		ok                       bool
	}{
		{"1.18 lower bound", "1.18.0", "v1.30.0", true},
		{"1.18 upper bound", "1.18.4", "v1.33.9", true},
		{"1.18 one minor above", "1.18.0", "v1.34.0", false},
		{"1.19 range", "v1.19.2", "v1.34.1", true},
		{"1.19 below range", "1.19.0", "v1.30.5", false},
		{"1.20 range", "1.20.0", "v1.36.0", true},
		{"1.20 above range", "1.20.0", "v1.37.1", false},
		{"unknown Cilium minor", "1.21.0", "v1.33.0", false},
		{"unknown Cilium minor, not semver", "latest", "v1.33.0", false},
		{"Kubernetes not semver", "1.18.0", "1.33", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CiliumKubernetesError(tc.cilium, tc.kubernetes)
			if (err == nil) != tc.ok {
				t.Fatalf("CiliumKubernetesError(%q, %q) = %v, want ok=%v", tc.cilium, tc.kubernetes, err, tc.ok)
			}
		})
	}
}

func TestTalosKubernetesUsesMachineryAndRefusesUnknownMinors(t *testing.T) {
	cases := []struct {
		name, talos, kubernetes string
		ok                      bool
	}{
		// Machinery v1.14.3 covers Talos 1.14 with 1.32.0 to 1.37.99.
		{"Talos 1.14 with 1.37.1", "v1.14.3", "v1.37.1", true},
		{"Talos 1.14 with 1.32.0", "v1.14.3", "v1.32.0", true},
		{"Talos 1.14 with 1.31.9", "v1.14.3", "v1.31.9", false},
		{"Talos 1.14 with 1.38.0", "v1.14.3", "v1.38.0", false},
		{"Talos 1.12 with 1.35.0", "v1.12.0", "v1.35.0", true},
		{"Talos 1.12 with 1.36.0", "v1.12.0", "v1.36.0", false},
		// Talos 1.15 is newer than machinery, and the typed table has no entry for it.
		{"Talos 1.15 is not known", "v1.15.0", "v1.37.1", false},
		{"Talos not a version", "latest", "v1.37.1", false},
		{"Kubernetes not semver", "v1.14.3", "latest", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := TalosKubernetesError(tc.talos, tc.kubernetes)
			if (err == nil) != tc.ok {
				t.Fatalf("TalosKubernetesError(%q, %q) = %v, want ok=%v", tc.talos, tc.kubernetes, err, tc.ok)
			}
		})
	}
}

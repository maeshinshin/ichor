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

package upgrade

import "testing"

// Versions reused across several test cases below.
const (
	talos1143      = "v1.14.3"
	kubernetes1340 = "v1.34.0"
	kubernetes1330 = "v1.33.0"
	kubernetes1334 = "v1.33.4"
	notAVersion    = "not-a-version"
)

// TestTalosPath exercises pkg/machinery/compatibility's own rule through
// this package's wrapper: a same-minor patch bump, a supported multi-minor
// jump within the documented floor (1.12 -> 1.14 skips 1.13, both within
// machinery's talos114.MinimumHostUpgradeVersion), a host too old to
// upgrade, and a downgrade.
func TestTalosPath(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		{name: "patch upgrade within the same minor", from: "v1.14.0", to: talos1143, wantErr: false},
		{name: "supported multi-minor jump", from: "v1.12.0", to: talos1143, wantErr: false},
		{name: "host too old for the target minor", from: "v1.0.0", to: talos1143, wantErr: true},
		{name: "downgrade", from: talos1143, to: "v1.12.0", wantErr: true},
		{name: "invalid current version", from: notAVersion, to: talos1143, wantErr: true},
		{name: "invalid target version", from: talos1143, to: notAVersion, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := TalosPath(tc.from, tc.to)
			if (err != nil) != tc.wantErr {
				t.Fatalf("TalosPath(%q, %q) = %v, want error = %v", tc.from, tc.to, err, tc.wantErr)
			}
		})
	}
}

// TestKubernetesPairing exercises pkg/machinery/compatibility's
// KubernetesVersion.SupportedWith: in range, too old, too new.
func TestKubernetesPairing(t *testing.T) {
	cases := []struct {
		name       string
		kubernetes string
		talos      string
		wantErr    bool
	}{
		{name: "in range for Talos 1.14", kubernetes: kubernetes1340, talos: talos1143, wantErr: false},
		{name: "too old for Talos 1.14", kubernetes: "v1.20.0", talos: talos1143, wantErr: true},
		{name: "too new for Talos 1.14", kubernetes: "v1.99.0", talos: talos1143, wantErr: true},
		{name: "invalid Kubernetes version", kubernetes: notAVersion, talos: talos1143, wantErr: true},
		{name: "invalid Talos version", kubernetes: kubernetes1340, talos: notAVersion, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := KubernetesPairing(tc.kubernetes, tc.talos)
			if (err != nil) != tc.wantErr {
				t.Fatalf("KubernetesPairing(%q, %q) = %v, want error = %v", tc.kubernetes, tc.talos, err, tc.wantErr)
			}
		})
	}
}

// TestKubernetesSkew covers the boundary explicitly: same version (no-op),
// patch-only bump, exactly one minor forward (allowed), two minors forward
// (skips a minor, rejected), a major version change, and a downgrade (both
// across a minor and within one).
func TestKubernetesSkew(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		{name: "identical version", from: kubernetes1330, to: kubernetes1330, wantErr: false},
		{name: "patch-only bump", from: kubernetes1330, to: kubernetes1334, wantErr: false},
		{name: "exactly one minor forward", from: kubernetes1334, to: kubernetes1340, wantErr: false},
		{name: "two minors forward skips one", from: kubernetes1330, to: "v1.35.0", wantErr: true},
		{name: "major version change", from: kubernetes1330, to: "v2.0.0", wantErr: true},
		{name: "downgrade across a minor", from: kubernetes1340, to: kubernetes1330, wantErr: true},
		{name: "downgrade within a minor", from: kubernetes1334, to: kubernetes1330, wantErr: true},
		{name: "invalid current version", from: notAVersion, to: kubernetes1330, wantErr: true},
		{name: "invalid target version", from: kubernetes1330, to: notAVersion, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := KubernetesSkew(tc.from, tc.to)
			if (err != nil) != tc.wantErr {
				t.Fatalf("KubernetesSkew(%q, %q) = %v, want error = %v", tc.from, tc.to, err, tc.wantErr)
			}
		})
	}
}

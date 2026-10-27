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

package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

func TestRegistryAuthSecretNames(t *testing.T) {
	cluster := &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{
		Registries: []ichorv1alpha1.TalosClusterRegistry{
			{Host: "a"},
			{Host: "b", AuthSecretRef: &corev1.LocalObjectReference{Name: "s1"}},
			{Host: "c", AuthSecretRef: &corev1.LocalObjectReference{Name: "s2"}},
		},
	}}

	got := registryAuthSecretNames(cluster)
	if len(got) != 2 || got[0] != "s1" || got[1] != "s2" {
		t.Fatalf("registryAuthSecretNames = %v, want [s1 s2]", got)
	}

	if registryAuthSecretNames(&corev1.Secret{}) != nil {
		t.Fatal("a non-TalosCluster object must index to nothing")
	}
}

func TestBundleSecretNames(t *testing.T) {
	cluster := &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{
		SecretsRef: corev1.LocalObjectReference{Name: "bundle-secret"},
	}}

	if got := bundleSecretNames(cluster); len(got) != 1 || got[0] != "bundle-secret" {
		t.Fatalf("bundleSecretNames = %v, want [bundle-secret]", got)
	}

	if bundleSecretNames(&corev1.Secret{}) != nil {
		t.Fatal("a non-TalosCluster object must index to nothing")
	}
}

func TestRegistryAuthFingerprint(t *testing.T) {
	if got := registryAuthFingerprint(nil); got != "" {
		t.Fatalf("no auth must fingerprint to empty (so existing hashes do not move), got %q", got)
	}

	a := map[string]render.RegistryAuth{"h": {Password: "old-secret-value"}}
	b := map[string]render.RegistryAuth{"h": {Password: "new-secret-value"}}

	fa, fb := registryAuthFingerprint(a), registryAuthFingerprint(b)
	if fa == "" || fa == fb {
		t.Fatalf("a changed password must change the fingerprint: %q vs %q", fa, fb)
	}

	if fa != registryAuthFingerprint(map[string]render.RegistryAuth{"h": {Password: "old-secret-value"}}) {
		t.Fatal("the fingerprint must be deterministic")
	}

	if strings.Contains(fa, "secret-value") {
		t.Fatal("the fingerprint must not carry the data")
	}

	// A value moving between fields must not collide.
	if registryAuthFingerprint(map[string]render.RegistryAuth{"h": {Auth: "x"}}) ==
		registryAuthFingerprint(map[string]render.RegistryAuth{"h": {Password: "x"}}) {
		t.Fatal("password and auth must fingerprint differently")
	}
}

func TestDesiredInputsHashFollowsRegistryAuth(t *testing.T) {
	cluster := &ichorv1alpha1.TalosCluster{}
	m := &ichorv1alpha1.TalosMachine{}

	base, err := desiredInputsHash(cluster, m, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	withAuth, err := desiredInputsHash(cluster, m, nil, nil, "sha256:abc")
	if err != nil {
		t.Fatal(err)
	}

	if base == withAuth {
		t.Fatal("the registry auth fingerprint must feed the desired-inputs hash")
	}
}

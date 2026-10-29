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

package v1alpha1

import (
	"context"
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// A TalosCluster whose talosVersion or kubernetesVersion is not semver is refused on create
// and, when the update changes it, on update: the CRD pattern alone lets "v01.2.3" through.
func TestTalosClusterRefusesANonSemverVersion(t *testing.T) {
	t.Parallel()

	v := &TalosClusterCustomValidator{}
	ctx := context.Background()

	bad := newTestCluster()
	bad.Spec.TalosVersion = "v01.14.3"

	if _, err := v.ValidateCreate(ctx, bad); err == nil || !strings.Contains(err.Error(), "spec.talosVersion: v01.14.3 is not a valid version (semver)") {
		t.Fatalf("create with talosVersion v01.14.3: err = %v", err)
	}

	bad = newTestCluster()
	bad.Spec.KubernetesVersion = "v1.36.0-02"

	if _, err := v.ValidateCreate(ctx, bad); err == nil || !strings.Contains(err.Error(), "spec.kubernetesVersion: v1.36.0-02 is not a valid version (semver)") {
		t.Fatalf("create with kubernetesVersion v1.36.0-02: err = %v", err)
	}

	if _, err := v.ValidateCreate(ctx, newTestCluster()); err != nil {
		t.Fatalf("create with valid versions: %v", err)
	}

	old := newTestCluster()
	edited := newTestCluster()
	edited.Spec.TalosVersion = "v01.14.3"

	if _, err := v.ValidateUpdate(ctx, old, edited); err == nil {
		t.Fatal("update that changes talosVersion to v01.14.3 was accepted")
	}

	// A stored object whose version predates the check is still editable for something else.
	stored := newTestCluster()
	stored.Spec.TalosVersion = "v01.14.3"

	if _, err := v.ValidateUpdate(ctx, stored, stored.DeepCopy()); err != nil {
		t.Fatalf("update that leaves the version alone: %v", err)
	}
}

// An IchorInstallation whose spec.version is not semver is refused on create and on an update that
// changes it. This is the kubectl path to the bug where a leading-zero pre-release was accepted.
func TestIchorInstallationRefusesANonSemverVersion(t *testing.T) {
	t.Parallel()

	v := &IchorInstallationCustomValidator{}
	ctx := context.Background()

	bad := &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{Version: "v0.0.1814000000-02357186"}}

	_, err := v.ValidateCreate(ctx, bad)
	if err == nil || err.Error() != "v0.0.1814000000-02357186 is not a valid version (semver): numeric pre-release identifier \"02357186\" has a leading zero" {
		t.Fatalf("create: err = %v", err)
	}

	good := &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{Version: "v0.4.0"}}
	if _, err := v.ValidateCreate(ctx, good); err != nil {
		t.Fatalf("create with v0.4.0: %v", err)
	}

	if _, err := v.ValidateUpdate(ctx, good, bad); err == nil {
		t.Fatal("update to the leading-zero pre-release was accepted")
	}

	// Unchanged: the stored value is not re-judged.
	if _, err := v.ValidateUpdate(ctx, bad, bad.DeepCopy()); err != nil {
		t.Fatalf("update that leaves the version alone: %v", err)
	}
}

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

const leadingZeroTalos = "v01.14.3"

// A TalosCluster whose talosVersion or kubernetesVersion is not semver is refused on create
// and, when the update changes it, on update: the CRD pattern alone lets "v01.2.3" through.
func TestTalosClusterRefusesANonSemverVersion(t *testing.T) {
	t.Parallel()

	v := &TalosClusterCustomValidator{}
	ctx := context.Background()

	bad := newTestCluster()
	bad.Spec.TalosVersion = leadingZeroTalos

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
	edited.Spec.TalosVersion = leadingZeroTalos

	if _, err := v.ValidateUpdate(ctx, old, edited); err == nil {
		t.Fatal("update that changes talosVersion to v01.14.3 was accepted")
	}

	// A stored object whose version predates the check is still editable for something else.
	stored := newTestCluster()
	stored.Spec.TalosVersion = leadingZeroTalos

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

// A TalosCluster may be created on any compatible semantic version, the release set's or not. A changed version may
// move up but not down.
func TestTalosClusterMovesUpButNotDown(t *testing.T) {
	t.Parallel()

	v := &TalosClusterCustomValidator{}
	ctx := context.Background()

	newer := newTestCluster()
	newer.Spec.TalosVersion, newer.Spec.KubernetesVersion, newer.Spec.VersionContract = "v1.14.4", "v1.37.2", testVersionContract

	if _, err := v.ValidateCreate(ctx, newer); err != nil {
		t.Fatalf("create on a newer patch: %v", err)
	}

	older := newTestCluster()
	older.Spec.TalosVersion, older.Spec.KubernetesVersion, older.Spec.VersionContract = testTalosVersion, testK8sVersion, testVersionContract

	if _, err := v.ValidateCreate(ctx, older); err != nil {
		t.Fatalf("create on an older patch: %v", err)
	}

	badContract := newTestCluster()
	badContract.Spec.VersionContract = "1.14"

	if _, err := v.ValidateCreate(ctx, badContract); err == nil {
		t.Fatal("create with a contract without a v accepted")
	}

	if _, err := v.ValidateUpdate(ctx, older, newer); err != nil {
		t.Fatalf("update that moves up a patch: %v", err)
	}

	if _, err := v.ValidateUpdate(ctx, newer, older); err == nil || !strings.Contains(err.Error(), "cannot downgrade") {
		t.Fatalf("update that moves Talos down: err = %v", err)
	}

	contractDown := newer.DeepCopy()
	contractDown.Spec.VersionContract = "v1.13"

	if _, err := v.ValidateUpdate(ctx, newer, contractDown); err == nil || !strings.Contains(err.Error(), "versionContract") {
		t.Fatalf("update that moves the contract down: err = %v", err)
	}
}

// An incompatible Talos and Kubernetes pair is refused on create and on an update that moves either
// side, and the override verb lets it through.
func TestTalosClusterRefusesAnIncompatiblePairUnlessOverridden(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	noOverride := &TalosClusterCustomValidator{}
	withOverride := &TalosClusterCustomValidator{Can: func(_ context.Context, verb, _, _, _, _ string) (bool, error) {
		return verb == OverrideVerb, nil
	}}

	bad := newTestCluster()
	bad.Spec.TalosVersion, bad.Spec.KubernetesVersion = testTalosVersion, "v1.32.0"

	if _, err := noOverride.ValidateCreate(ctx, bad); err == nil || !strings.Contains(err.Error(), "v1.32.0") {
		t.Fatalf("create with an incompatible pair: err = %v", err)
	}

	if _, err := withOverride.ValidateCreate(ctx, bad); err != nil {
		t.Fatalf("create with an incompatible pair under the override verb: %v", err)
	}

	old := newTestCluster()
	old.Spec.TalosVersion, old.Spec.KubernetesVersion = testTalosVersion, testK8sVersion
	old.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{Detected: "cilium", DetectedVersion: "v1.18.0"}

	edited := old.DeepCopy()
	edited.Spec.KubernetesVersion = "v1.37.1"

	if _, err := noOverride.ValidateUpdate(ctx, old, edited); err == nil || !strings.Contains(err.Error(), "v1.37.1") {
		t.Fatalf("update to a Kubernetes the installed Cilium does not run: err = %v", err)
	}

	if _, err := withOverride.ValidateUpdate(ctx, old, edited); err != nil {
		t.Fatalf("the same update under the override verb: %v", err)
	}
}

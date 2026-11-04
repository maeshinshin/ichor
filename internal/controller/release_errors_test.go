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
	"errors"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	patchA = "v1.14.5"
	patchB = "v1.14.4"
)

// Each list records its own error, so one repository that cannot be listed does not make the other lists
// look failed (ADR 0115, amendment 2026-11-04).
func TestUpgradeTargetsOfRecordsTheErrorOfEachListOnItsOwn(t *testing.T) {
	prev := &ichorv1alpha1.IchorUpgradeTargets{Talos: []string{"v1.14.3"}, TalosError: "old"}

	got := upgradeTargetsOf(prev, metav1.Now(),
		listing{tags: []string{patchA, patchB}}, listing{err: errors.New("ghcr unreachable")})

	if got.TalosError != "" {
		t.Fatalf("lists that listed fine kept errors: talos %q", got.TalosError)
	}

	if !strings.Contains(got.KubernetesError, "ghcr unreachable") {
		t.Fatalf("kubernetesError = %q, want the listing failure", got.KubernetesError)
	}

	if !slices.Equal(got.Talos, []string{patchA, patchB}) {
		t.Fatalf("a failing Kubernetes listing blanked the others: talos %v", got.Talos)
	}

	if !strings.Contains(got.Error, "ghcr unreachable") {
		t.Fatalf("error = %q should still join the failures", got.Error)
	}
}

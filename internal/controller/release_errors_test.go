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

// Each list records its own error, so one repository that cannot be listed does not make the other lists
// look failed.
func TestUpgradeTargetsOfRecordsTheErrorOfEachListOnItsOwn(t *testing.T) {
	prev := &ichorv1alpha1.IchorUpgradeTargets{Talos: []string{"v1.14.3"}, TalosError: "old", CiliumError: "old"}

	got := upgradeTargetsOf(prev, metav1.Now(),
		listing{tags: []string{"v1.14.5", "v1.14.4"}}, listing{err: errors.New("ghcr unreachable")}, listing{tags: []string{"v1.20.5"}})

	if got.TalosError != "" || got.CiliumError != "" {
		t.Fatalf("lists that listed fine kept errors: talos %q, cilium %q", got.TalosError, got.CiliumError)
	}

	if !strings.Contains(got.KubernetesError, "ghcr unreachable") {
		t.Fatalf("kubernetesError = %q, want the listing failure", got.KubernetesError)
	}

	if !slices.Equal(got.Talos, []string{"v1.14.5", "v1.14.4"}) || !slices.Equal(got.Cilium, []string{"v1.20.5"}) {
		t.Fatalf("a failing Kubernetes listing blanked the others: talos %v, cilium %v", got.Talos, got.Cilium)
	}

	if !strings.Contains(got.Error, "ghcr unreachable") {
		t.Fatalf("error = %q should still join the failures", got.Error)
	}
}

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
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// An unset preset is the CRD default, cilium, so the readers that gate on cilium must agree with
// an explicit one.
func TestUnsetPresetReadsAsCilium(t *testing.T) {
	unset, explicit := &ichorv1alpha1.TalosCluster{}, &ichorv1alpha1.TalosCluster{}
	explicit.Spec.CNI.Preset = ciliumName

	if err := verifiableCNI(unset); err != nil {
		t.Errorf("verifiableCNI refuses an unset preset: %v", err)
	}

	if got, want := cniHandoverPending(unset), cniHandoverPending(explicit); got != want || got == "" {
		t.Errorf("cniHandoverPending(unset) = %q, explicit cilium = %q; both must wait for the hand-over", got, want)
	}
}

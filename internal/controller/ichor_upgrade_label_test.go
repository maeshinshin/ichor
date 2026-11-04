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

// config/cilium-network-policy selects the installer's pod by this label (ADR 0100): without it
// the default deny would cut the Job off from the API server.
func TestInstallerJobPodCarriesTheLabelItsNetworkPolicySelects(t *testing.T) {
	job := newInstallerJob("ichor-system", "ichor-installer-x", &ichorv1alpha1.TalosOperation{}, &ichorv1alpha1.IchorInstallation{}, testInstallerDigest, "cilium", false)

	if got := job.Spec.Template.Labels["app.kubernetes.io/name"]; got != "ichor-installer" {
		t.Errorf("installer pod label app.kubernetes.io/name = %q, want ichor-installer", got)
	}
}

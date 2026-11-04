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

package render

import (
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// SecureBoot and lockdown render nothing of their own; Required only makes a TPM key
// check the SecureBoot state, and an explicit choice wins.
func TestSecureBootRender(t *testing.T) {
	bundle := testBundle(t)
	cp := testMachine("cp", roleControlPlane, nil)
	base := mustRender(t, testCluster("true"), cp, bundle, nil)

	sb := func(mode string) *ichorv1alpha1.TalosClusterSecureBoot {
		return &ichorv1alpha1.TalosClusterSecureBoot{Mode: mode}
	}

	t.Run("modes and lockdown alone render the same bytes", func(t *testing.T) {
		for name, s := range map[string]*ichorv1alpha1.TalosClusterSecurity{
			"off":       {SecureBoot: sb("Off")},
			"preferred": {SecureBoot: sb("Preferred")},
			"required":  {SecureBoot: sb("Required")},
			"lockdown":  {KernelLockdown: "confidentiality"},
		} {
			if got := mustRender(t, secCluster(s), cp, bundle, nil); got != base {
				t.Errorf("%s: render differs from a cluster without spec.security", name)
			}
		}
	})

	tpm := func(mode string, require *bool) *ichorv1alpha1.TalosCluster {
		return secCluster(&ichorv1alpha1.TalosClusterSecurity{
			SecureBoot:     sb(mode),
			DiskEncryption: &ichorv1alpha1.TalosClusterDiskEncryption{Type: "tpm", RequireSecureBoot: require},
		})
	}

	t.Run("Required makes a TPM key check SecureBoot unless told otherwise", func(t *testing.T) {
		out := mustRender(t, tpm("Required", nil), cp, bundle, nil)
		requireAll(t, section(out, "name: STATE\n"), "checkSecurebootStatusOnEnroll: true")
		requireAll(t, section(out, "name: EPHEMERAL\n"), "checkSecurebootStatusOnEnroll: true")

		no := false
		requireNone(t, mustRender(t, tpm("Required", &no), cp, bundle, nil), "checkSecurebootStatusOnEnroll: true")
	})

	t.Run("Preferred and Off leave the TPM key as it was", func(t *testing.T) {
		for _, mode := range []string{"Preferred", "Off"} {
			requireNone(t, mustRender(t, tpm(mode, nil), cp, bundle, nil), "checkSecurebootStatusOnEnroll")
		}
	})
}

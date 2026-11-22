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
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const deviceOwnershipKind = "kind: CRICustomizationConfig"

func containerdCluster(on bool) *ichorv1alpha1.TalosCluster {
	c := testCluster("true")
	c.Spec.Containerd = &ichorv1alpha1.TalosClusterContainerd{DeviceOwnershipFromSecurityContext: on}

	return c
}

// TestContainerdDeviceOwnership checks that true renders one fixed customization on every node
// type, and that unset and false render the same bytes as before the field existed.
func TestContainerdDeviceOwnership(t *testing.T) {
	bundle := testBundle(t)

	for _, role := range []string{roleControlPlane, roleWorker} {
		m := testMachine("node-a", role, nil)

		out := mustRender(t, containerdCluster(true), m, bundle, nil)
		requireAll(t, section(out, deviceOwnershipKind), "name: ichor-device-ownership",
			`io.containerd.cri.v1.runtime`, "device_ownership_from_security_context = true")

		if strings.Count(out, deviceOwnershipKind) != 1 {
			t.Errorf("%s: want exactly one customization, got %d", role, strings.Count(out, deviceOwnershipKind))
		}

		base := mustRender(t, testCluster("true"), m, bundle, nil)
		if off := mustRender(t, containerdCluster(false), m, bundle, nil); off != base {
			t.Errorf("%s: false must render the same bytes as unset", role)
		}

		requireNone(t, base, deviceOwnershipKind)
	}
}

func TestValidateContainerdNeedsContract114(t *testing.T) {
	c := containerdCluster(true)
	if err := ValidateContainerd(c); err != nil {
		t.Fatalf("v1.14: %v", err)
	}

	c.Spec.VersionContract = contractV113
	if err := ValidateContainerd(c); err == nil || !strings.Contains(err.Error(), "v1.14") {
		t.Fatalf("v1.13 must be refused naming v1.14, got %v", err)
	}

	c.Spec.Containerd.DeviceOwnershipFromSecurityContext = false
	if err := ValidateContainerd(c); err != nil {
		t.Fatalf("false needs no contract: %v", err)
	}
}

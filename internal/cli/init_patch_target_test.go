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

package cli

import (
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The bootstrap patch sets machine.type to the first node's role. At the cluster layer it
// applied to every machine, so a worker approved later was rendered as a control plane
// ("issuing CA key is required for controlplane nodes").
func TestBuildInlineManifestsPatch_OnlyTargetsTheFirstNodesRole(t *testing.T) {
	cluster, err := buildInitPlan(validInitPlanInput(), nil)
	if err != nil {
		t.Fatal(err)
	}

	machine := &ichorv1alpha1.TalosMachine{Spec: ichorv1alpha1.TalosMachineSpec{Role: roleControlPlane}}

	p := buildInlineManifestsPatch(cluster, machine, "10.0.0.10:50000", nil)

	if p.Spec.Layer != "role" || p.Spec.Target == nil || p.Spec.Target.Role != roleControlPlane {
		t.Fatalf("layer/target = %q/%+v, want the role layer targeting %q", p.Spec.Layer, p.Spec.Target, roleControlPlane)
	}
}

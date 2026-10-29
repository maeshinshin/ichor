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
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

// TestBuildAdoptPlan_RefusesJWTAuthenticators: a node whose KubeAuthenticationConfig already has
// jwt entries would lose them to the render, so adopt refuses instead of dropping them.
func TestBuildAdoptPlan_RefusesJWTAuthenticators(t *testing.T) {
	bundle := newTestBundle(t)
	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)
	machine.Spec.Role = "controlplane"

	withIdP := cluster.DeepCopy()
	withIdP.Spec.Authentication = &ichorv1alpha1.TalosClusterAuthentication{External: []ichorv1alpha1.TalosClusterExternalIssuer{{
		IssuerURL: "https://idp.example.org", Audiences: []string{"ichor"},
		UsernameClaim: "email", GroupsClaim: "groups", UsernamePrefix: "oidc:", GroupsPrefix: "oidc:",
	}}}

	currentCfg, _, err := render.Render(withIdP, machine, bundle, nil, nil)
	if err != nil {
		t.Fatalf("rendering the live configuration: %v", err)
	}

	_, err = buildAdoptPlan(cluster, machine, bundle, nil, currentCfg, nil, ownership{})
	if err == nil || !strings.Contains(err.Error(), "spec.authentication.external") {
		t.Fatalf("buildAdoptPlan error = %v, want a refusal naming spec.authentication.external", err)
	}
}

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
	"os"
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

// ichorOwnIssuer is Ichor's own authenticator as init renders it.
func ichorOwnIssuer() *ichorv1alpha1.TalosClusterAuthentication {
	return &ichorv1alpha1.TalosClusterAuthentication{Ichor: &ichorv1alpha1.TalosClusterIchorIssuer{
		Enabled: true, URL: "https://ichor.example.org",
	}}
}

func liveWithAuth(t *testing.T, auth *ichorv1alpha1.TalosClusterAuthentication) (*ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, []byte) {
	t.Helper()

	bundle := newTestBundle(t)
	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)
	machine.Spec.Role = "controlplane"

	live := cluster.DeepCopy()
	live.Spec.Authentication = auth

	certFile, _, _ := writeTestKeyPair(t)

	caPEM, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}

	sys := &render.SystemInputs{OIDCTrust: &render.OIDCTrust{
		DiscoveryURL: "https://ichor-ui-service.ichor-system.svc:8444/.well-known/openid-configuration", CAPEM: string(caPEM),
	}}

	cfg, _, err := render.Render(live, machine, bundle, nil, sys)
	if err != nil {
		t.Fatalf("rendering the live configuration: %v", err)
	}

	return cluster, machine, cfg
}

// An Ichor-rendered authenticator on the node is Ichor's own, not a foreign identity provider.
func TestBuildAdoptPlan_AcceptsIchorOwnAuthenticator(t *testing.T) {
	cluster, machine, cfg := liveWithAuth(t, ichorOwnIssuer())

	plan, err := buildAdoptPlan(cluster, machine, newTestBundle(t), nil, cfg, nil, ownership{})
	if err != nil {
		t.Fatalf("buildAdoptPlan error = %v, want none", err)
	}

	if plan.Patch != nil || len(plan.UnrepresentablePaths) > 0 {
		t.Fatalf("plan = %+v, want no patch and nothing unrepresentable", plan)
	}
}

func TestBuildAdoptPlan_RefusesForeignBesideIchorOwn(t *testing.T) {
	auth := ichorOwnIssuer()
	auth.External = []ichorv1alpha1.TalosClusterExternalIssuer{{
		IssuerURL: "https://idp.example.org", Audiences: []string{"ichor-kube"},
		UsernameClaim: "email", GroupsClaim: "groups", UsernamePrefix: "oidc:", GroupsPrefix: "oidc:",
	}}
	cluster, machine, cfg := liveWithAuth(t, auth)

	_, err := buildAdoptPlan(cluster, machine, newTestBundle(t), nil, cfg, nil, ownership{})
	if err == nil || !strings.Contains(err.Error(), "has 1 jwt authenticator(s)") {
		t.Fatalf("buildAdoptPlan error = %v, want a refusal naming only the one foreign authenticator", err)
	}
}

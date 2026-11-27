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
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

const (
	oidcPrefix       = "oidc:"
	testDiscoveryURL = "https://ichor-ui-service.ichor-system.svc:8444/.well-known/openid-configuration"
)

// TestBuildAdoptPlan_RefusesJWTAuthenticators: a node whose KubeAuthenticationConfig already has
// jwt entries would lose them to the render, so adopt refuses instead of dropping them.
func TestBuildAdoptPlan_RefusesJWTAuthenticators(t *testing.T) {
	bundle := newTestBundle(t)
	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)
	machine.Spec.Role = roleControlPlane

	withIdP := cluster.DeepCopy()
	withIdP.Spec.Authentication = &ichorv1alpha1.TalosClusterAuthentication{External: []ichorv1alpha1.TalosClusterExternalIssuer{{
		IssuerURL: "https://idp.example.org", Audiences: []string{"ichor"},
		UsernameClaim: "email", GroupsClaim: "groups", UsernamePrefix: oidcPrefix, GroupsPrefix: oidcPrefix,
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
	machine.Spec.Role = roleControlPlane

	live := cluster.DeepCopy()
	live.Spec.Authentication = auth

	certFile, _, _ := writeTestKeyPair(t)

	caPEM, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}

	sys := &render.SystemInputs{OIDCTrust: &render.OIDCTrust{
		DiscoveryURL: testDiscoveryURL, CAPEM: string(caPEM),
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

	plan, err := buildAdoptPlan(cluster, machine, newTestBundle(t), nil, cfg, nil, ownership{IchorAuthenticator: true})
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
		IssuerURL: "https://idp.example.org", Audiences: []string{defaultAuthAudience},
		UsernameClaim: "email", GroupsClaim: "groups", UsernamePrefix: oidcPrefix, GroupsPrefix: oidcPrefix,
	}}
	cluster, machine, cfg := liveWithAuth(t, auth)

	_, err := buildAdoptPlan(cluster, machine, newTestBundle(t), nil, cfg, nil, ownership{})
	if err == nil || !strings.Contains(err.Error(), "has 1 jwt authenticator(s)") {
		t.Fatalf("buildAdoptPlan error = %v, want a refusal naming only the one foreign authenticator", err)
	}
}

// authenticatorPlan plans one node that carries Ichor's authenticator, for a cluster whose spec has none, the way adopt
// does: the sign-in plan first (against k8s, with an Ichor install), then the node's plan under what that plan owns.
// It returns the plan and what adopt said about it.
func authenticatorPlan(t *testing.T, k8s client.Client, auth *ichorv1alpha1.TalosClusterAuthentication) (*adoptPlan, string) {
	t.Helper()

	_, _, cfg := liveWithAuth(t, ichorOwnIssuer())
	cluster, machine, _ := liveWithAuth(t, nil)
	cluster.Spec.Authentication = auth

	var out bytes.Buffer

	st := &adoptState{cluster: cluster}
	deps := adoptDeps{K8s: k8s, Stderr: &out, Ichor: &adoptWriteIchor{}, UIURL: defaultUIURL, AdminUser: defaultAdminUser}

	if err := st.planSignIn(context.Background(), deps); err != nil {
		t.Fatalf("planSignIn: %v", err)
	}

	plan, err := buildAdoptPlan(cluster, machine, newTestBundle(t), nil, cfg, nil, st.own(&adoptNode{}))
	if err != nil {
		t.Fatalf("buildAdoptPlan: %v", err)
	}

	reportPlans(deps.say, []*adoptPlan{plan})

	return plan, out.String()
}

func helmControllerClient(t *testing.T) client.Client {
	t.Helper()

	return fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(controllerDeployment(true)).Build()
}

// Adopt that does not turn sign-in on (Helm-managed Ichor, or the operator's enabled: false) renders no authenticator,
// so the node's stays in the patch and adopt does not say it turns sign-in on again.
func TestAdoptPlan_KeepsTheAuthenticatorWhenSignInIsNotTurnedOn(t *testing.T) {
	disabled := &ichorv1alpha1.TalosClusterAuthentication{Ichor: &ichorv1alpha1.TalosClusterIchorIssuer{Enabled: false, URL: "https://ichor.example.org"}}

	for name, tc := range map[string]struct {
		k8s  client.Client
		auth *ichorv1alpha1.TalosClusterAuthentication
	}{
		"helm-managed":  {helmControllerClient(t), nil},
		"enabled false": {fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build(), disabled},
	} {
		t.Run(name, func(t *testing.T) {
			plan, said := authenticatorPlan(t, tc.k8s, tc.auth)

			if !slices.ContainsFunc(plan.UnrepresentablePaths, func(p string) bool { return underPath(p, kindKubeAuthenticationConfig) }) {
				t.Errorf("plan = %+v, want the KubeAuthenticationConfig kept in the drift", plan)
			}

			if strings.Contains(said, "turns sign-in on again") || !strings.Contains(said, "adopt refuses the node rather than remove it") || !strings.Contains(said, "spec.authentication.ichor") {
				t.Errorf("adopt said:\n%s\nwant the refusal line and not \"turns sign-in on again\"", said)
			}
		})
	}
}

// A non-Helm adopt turns sign-in on: the node's authenticator is replaced by the render, not captured.
func TestAdoptPlan_DropsTheAuthenticatorWhenSignInIsTurnedOn(t *testing.T) {
	plan, said := authenticatorPlan(t, fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build(), nil)

	if plan.Patch != nil || plan.AuthenticatorRefused {
		t.Errorf("plan = %+v, want no patch and the authenticator not kept", plan)
	}

	if !strings.Contains(said, "turns sign-in on again") {
		t.Errorf("adopt said:\n%s\nwant the \"turns sign-in on again\" line", said)
	}
}

// A re-run of adopt on a cluster whose spec.authentication.ichor is already enabled: the node carries Ichor's
// authenticator, rendered earlier with another CA than the issuer publishes now, so it differs from the
// baseline although the issuer URL is the same. It is Ichor's own and the render replaces it: not blocked, no refusal line.
func TestAdoptPlan_RerunWithSignInAlreadyOnIsNotBlocked(t *testing.T) {
	_, _, cfg := liveWithAuth(t, ichorOwnIssuer())
	cluster, machine, _ := liveWithAuth(t, nil)
	cluster.Spec.Authentication = ichorOwnIssuer()

	cert, _, _ := writeTestKeyPair(t)

	ca, err := os.ReadFile(cert)
	if err != nil {
		t.Fatal(err)
	}

	sys := &render.SystemInputs{OIDCTrust: &render.OIDCTrust{
		DiscoveryURL: testDiscoveryURL,
		CAPEM:        string(ca) + "# reissued\n",
	}}

	var out bytes.Buffer

	st := &adoptState{cluster: cluster}
	deps := adoptDeps{K8s: fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build(), Stderr: &out, Ichor: &adoptWriteIchor{}, UIURL: defaultUIURL, AdminUser: defaultAdminUser}

	if err := st.planSignIn(context.Background(), deps); err != nil {
		t.Fatalf("planSignIn: %v", err)
	}

	plan, err := buildAdoptPlan(cluster, machine, newTestBundle(t), nil, cfg, sys, st.own(&adoptNode{}))
	if err != nil {
		t.Fatalf("buildAdoptPlan: %v", err)
	}

	reportPlans(deps.say, []*adoptPlan{plan})

	if len(plan.UnrepresentablePaths) > 0 || plan.AuthenticatorRefused || strings.Contains(out.String(), "adopt refuses the node") {
		t.Errorf("plan = %+v, adopt said:\n%s\nwant the node not blocked and no refusal line", plan, out.String())
	}
}

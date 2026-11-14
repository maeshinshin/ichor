/*
Copyright 2026.

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

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	mnetwork "github.com/siderolabs/talos/pkg/machinery/config/types/network"

	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

const testDNSPrimary = "1.1.1.1"

const classAResolverDoc = `---
apiVersion: v1alpha1
kind: ResolverConfig
nameservers:
    - address: 1.1.1.1
    - address: 8.8.8.8
`

func nameserversOf(t *testing.T, cfg []byte) []string {
	t.Helper()

	provider, err := configloader.NewFromBytes(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var out []string

	for _, d := range provider.Documents() {
		if r, ok := d.(*mnetwork.ResolverConfigV1Alpha1); ok {
			for _, ns := range r.ResolverNameservers {
				out = append(out, ns.Address.String())
			}
		}
	}

	return out
}

// liveWithResolver is what a node holds that was set up with the nameservers 1.1.1.1 and 8.8.8.8.
func liveWithResolver(t *testing.T, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle) []byte {
	t.Helper()

	p := ichorv1alpha1.TalosConfigPatch{
		Name: "fixture-resolver", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
			Layer:      cniPatchLayer,
			Documents: []ichorv1alpha1.TalosConfigPatchDocument{{
				Kind: kindResolverConfig,
				ResolverConfig: &typedconfig.ResolverConfigV1Alpha1{ResolverNameservers: []*typedconfig.NameserverConfig{
					{Address: testDNSPrimary}, {Address: "8.8.8.8"},
				}},
			}},
		},
	}

	live, _, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{p}, nil)
	if err != nil {
		t.Fatal(err)
	}

	return live
}

// A foreign cluster with DNS nameservers is no longer blocked: the ResolverConfig is captured into a
// patch, and rendering with it reproduces the live configuration.
func TestAdoptCapturesResolverConfig(t *testing.T) {
	cp, wk := classAFixture(t, "v114")
	// The v1.14 fixture's ResolverConfig (hostDNS only) takes the nameservers, so there is still one document.
	withNS := strings.Replace(classAResolverDoc, "---\napiVersion: v1alpha1\nkind: ResolverConfig\n", "", 1)
	cp = []byte(strings.Replace(string(cp), "kind: ResolverConfig\n", "kind: ResolverConfig\n"+withNS, 1))
	wk = []byte(strings.Replace(string(wk), "kind: ResolverConfig\n", "kind: ResolverConfig\n"+withNS, 1))

	env, deps := classAFlow(t, "v1.14.3", cp, wk)

	out := planOutput(t, env, deps, false)
	if strings.Contains(out, "cannot patch automatically") || !strings.Contains(out, "drift found, patch ready (zero drift verified)") {
		t.Fatalf("the ResolverConfig was not captured:\n%s", out)
	}
}

func TestBuildAdoptPlanCapturesResolverConfigAndRoundTrips(t *testing.T) {
	bundle := newTestBundle(t)
	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)

	live := liveWithResolver(t, cluster, newTestMachine(cluster.Name), bundle)

	plan, err := buildAdoptPlan(cluster, machine, bundle, nil, live, nil, ownership{})
	if err != nil {
		t.Fatal(err)
	}

	if plan.Patch == nil || len(plan.UnrepresentablePaths) != 0 {
		t.Fatalf("plan = %+v, want a patch and no unrepresentable paths", plan)
	}

	if len(plan.Patch.Spec.Documents) != 1 || plan.Patch.Spec.Documents[0].Kind != kindResolverConfig {
		t.Fatalf("patch documents = %+v, want one ResolverConfig", plan.Patch.Spec.Documents)
	}

	rendered, _, err := render.Render(cluster, machine, bundle, []ichorv1alpha1.TalosConfigPatch{*plan.Patch}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := nameserversOf(t, rendered); strings.Join(got, ",") != "1.1.1.1,8.8.8.8" {
		t.Fatalf("rendered nameservers = %v", got)
	}
}

// spec.network.dnsServers already renders a ResolverConfig and the render refuses a patch with one, so
// a differing live ResolverConfig is left unrepresentable instead of captured.
func TestBuildAdoptPlanDoesNotCaptureResolverConfigWhenSpecSetsDNS(t *testing.T) {
	bundle := newTestBundle(t)
	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)
	machine.Spec.Network = &ichorv1alpha1.TalosMachineNetwork{DNSServers: []string{"9.9.9.9"}}

	live := liveWithResolver(t, cluster, newTestMachine(cluster.Name), bundle)

	plan, err := buildAdoptPlan(cluster, machine, bundle, nil, live, nil, ownership{})
	if err != nil {
		t.Fatal(err)
	}

	if plan.Patch != nil || len(plan.UnrepresentablePaths) == 0 {
		t.Fatalf("plan = %+v, want no patch and the ResolverConfig paths unrepresentable", plan)
	}
}

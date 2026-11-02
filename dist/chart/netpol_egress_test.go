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

package chart

import (
	"encoding/json"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/constants"

	"github.com/maeshinshin/ichor/internal/controller"
)

// Default-deny both ways, narrowed egress, on by default with Cilium.

const ciliumAPI = "cilium.io/v2/CiliumNetworkPolicy"

func renderWithAPIs(t *testing.T, apis string, sets ...string) []map[string]any {
	t.Helper()

	args := []string{"template", "ichor", ".", "--api-versions", apis}
	for _, s := range sets {
		args = append(args, "--set", s)
	}

	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return decodeYAMLDocs(t, out, "helm template with api versions")
}

func TestChartCiliumPoliciesAreOnWhereCiliumsAPIIsServed(t *testing.T) {
	if got := findAll(renderWithAPIs(t, ciliumAPI), kindCNP); len(got) != 3 {
		t.Errorf("with the API served and enabled unset, got %d policies, want default-deny, controller and ui (the installer is off by default)", len(got))
	}

	if got := findAll(renderWithAPIs(t, "v1"), kindCNP); len(got) != 0 {
		t.Errorf("without the API %d policies are rendered", len(got))
	}

	if got := findAll(renderWithAPIs(t, ciliumAPI, "networkPolicy.cilium.enabled=false"), kindCNP); len(got) != 0 {
		t.Errorf("enabled=false still rendered %d policies", len(got))
	}

	// The escape hatch also reaches the controller: no egress policy of its own, no installer apply.
	docs := renderWithAPIs(t, ciliumAPI, "networkPolicy.cilium.enabled=false")
	args := containerArgs(find(docs, kindDeployment, "ichor-controller-manager"))

	if !containsStr(args, "--no-cilium-policies") {
		t.Errorf("enabled=false must pass --no-cilium-policies: %v", args)
	}

	docs = renderWithAPIs(t, ciliumAPI, "networkPolicy.cilium.dynamicTalosEgress=false")
	args = containerArgs(find(docs, kindDeployment, "ichor-controller-manager"))

	if !containsStr(args, "--talos-egress-policy=false") {
		t.Errorf("dynamicTalosEgress=false must pass --talos-egress-policy=false: %v", args)
	}

	if !containsStr(containerArgs(find(renderWithAPIs(t, ciliumAPI), kindDeployment, "ichor-controller-manager")),
		"--talos-egress-policy-name=ichor-controller-talos-egress") {
		t.Errorf("the controller is not told the name of its egress policy")
	}
}

// egressPeers maps each egress rule's peer kind ("entity:world", "cidr", "endpoints") to its ports.
func egressPorts(spec map[string]any) map[string][]int {
	out := map[string][]int{}

	rules, _ := spec["egress"].([]any)
	for _, r := range rules {
		rule, _ := r.(map[string]any)
		toPorts, _ := rule["toPorts"].([]any)

		var ports []int

		for _, tp := range toPorts {
			ps, _ := tp.(map[string]any)["ports"].([]any)
			for _, p := range ps {
				n, _ := strconv.Atoi(p.(map[string]any)["port"].(string))
				ports = append(ports, n)
			}
		}

		var peers []string

		for _, e := range asList(rule["toEntities"]) {
			peers = append(peers, "entity:"+e.(string))
		}

		for _, k := range []string{"toCIDR", "toCIDRSet"} {
			if rule[k] != nil {
				peers = append(peers, "cidr")
			}
		}

		if rule["toEndpoints"] != nil {
			peers = append(peers, "endpoints")
		}

		for _, p := range peers {
			out[p] = append(out[p], ports...)
		}
	}

	return out
}

func asList(v any) []any {
	l, _ := v.([]any)

	return l
}

// Both copies, the chart's and the kustomize one, cover every port the code dials and name no more
// `world` than the apid-free flows need.
func TestCiliumEgressCoversEveryDialedPort(t *testing.T) {
	copies := map[string]map[string]map[string]any{
		"kustomize": cnpSpecs(t, loadYAMLDocs(t, netpolConfigPath), ""),
		"chart":     cnpSpecs(t, renderChartWithValues(t, "ichor", netpolAllOn...), "ichor-"),
	}

	// Derived from the code's own constants: the controller's dynamic policy ports must be allowed
	// toward this cluster's nodes (host, remote-node) in the static policy too.
	dynamic := controller.TalosEgressPorts()
	for _, p := range []int{constants.ApidPort, constants.DefaultControlPlanePort, constants.EtcdPeerPort} {
		if !slices.Contains(dynamic, p) {
			t.Errorf("the dynamic egress policy does not open %d", p)
		}
	}

	for name, specs := range copies {
		ctrl, ui, inst := egressPorts(specs["controller-manager"]), egressPorts(specs["ui"]), egressPorts(specs["installer"])

		for _, p := range dynamic {
			if !slices.Contains(ctrl["entity:host"], p) {
				t.Errorf("%s: the controller cannot reach port %d on this cluster's nodes", name, p)
			}
		}

		if !slices.Contains(ctrl["entity:kube-apiserver"], 6443) || !slices.Contains(ui["entity:kube-apiserver"], 6443) ||
			!slices.Contains(inst["entity:kube-apiserver"], 6443) {
			t.Errorf("%s: a workload cannot reach the Kubernetes API", name)
		}

		if !slices.Contains(ui["entity:host"], constants.ApidPort) {
			t.Errorf("%s: ichor-ui cannot reach apid on this cluster's nodes", name)
		}

		for _, who := range []string{"controller-manager", "ui"} {
			if !slices.Contains(egressPorts(specs[who])["endpoints"], 53) {
				t.Errorf("%s: %s cannot resolve names", name, who)
			}

			if !strings.Contains(mustJSON(specs[who]), `"rules":{"dns":[{"matchPattern":"*"}]}`) {
				t.Errorf("%s: %s's DNS rule has no L7 dns rule, so toFQDNs rules can never match", name, who)
			}
		}

		// No world for the controller: nodes, API servers and S3 come from its own CRs.
		if len(ctrl["entity:world"]) != 0 {
			t.Errorf("%s: the controller still has world egress: %v", name, ctrl["entity:world"])
		}

		// ichor-ui keeps world only for 443 (the OIDC issuer, a pull request's host).
		if got := ui["entity:world"]; len(got) != 1 || got[0] != 443 {
			t.Errorf("%s: ichor-ui world egress = %v, want 443 only", name, got)
		}

		if len(inst) != 1 {
			t.Errorf("%s: the installer reaches %v, want the API server only", name, inst)
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)

	return string(b)
}

func TestChartDynamicTalosEgressFalseRestoresWorld(t *testing.T) {
	specs := cnpSpecs(t, renderChartWithValues(t, "ichor", append([]string{"networkPolicy.cilium.dynamicTalosEgress=false"}, netpolAllOn...)...), "ichor-")

	ctrl := egressPorts(specs["controller-manager"])
	for _, p := range []int{constants.ApidPort, constants.DefaultControlPlanePort} {
		if !slices.Contains(ctrl["entity:world"], p) {
			t.Errorf("port %d is not allowed to world with dynamicTalosEgress=false: %v", p, ctrl)
		}
	}

	if !slices.Contains(ctrl["entity:world"], 443) {
		t.Errorf("S3 (443) is not allowed to world with dynamicTalosEgress=false: %v", ctrl)
	}
}

func TestChartMetricsNamespacesNarrowTheScrapeRule(t *testing.T) {
	specs := cnpSpecs(t, renderChartWithValues(t, "ichor", append([]string{"networkPolicy.cilium.metricsNamespaces={monitoring}"}, netpolAllOn...)...), "ichor-")
	text := mustJSON(specs["controller-manager"]["ingress"])

	if !strings.Contains(text, `"k8s:io.kubernetes.pod.namespace":"monitoring"`) {
		t.Errorf("the metrics rule does not name the monitoring namespace: %s", text)
	}

	if got := ingressPorts(specs["controller-manager"]); len(got["cluster"]) != 0 {
		t.Errorf("metricsNamespaces set but the cluster entity still has ports: %v", got["cluster"])
	}
}

// The chart's talosCIDRs and externalCIDRs stay as static fallbacks in dynamic mode.
func TestChartStaticFallbackCIDRsStillApply(t *testing.T) {
	specs := cnpSpecs(t, renderChartWithValues(t, "ichor", append([]string{
		"networkPolicy.cilium.talosCIDRs={192.0.2.0/24}", "networkPolicy.cilium.externalCIDRs={198.51.100.0/24}",
	}, netpolAllOn...)...), "ichor-")

	ctrl := egressPorts(specs["controller-manager"])
	if !slices.Contains(ctrl["cidr"], constants.ApidPort) || !slices.Contains(ctrl["cidr"], 443) {
		t.Errorf("fallback CIDR rules missing for the controller: %v", ctrl)
	}

	if len(ctrl["entity:world"]) != 0 {
		t.Errorf("world appeared next to fallback CIDRs: %v", ctrl["entity:world"])
	}
}

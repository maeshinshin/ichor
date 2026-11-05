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

	out, err := helmTemplate("ichor", []string{"--api-versions", apis}, sets)
	if err != nil {
		t.Fatalf("helm template --api-versions %s (--set %s): %v\n%s", apis, strings.Join(sets, " --set "), err, out)
	}

	return decodeYAMLDocs(t, out, "helm template with api versions")
}

func TestChartCiliumPoliciesAreOnWhereCiliumsAPIIsServed(t *testing.T) {
	if got := findAll(renderWithAPIs(t, ciliumAPI), kindCNP); len(got) != 3 {
		t.Errorf("API served, enabled unset: %d policies, want default-deny, controller and ui (installer is off)", len(got))
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
	const kustomizeCopy = "kustomize"

	copies := map[string]map[string]map[string]any{
		kustomizeCopy: cnpSpecs(t, loadYAMLDocs(t, netpolConfigPath), ""),
		"chart":       cnpSpecs(t, renderChartWithValues(t, "ichor", netpolAllOn...), "ichor-"),
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

		// No world for ichor-ui either: its OIDC issuer comes from ichor-ui-oidc-egress, which the
		// controller writes from the Deployment.
		if got := ui["entity:world"]; len(got) != 0 {
			t.Errorf("%s: ichor-ui still has world egress: %v", name, got)
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
	sets := append([]string{"networkPolicy.cilium.dynamicTalosEgress=false"}, netpolAllOn...)
	specs := cnpSpecs(t, renderChartWithValues(t, "ichor", sets...), "ichor-")

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

var oidcOn = []string{
	"ichorUI.oidc.enabled=true", "ichorUI.oidc.issuer=https://idp.example.test", "ichorUI.oidc.clientId=ichor",
	"ichorUI.oidc.impersonate.groups={oidc:platform}", "ichorUI.oidc.impersonate.users={oidc:alice}",
}

// Ichor-ui's external endpoint is named by the controller from the Deployment's issuer, so the
// chart renders an external rule for it only as a fallback, and only while OIDC is on.
func TestChartUIExternalEgressIsAFallbackAndOnlyWithOIDC(t *testing.T) {
	ui := func(sets ...string) map[string][]int {
		specs := cnpSpecs(t, renderChartWithValues(t, "ichor", slices.Concat(sets, netpolAllOn)...), "ichor-")

		return egressPorts(specs["ui"])
	}

	if got := ui(oidcOn...); len(got["entity:world"]) != 0 || len(got["cidr"]) != 0 {
		t.Errorf("OIDC on, dynamic: ichor-ui has a static external rule: %v", got)
	}

	// The issuer reaches the pod by name: the Deployment carries it for the controller to read.
	docs := renderChartWithValues(t, "ichor", slices.Concat(oidcOn, netpolAllOn)...)
	if !strings.Contains(mustJSON(find(docs, kindDeployment, ichorUIDeploymentName)), `"ICHOR_OIDC_ISSUER"`) {
		t.Errorf("the ichor-ui Deployment does not carry ICHOR_OIDC_ISSUER, which the controller reads")
	}

	fallback := ui(append([]string{"networkPolicy.cilium.dynamicTalosEgress=false"}, oidcOn...)...)
	if !slices.Contains(fallback["entity:world"], 443) {
		t.Errorf("dynamicTalosEgress=false with OIDC: ichor-ui cannot reach the issuer: %v", fallback)
	}

	if got := ui("networkPolicy.cilium.dynamicTalosEgress=false"); slices.Contains(got["entity:world"], 443) {
		t.Errorf("dynamicTalosEgress=false but OIDC off: ichor-ui still reaches world on 443: %v", got)
	}

	got := ui(append([]string{"networkPolicy.cilium.externalCIDRs={198.51.100.0/24}"}, oidcOn...)...)
	if !slices.Contains(got["cidr"], 443) || len(got["entity:world"]) != 0 {
		t.Errorf("externalCIDRs with OIDC: want a CIDR rule on 443 and no world: %v", got)
	}

	if got := ui("networkPolicy.cilium.externalCIDRs={198.51.100.0/24}"); len(got["cidr"]) != 0 {
		t.Errorf("externalCIDRs but OIDC off: ichor-ui has an external rule: %v", got)
	}
}

func TestChartMetricsNamespacesNarrowTheScrapeRule(t *testing.T) {
	sets := append([]string{"networkPolicy.cilium.metricsNamespaces={monitoring}"}, netpolAllOn...)
	specs := cnpSpecs(t, renderChartWithValues(t, "ichor", sets...), "ichor-")
	text := mustJSON(specs["controller-manager"]["ingress"])

	if !strings.Contains(text, `"k8s:io.kubernetes.pod.namespace":"monitoring"`) {
		t.Errorf("the metrics rule does not name the monitoring namespace: %s", text)
	}

	if got := ingressPorts(specs["controller-manager"]); len(got["cluster"]) != 0 {
		t.Errorf("metricsNamespaces set but the cluster entity still has ports: %v", got["cluster"])
	}
}

// No source may open the metrics port until a namespace is named.
func TestChartMetricsPortIsClosedUntilANamespaceIsNamed(t *testing.T) {
	specs := cnpSpecs(t, renderChartWithValues(t, "ichor", netpolAllOn...), "ichor-")

	got := ingressPorts(specs["controller-manager"])
	for peer, ports := range got {
		if slices.Contains(ports, "8443") {
			t.Errorf("%s reaches the metrics port 8443 by default: %v", peer, got)
		}
	}
}

func TestChartMetricsPodLabelsNarrowTheNamespaces(t *testing.T) {
	sets := append([]string{
		"networkPolicy.cilium.metricsNamespaces={monitoring,observability}",
		"networkPolicy.cilium.metricsPodLabels.app\\.kubernetes\\.io/name=prometheus",
	}, netpolAllOn...)
	specs := cnpSpecs(t, renderChartWithValues(t, "ichor", sets...), "ichor-")
	text := mustJSON(specs["controller-manager"]["ingress"])

	for _, want := range []string{
		`{"matchLabels":{"k8s:app.kubernetes.io/name":"prometheus","k8s:io.kubernetes.pod.namespace":"monitoring"}}`,
		`{"matchLabels":{"k8s:app.kubernetes.io/name":"prometheus","k8s:io.kubernetes.pod.namespace":"observability"}}`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
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

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
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/internal/installer"
)

// Ichor's CiliumNetworkPolicies live twice, in config/cilium-network-policy (what the
// install manifest and the installer apply) and in this chart. These tests keep the two equal
// and tie every port to the Deployments that open it.

const (
	netpolConfigPath = "../../config/cilium-network-policy/policies.yaml"
	kindCNP          = "CiliumNetworkPolicy"
)

var netpolAllOn = []string{
	"networkPolicy.cilium.enabled=true", "installer.enabled=true",
	"ichorUI.users.enabled=true", "ichorUI.users.externalURL=https://ichor.example.com",
}

// cnpSpecs maps a policy's name with its prefix removed ("controller-manager") to its spec without
// the description, which is prose.
func cnpSpecs(t *testing.T, docs []map[string]any, prefix string) map[string]map[string]any {
	t.Helper()

	out := map[string]map[string]any{}

	for _, d := range findAll(docs, kindCNP) {
		spec, _ := d["spec"].(map[string]any)
		delete(spec, "description")
		out[strings.TrimPrefix(nameOf(d), prefix)] = spec
	}

	return out
}

func TestCiliumPoliciesAreOffByDefault(t *testing.T) {
	if got := findAll(renderChart(t, "ichor"), kindCNP); len(got) != 0 {
		t.Fatalf("the chart renders %d CiliumNetworkPolicies without networkPolicy.cilium.enabled", len(got))
	}
}

// The chart's policies and the kustomize ones, which the installer and init apply, are the same.
func TestChartCiliumPoliciesEqualTheKustomizeOnes(t *testing.T) {
	fromChart := cnpSpecs(t, renderChartWithValues(t, "ichor", netpolAllOn...), "ichor-")
	fromConfig := cnpSpecs(t, loadYAMLDocs(t, netpolConfigPath), "")

	want := []string{"controller-manager", "default-deny", "installer", "ui"}
	for _, name := range want {
		if fromChart[name] == nil || fromConfig[name] == nil {
			t.Fatalf("policy %q missing: chart has %d, config has %d policies", name, len(fromChart), len(fromConfig))
		}
	}

	if len(fromChart) != len(want) || len(fromConfig) != len(want) {
		t.Fatalf("policies: chart %d, config %d, want %d", len(fromChart), len(fromConfig), len(want))
	}

	for _, name := range want {
		if !reflect.DeepEqual(fromChart[name], fromConfig[name]) {
			t.Errorf("policy %q differs:\nchart:  %v\nconfig: %v", name, fromChart[name], fromConfig[name])
		}
	}
}

// ingressPorts lists the ports a policy's ingress rules open, per peer entity.
func ingressPorts(spec map[string]any) map[string][]string {
	out := map[string][]string{}

	rules, _ := spec["ingress"].([]any)
	for _, r := range rules {
		rule, _ := r.(map[string]any)
		entities, _ := rule["fromEntities"].([]any)
		toPorts, _ := rule["toPorts"].([]any)

		peers := make([]string, 0, len(entities)+1)
		for _, e := range entities {
			peers = append(peers, fmt.Sprint(e))
		}

		if rule["fromEndpoints"] != nil {
			peers = append(peers, "endpoints")
		}

		for _, peer := range peers {
			for _, tp := range toPorts {
				ports, _ := tp.(map[string]any)["ports"].([]any)
				for _, p := range ports {
					out[peer] = append(out[peer], p.(map[string]any)["port"].(string))
				}
			}
		}
	}

	return out
}

func selectorLabels(spec map[string]any) map[string]any {
	sel, _ := spec["endpointSelector"].(map[string]any)
	labels, _ := sel["matchLabels"].(map[string]any)

	return labels
}

func podLabels(deployment map[string]any) map[string]any {
	spec, _ := deployment["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	md, _ := tmpl["metadata"].(map[string]any)
	labels, _ := md["labels"].(map[string]any)

	return labels
}

func requireSubset(t *testing.T, what string, sel, labels map[string]any) {
	t.Helper()

	if len(sel) == 0 {
		t.Fatalf("%s selects nothing", what)
	}

	for k, v := range sel {
		if labels[k] != v {
			t.Errorf("%s selects %s=%v, but the pod labels are %v", what, k, v, labels)
		}
	}
}

func TestChartCiliumPolicyPortsAndSelectorsComeFromTheDeployments(t *testing.T) {
	ports := []string{"webhook.port=9555", "metrics.port=8555", "ichorUI.port=8099", "ichorUI.users.internalPort=8455",
		"networkPolicy.cilium.metricsNamespaces={monitoring}"}
	docs := renderChartWithValues(t, "ichor", slices.Concat(ports, netpolAllOn)...)
	specs := cnpSpecs(t, docs, "ichor-")

	ctrl := find(docs, kindDeployment, "ichor-controller-manager")
	ui := find(docs, kindDeployment, ichorUIDeploymentName)

	requireSubset(t, "the controller policy", selectorLabels(specs["controller-manager"]), podLabels(ctrl))
	requireSubset(t, "the ui policy", selectorLabels(specs["ui"]), podLabels(ui))

	ctrlPorts := ingressPorts(specs["controller-manager"])
	if got := strings.Join(ctrlPorts["kube-apiserver"], ","); got != "9555" {
		t.Errorf("webhook ingress from kube-apiserver = %q, want the webhook port 9555", got)
	}

	if got := strings.Join(ctrlPorts["endpoints"], ","); got != "8555" {
		t.Errorf("metrics ingress from the named namespaces = %q, want the metrics port 8555", got)
	}

	if len(ctrlPorts["cluster"]) != 0 {
		t.Errorf("the cluster entity still reaches the controller on %v", ctrlPorts["cluster"])
	}

	if got := strings.Join(ctrlPorts["host"], ","); got != "8081" {
		t.Errorf("probe ingress from the host = %q, want 8081", got)
	}

	// The Deployment really opens them.
	args := containerArgs(ctrl)
	for _, want := range []string{"--metrics-bind-address=:8555", "--health-probe-bind-address=:8081"} {
		if !containsStr(args, want) {
			t.Errorf("the controller does not run with %s: %v", want, args)
		}
	}

	uiArgs := containerArgs(ui)
	for _, want := range []string{"--addr=:8099", "--internal-addr=:8455"} {
		if !containsStr(uiArgs, want) {
			t.Errorf("ichor-ui does not run with %s: %v", want, uiArgs)
		}
	}

	uiPorts := ingressPorts(specs["ui"])
	if got := strings.Join(uiPorts["world"], ","); got != "8099" {
		t.Errorf("ui ingress from world = %q, want 8099", got)
	}

	if got := strings.Join(uiPorts["kube-apiserver"], ","); got != "8455" {
		t.Errorf("ui ingress from kube-apiserver = %q, want the internal port 8455", got)
	}

	// The installer Job's pod carries the label its policy selects.
	if got := selectorLabels(specs["installer"]); got["app.kubernetes.io/name"] != "ichor-installer" {
		t.Errorf("installer policy selector = %v", got)
	}

	// The Cilium Helm Job has none: it runs on the host network, which these policies do not govern.
	if specs["cni"] != nil {
		t.Errorf("a cni policy is rendered, but the Cilium Helm Job runs on the host network")
	}
}

func TestChartCiliumPoliciesDropWhatIsOff(t *testing.T) {
	docs := renderChartWithValues(t, "ichor", "networkPolicy.cilium.enabled=true", "webhook.enabled=false",
		"metrics.enabled=false", "ichorUI.enabled=false")
	specs := cnpSpecs(t, docs, "ichor-")

	if specs["ui"] != nil || specs["installer"] != nil {
		t.Errorf("ui or installer policy rendered while their workloads are off: %v", specs)
	}

	if got := ingressPorts(specs["controller-manager"]); len(got) != 1 || got["host"] == nil {
		t.Errorf("controller ingress = %v, want the probe rule only", got)
	}
}

// containerArgs returns the first container's args.
func containerArgs(deployment map[string]any) []any {
	podSpec := podSpecOf(deployment)
	containers, _ := podSpec["containers"].([]any)

	if len(containers) == 0 {
		return nil
	}

	args, _ := containers[0].(map[string]any)["args"].([]any)

	return args
}

// The kustomize policies name the ports config/manager, its patches and config/manager-ui open.
func TestKustomizeCiliumPolicyPortsMatchTheBaseDeployments(t *testing.T) {
	read := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		return string(b)
	}

	managerBase := read("../../config/manager/manager.yaml")
	webhookPatch := read("../../config/default/manager_webhook_patch.yaml")
	metricsPatch := read("../../config/default/manager_metrics_patch.yaml")
	uiBase := read("../../config/manager-ui/manager.yaml")

	specs := cnpSpecs(t, loadYAMLDocs(t, netpolConfigPath), "")
	ctrl, ui := ingressPorts(specs["controller-manager"]), ingressPorts(specs["ui"])

	for _, c := range []struct {
		what, got, source, want string
	}{
		{"webhook", strings.Join(ctrl["kube-apiserver"], ","), webhookPatch, "containerPort: 9443"},
		{"probes", strings.Join(ctrl["host"], ","), managerBase, "--health-probe-bind-address=:8081"},
		{"ui", strings.Join(ui["world"], ","), uiBase, "--addr=:8443"},
		{"ui internal", strings.Join(ui["kube-apiserver"], ","), "", fmt.Sprint(installer.UIInternalPort)},
	} {
		port := c.want[strings.LastIndex(c.want, ":")+1:]
		port = strings.TrimSpace(port)

		if c.got != port {
			t.Errorf("%s: the policy opens %q, the source says %q", c.what, c.got, port)
		}

		if c.source != "" && !strings.Contains(c.source, c.want) {
			t.Errorf("%s: %q not found in its source", c.what, c.want)
		}
	}

	// The install copy opens no metrics source; the operator names a scraper with a policy of their own.
	if !strings.Contains(metricsPatch, "--metrics-bind-address=:8443") {
		t.Errorf("the metrics port moved: update the guide's example policy (docs/guides/install-with-helm.md)")
	}

	if len(ctrl["cluster"]) != 0 || len(ctrl["endpoints"]) != 0 {
		t.Errorf("the install copy opens the metrics port to %v %v", ctrl["cluster"], ctrl["endpoints"])
	}

	requireSubset(t, "the controller policy", selectorLabels(specs["controller-manager"]),
		podLabels(loadYAMLDocs(t, "../../config/manager/manager.yaml")[1]))
	uiDeployment := loadYAMLDocs(t, "../../config/manager-ui/manager.yaml")[0]
	requireSubset(t, "the ui policy", selectorLabels(specs["ui"]), podLabels(uiDeployment))
}

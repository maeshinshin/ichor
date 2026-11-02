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
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func toYAMLString(t *testing.T, v any) string {
	t.Helper()

	b, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

// ADR 0108: the CIDR values replace the `world` entity, and the default keeps it.
func TestCiliumCIDRValuesReplaceWorld(t *testing.T) {
	render := func(extra ...string) string {
		var b strings.Builder

		values := append(append([]string{}, netpolAllOn...), extra...)

		for _, d := range findAll(renderChartWithValues(t, "ichor", values...), kindCNP) {
			b.WriteString(nameOf(d) + "\n")
			spec, _ := d["spec"].(map[string]any)
			b.WriteString(toYAMLString(t, spec))
		}

		return b.String()
	}

	def := render()
	if !strings.Contains(def, "world") || strings.Contains(def, "toCIDR") || strings.Contains(def, "fromCIDR") {
		t.Fatalf("the default policies must allow world and name no CIDR:\n%s", def)
	}

	narrow := render(
		"networkPolicy.cilium.externalCIDRs={10.0.0.0/8}",
		"networkPolicy.cilium.talosCIDRs={192.0.2.0/24}",
		"networkPolicy.cilium.uiIngressCIDRs={198.51.100.0/24}",
	)
	for _, want := range []string{"10.0.0.0/8", "192.0.2.0/24", "198.51.100.0/24"} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("the narrowed policies lack %s:\n%s", want, narrow)
		}
	}

	if strings.Contains(narrow, "world") {
		t.Fatalf("the narrowed policies still allow world:\n%s", narrow)
	}
}

// Both pods run as the image's user under the PriorityClass the chart creates.
func TestPodsRunAsTheImageUserUnderThePriorityClass(t *testing.T) {
	docs := renderChart(t, "ichor")

	if len(findAll(docs, "PriorityClass")) != 1 {
		t.Fatal("the chart does not render exactly one PriorityClass")
	}

	deployments := findAll(docs, "Deployment")
	if len(deployments) < 2 {
		t.Fatalf("want the controller and ichor-ui Deployments, got %d", len(deployments))
	}

	for _, d := range deployments {
		out := toYAMLString(t, d)
		for _, want := range []string{"priorityClassName: ichor-critical", "runAsUser: 65532", "runAsGroup: 65532"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s lacks %q", nameOf(d), want)
			}
		}
	}
}

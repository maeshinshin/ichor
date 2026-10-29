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
	"slices"
	"strings"
	"testing"
)

const specKey = "spec"

// uiParts returns the ichor-ui container, its pod volumes and the Service's port names and numbers.
func uiParts(t *testing.T, docs []map[string]any) (c map[string]any, volumes []any, svcPorts map[string]int) {
	t.Helper()

	dep := find(docs, kindDeployment, ichorUIDeploymentName)
	podSpec := dep[specKey].(map[string]any)["template"].(map[string]any)[specKey].(map[string]any)
	c = podSpec["containers"].([]any)[0].(map[string]any)
	volumes, _ = podSpec["volumes"].([]any)

	svc := find(docs, "Service", "ichor-ui-service")
	svcPorts = map[string]int{}

	for _, p := range svc[specKey].(map[string]any)["ports"].([]any) {
		pm := p.(map[string]any)
		svcPorts[pm["name"].(string)] = int(pm["port"].(float64))
	}

	return c, volumes, svcPorts
}

func probePortAndScheme(c map[string]any, probe string) string {
	g := c[probe].(map[string]any)["httpGet"].(map[string]any)

	return fmt.Sprint(g["scheme"], ":", g["port"])
}

func hasMount(c map[string]any, name string) bool {
	for _, m := range c["volumeMounts"].([]any) {
		if m.(map[string]any)["name"] == name {
			return true
		}
	}

	return false
}

// A fresh Helm install serves HTTPS on 8443 only.
func TestUIServesHTTPSOnlyByDefault(t *testing.T) {
	c, vols, ports := uiParts(t, renderChartWithValues(t, "ichor"))

	args := fmt.Sprint(c["args"])
	if !strings.Contains(args, "--addr=:8443") || strings.Contains(args, "--http-addr") {
		t.Errorf("args = %v, want --addr=:8443 and no --http-addr", args)
	}

	if got := probePortAndScheme(c, "readinessProbe"); got != "HTTPS:8443" {
		t.Errorf("readinessProbe = %s, want HTTPS:8443", got)
	}

	// Liveness must not depend on the certificate, which can arrive 60-90 s after the pod starts.
	live := c["livenessProbe"].(map[string]any)
	if _, ok := live["httpGet"]; ok || fmt.Sprint(live["tcpSocket"].(map[string]any)["port"]) != "8443" {
		t.Errorf("livenessProbe = %v, want a tcpSocket on 8443", live)
	}

	if len(ports) != 1 || ports["https"] != 8443 {
		t.Errorf("Service ports = %v, want https 8443 only", ports)
	}

	if !hasMount(c, "ui-tls") {
		t.Errorf("no ui-tls mount: %v", c["volumeMounts"])
	}

	i := slices.IndexFunc(vols, func(v any) bool { return v.(map[string]any)["name"] == "ui-tls" })
	if i < 0 {
		t.Fatalf("no ui-tls volume: %v", vols)
	}

	s := vols[i].(map[string]any)["secret"].(map[string]any)
	if s["secretName"] != "ichor-oidc-tls" || s["optional"] != true {
		t.Errorf("ui-tls volume = %v, want the optional Secret ichor-oidc-tls", s)
	}
}

// The operator's Secret is mounted next to the Ichor one. The removed ichorUI.plaintextPort, still set by an old
// values file, is ignored: no plaintext port anywhere.
func TestUIOperatorCertificateAndRemovedPlaintextPort(t *testing.T) {
	docs := renderChartWithValues(t, "ichor", "ichorUI.tls.secretName=my-cert", "ichorUI.plaintextPort=8080",
		"networkPolicy.cilium.enabled=true")
	c, _, ports := uiParts(t, docs)

	args := fmt.Sprint(c["args"])
	wants := []string{"--tls-cert-file=/etc/ichor/tls/tls.crt", "--tls-key-file=/etc/ichor/tls/tls.key"}
	for _, want := range wants {
		if !strings.Contains(args, want) {
			t.Errorf("args %s lack %s", args, want)
		}
	}

	if strings.Contains(args, "--http-addr") {
		t.Errorf("args %s still pass --http-addr", args)
	}

	if !hasMount(c, "tls") || !hasMount(c, "ui-tls") {
		t.Errorf("mounts = %v, want tls and ui-tls", c["volumeMounts"])
	}

	if len(ports) != 1 || ports["https"] != 8443 {
		t.Errorf("Service ports = %v, want https 8443 only", ports)
	}

	ui := ingressPorts(cnpSpecs(t, docs, "ichor-")["ui"])
	if got := strings.Join(ui["world"], ","); got != "8443" {
		t.Errorf("ui policy ingress = %q, want 8443", got)
	}
}

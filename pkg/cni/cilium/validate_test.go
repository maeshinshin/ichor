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

package cilium

import (
	"os"
	"strings"
	"testing"

	"k8s.io/utils/ptr"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/values"
)

const (
	keyBPF        = "bpf"
	keyGatewayAPI = "gatewayAPI"
	keyEnabled    = "enabled"
	keyDebug      = "debug"
	keyIPAM       = "ipam"
	keyMode       = "mode"
	tunnelGeneve  = "geneve"
)

// debugOn is a user value that every chart accepts.
var debugOn = map[string]any{keyDebug: map[string]any{keyEnabled: true}}

func TestValidateValues(t *testing.T) {
	replaced := ValidationContext{KubeProxy: cni.KubeProxyReplaced}
	enabled := ValidationContext{KubeProxy: cni.KubeProxyEnabled}

	with := func(c ValidationContext, f func(*ValidationContext)) ValidationContext {
		f(&c)

		return c
	}

	masq := map[string]any{keyBPF: map[string]any{"masquerade": true}}
	gw := map[string]any{keyGatewayAPI: map[string]any{keyEnabled: true}}

	// mesh is clustermesh.useAPIServer with the given clustermesh.apiserver.tls.auto (nil: the chart's).
	mesh := func(auto map[string]any) map[string]any {
		cm := map[string]any{"useAPIServer": true}
		if auto != nil {
			cm["apiserver"] = map[string]any{"tls": map[string]any{"auto": auto}}
		}

		return map[string]any{"clustermesh": cm}
	}

	tests := []struct {
		name    string
		user    map[string]any
		ctx     ValidationContext
		wantErr []string // substrings; none means success
	}{
		{name: "empty", ctx: replaced},
		{name: "valid user value", ctx: enabled, user: debugOn},
		{name: "free-form map accepts any key", ctx: enabled,
			user: map[string]any{"nodeSelector": map[string]any{"disk": "ssd"}}},
		{name: "bad mode", ctx: ValidationContext{KubeProxy: "nope"}, wantErr: []string{"nope"}},

		{name: "owned key", ctx: enabled, user: map[string]any{keyIPAM: map[string]any{keyMode: "cluster-pool"}},
			wantErr: []string{"ipam.mode is set by Ichor"}},
		{name: "owned top-level key", ctx: replaced, user: map[string]any{"k8sServicePort": 6443},
			wantErr: []string{"k8sServicePort is set by Ichor"}},
		{name: "owned nested capability", ctx: enabled,
			user: map[string]any{"securityContext": map[string]any{
				"capabilities": map[string]any{"ciliumAgent": []any{"ALL"}}}},
			wantErr: []string{"securityContext.capabilities.ciliumAgent"}},
		{name: "sibling of an owned key is fine", ctx: enabled,
			user: map[string]any{"cgroup": map[string]any{"autoMount": map[string]any{}}}},

		{name: "unknown key with suggestion", ctx: enabled, user: map[string]any{keyBPF: map[string]any{"masqurade": true}},
			wantErr: []string{"unknown key bpf.masqurade", "did you mean bpf.masquerade?"}},
		{name: "unknown top-level key, no suggestion", ctx: enabled, user: map[string]any{"zzzzzzzzzzzz": 1},
			wantErr: []string{"unknown key zzzzzzzzzzzz"}},

		{name: "schema type error", ctx: enabled, user: map[string]any{"operator": map[string]any{"replicas": "two"}},
			wantErr: []string{"chart schema", "operator"}},

		{name: "masquerade with forwardKubeDNSToHost true", ctx: with(enabled, func(c *ValidationContext) {
			c.ForwardKubeDNSToHost = ptr.To(true)
		}), user: masq, wantErr: []string{"forwardKubeDNSToHost"}},
		{name: "masquerade with forwardKubeDNSToHost false", ctx: with(enabled, func(c *ValidationContext) {
			c.ForwardKubeDNSToHost = ptr.To(false)
		}), user: masq},
		{name: "masquerade, forwardKubeDNSToHost unknown passes", ctx: enabled, user: masq},
		{name: "forwardKubeDNSToHost true without masquerade", ctx: with(enabled, func(c *ValidationContext) {
			c.ForwardKubeDNSToHost = ptr.To(true)
		})},

		{name: "gateway API without CRDs", ctx: with(replaced, func(c *ValidationContext) {
			c.GatewayAPICRDs = ptr.To(false)
		}), user: gw, wantErr: []string{"Gateway API CRDs"}},
		{name: "gateway API with CRDs", ctx: with(replaced, func(c *ValidationContext) {
			c.GatewayAPICRDs = ptr.To(true)
		}), user: gw},
		{name: "gateway API, CRDs unknown passes", ctx: replaced, user: gw},
		// Cilium's Gateway API prerequisites (docs.cilium.io/en/stable/network/servicemesh/gateway-api/gateway-api):
		// "Cilium must be configured with the kube-proxy replacement, using kubeProxyReplacement=true".
		{name: "gateway API while kube-proxy runs", ctx: enabled, user: gw,
			wantErr: []string{"gatewayAPI.enabled=true needs kube-proxy replacement", "spec.cni.kubeProxy=replaced"}},
		{name: "gateway API explicitly off while kube-proxy runs", ctx: enabled,
			user: map[string]any{keyGatewayAPI: map[string]any{keyEnabled: false}}},

		{name: "clustermesh API server with the default helm certificates", ctx: enabled, user: mesh(nil),
			wantErr: []string{"clustermesh.apiserver.tls.auto.method=cronJob", "which Ichor's Helm Job may not manage"}},
		{name: "clustermesh API server with cronJob certificates", ctx: enabled,
			user: mesh(map[string]any{"method": "cronJob"})},
		{name: "clustermesh API server with its own certificates", ctx: enabled,
			user: mesh(map[string]any{keyEnabled: false})},

		{name: "replaced, KubePrism off", ctx: with(replaced, func(c *ValidationContext) {
			c.KubePrism = &KubePrism{Enabled: false}
		}), wantErr: []string{"KubePrism"}},
		{name: "replaced, KubePrism on another port", ctx: with(replaced, func(c *ValidationContext) {
			c.KubePrism = &KubePrism{Enabled: true, Port: 7446}
		}), wantErr: []string{"7445"}},
		{name: "replaced, KubePrism on 7445", ctx: with(replaced, func(c *ValidationContext) {
			c.KubePrism = &KubePrism{Enabled: true, Port: 7445}
		})},
		{name: "replaced, KubePrism default port", ctx: with(replaced, func(c *ValidationContext) {
			c.KubePrism = &KubePrism{Enabled: true}
		})},
		{name: "enabled mode ignores KubePrism", ctx: with(enabled, func(c *ValidationContext) {
			c.KubePrism = &KubePrism{Enabled: false}
		})},

		// The chart's own template validation (validate.yaml) refuses these in the Helm Job; they are
		// refused at admission instead.
		{name: "cluster.id without cluster.name", ctx: enabled,
			user:    map[string]any{"cluster": map[string]any{"id": 5}},
			wantErr: []string{"cluster name is invalid", "cluster.id != 0"}},
		{name: "valid cluster name and id", ctx: enabled,
			user: map[string]any{"cluster": map[string]any{"name": "east", "id": 5}}},
		{name: "a ServiceMonitor passes: the CRD is the Job's to find", ctx: enabled,
			user: map[string]any{"prometheus": map[string]any{keyEnabled: true,
				"serviceMonitor": map[string]any{keyEnabled: true}}}},

		{name: "all problems are reported together", ctx: with(enabled, func(c *ValidationContext) {
			c.ForwardKubeDNSToHost = ptr.To(true)
		}), user: map[string]any{keyBPF: map[string]any{"masquerade": true}, keyIPAM: map[string]any{keyMode: "x"}},
			wantErr: []string{"ipam.mode", "forwardKubeDNSToHost"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateValues(tc.user, tc.ctx)
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("ValidateValues() = %v, want nil", err)
				}

				return
			}

			if err == nil {
				t.Fatalf("ValidateValues() = nil, want error containing %q", tc.wantErr)
			}

			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
		})
	}
}

func TestEffectiveValues_MergePrecedence(t *testing.T) {
	user := map[string]any{
		"hubble": map[string]any{keyEnabled: false, "tls": map[string]any{"auto": map[string]any{"method": "helm"}}},
		keyDebug: map[string]any{keyEnabled: true},
	}

	got, err := EffectiveValues(user, cni.KubeProxyReplaced, tunnelGeneve, 0)
	if err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]any{
		"hubble.enabled":           false,  // user over Ichor default
		"hubble.tls.auto.method":   "helm", // user over Ichor default, nested
		"hubble.tls.auto.enabled":  true,   // Ichor default kept where the user is silent
		"debug.enabled":            true,   // user-only key
		"kubeProxyReplacement":     true,   // owned
		"k8sServiceHost":           "localhost",
		"tunnelProtocol":           tunnelGeneve,
		"ipam.mode":                "kubernetes",
		"cgroup.autoMount.enabled": false,
		"cgroup.hostRoot":          "/sys/fs/cgroup",
	} {
		v, ok := values.Get(got, path)
		if !ok || v != want {
			t.Errorf("%s = %v (present %t), want %v", path, v, ok, want)
		}
	}

	if _, err := EffectiveValues(map[string]any{"tunnelProtocol": "vxlan"}, cni.KubeProxyEnabled, "", 0); err == nil ||
		!strings.Contains(err.Error(), "tunnelProtocol") {
		t.Errorf("owned key error = %v, want one naming tunnelProtocol", err)
	}

	// ADR 0159: refused in both modes, so kube-proxy-enabled clusters cannot set the KubePrism address.
	if _, err := EffectiveValues(map[string]any{keyServiceHost: "10.0.0.1"}, cni.KubeProxyEnabled, "", 0); err == nil ||
		!strings.Contains(err.Error(), keyServiceHost) {
		t.Errorf("enabled mode k8sServiceHost error = %v, want one naming k8sServiceHost", err)
	}
}

// TestBootstrapManifest_EmptyUserValuesAreByteIdentical pins the output of
// the pre-values-engine renderer (testdata/*.golden.yaml, captured before the
// change) for empty and nil user values.
func TestBootstrapManifest_EmptyUserValuesAreByteIdentical(t *testing.T) {
	for _, tunnel := range []struct{ arg, file string }{{"", "default"}, {tunnelGeneve, tunnelGeneve}} {
		for _, mode := range []cni.KubeProxyMode{cni.KubeProxyEnabled, cni.KubeProxyReplaced} {
			want, err := os.ReadFile("testdata/bootstrap-" + string(mode) + "-tunnel-" + tunnel.file + ".golden.yaml")
			if err != nil {
				t.Fatal(err)
			}

			p, err := New().WithTunnel(tunnel.arg)
			if err != nil {
				t.Fatal(err)
			}

			for name, user := range map[string]map[string]any{"nil": nil, "empty": {}} {
				got, err := p.WithValues(user).BootstrapManifest(mode)
				if err != nil {
					t.Fatal(err)
				}

				if string(got) != string(want) {
					t.Errorf("%s/%s/%s user values: output differs from golden", mode, tunnel.file, name)
				}
			}
		}
	}
}

func TestBootstrapManifest_UserValuesAreApplied(t *testing.T) {
	base, err := New().BootstrapManifest(cni.KubeProxyEnabled)
	if err != nil {
		t.Fatal(err)
	}

	got, err := New().WithValues(debugOn).BootstrapManifest(cni.KubeProxyEnabled)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) == string(base) {
		t.Error("user value debug.enabled=true did not change the output")
	}

	if _, err := New().WithValues(map[string]any{keyIPAM: map[string]any{keyMode: "x"}}).
		BootstrapManifest(cni.KubeProxyEnabled); err == nil {
		t.Error("BootstrapManifest accepted an owned key")
	}
}

// BenchmarkValidateValues is the admission and pre-Job validation. The defaults case runs the schema, owned,
// unknown and size checks on Ichor's own values; the user case also renders the chart (templateCheck), which
// dominates its cost and is measured separately from the schema work.
func BenchmarkValidateValues(b *testing.B) {
	ctx := ValidationContext{KubeProxy: cni.KubeProxyReplaced, Tunnel: tunnelGeneve}

	for _, tc := range []struct {
		name string
		user map[string]any
	}{
		{"defaults", nil},
		{"user", debugOn},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				if err := ValidateValues(tc.user, ctx); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

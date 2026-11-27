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
	"encoding/base64"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"k8s.io/utils/ptr"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/values"
)

const (
	apiHost       = "10.0.0.1"
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
		{name: "owned top-level key", ctx: replaced, user: map[string]any{keyServicePort: 6443},
			wantErr: []string{"k8sServicePort is set by Ichor", "because spec.cni.kubeProxy is replaced"}},
		{name: "KubePrism address is the operator's when kube-proxy is enabled", ctx: enabled,
			user: map[string]any{keyServiceHost: apiHost, keyServicePort: 6443}},
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
			c.GatewayAPIRelease = gatewayAPIMinimum
		}), user: gw},
		{name: "gateway API below the Cilium 1.20 minimum is refused", ctx: with(replaced, func(c *ValidationContext) {
			c.GatewayAPICRDs = ptr.To(true)
			c.GatewayAPIRelease = "v1.5.1"
		}), user: gw, wantErr: []string{"v1.5.1", gatewayAPIMinimum}},
		{name: "gateway API at the Cilium 1.20 minimum passes", ctx: with(replaced, func(c *ValidationContext) {
			c.GatewayAPICRDs = ptr.To(true)
			c.GatewayAPIRelease = gatewayAPIMinimum
		}), user: gw},
		{name: "gateway API at a later release passes", ctx: with(replaced, func(c *ValidationContext) {
			c.GatewayAPICRDs = ptr.To(true)
			c.GatewayAPIRelease = "v1.6.3"
		}), user: gw},
		{name: "gateway API CRD recording no release is refused", ctx: with(replaced, func(c *ValidationContext) {
			c.GatewayAPICRDs = ptr.To(true)
		}), user: gw, wantErr: []string{"bundle-version", gatewayAPIMinimum}},
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

	// ADR 0159: with kube-proxy enabled Ichor sets no KubePrism address, so the operator's reaches the values.
	api := map[string]any{keyServiceHost: apiHost, keyServicePort: 6443}

	enabledVals, err := EffectiveValues(api, cni.KubeProxyEnabled, "", 0)
	if err != nil {
		t.Fatalf("enabled mode refused the operator's API address: %v", err)
	}

	for path, want := range api {
		if v, _ := values.Get(enabledVals, path); fmt.Sprint(v) != fmt.Sprint(want) { // JSON makes 6443 a float64
			t.Errorf("enabled mode %s = %v, want %v", path, v, want)
		}
	}

	// Replaced, Ichor sets both, so a user value is refused, and the reason is true of that mode.
	for path := range api {
		_, err := EffectiveValues(map[string]any{path: api[path]}, cni.KubeProxyReplaced, "", 0)
		if err == nil || !strings.Contains(err.Error(), path+" is set by Ichor") ||
			!strings.Contains(err.Error(), "because spec.cni.kubeProxy is replaced") {
			t.Errorf("replaced mode %s error = %v, want one naming the key and that kube-proxy is replaced", path, err)
		}
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

const hubbleServerKey = "hubble.tls.server.key"

// keyAt is user values that set the private key at path.
func keyAt(path string) map[string]any {
	user := map[string]any{}
	values.Set(user, path, "c2VjcmV0")

	return user
}

// The private keys come from the chart, so a chart that gains one is covered by itself; this pins today's six.
func TestPrivateKeyPathsAreDerivedFromTheChart(t *testing.T) {
	got, err := PrivateKeyPaths()
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"hubble.metrics.tls.server.key", "hubble.relay.tls.client.key", "hubble.relay.tls.server.key",
		"hubble.tls.server.key", "hubble.ui.tls.client.key", "tls.ca.key",
	}
	if !slices.Equal(got, want) {
		t.Errorf("PrivateKeyPaths = %v, want %v (a chart upgrade that adds a key leaf: confirm it is key material)",
			got, want)
	}
}

func TestValidateValuesRefusesEmbeddedPrivateKeys(t *testing.T) {
	ctx := ValidationContext{KubeProxy: cni.KubeProxyEnabled}

	paths, err := PrivateKeyPaths()
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range paths {
		err := ValidateValues(keyAt(p), ctx)
		if err == nil || !strings.Contains(err.Error(), p) || !strings.Contains(err.Error(), "never embedded") {
			t.Errorf("%s: err = %v, want a refusal naming the path", p, err)
		}
	}

	if err := ValidateValues(keyAt("tls.ca.key"), ctx); err == nil ||
		!strings.Contains(err.Error(), "hubble.tls.auto.method") {
		t.Errorf("err = %v, want the chart-generation alternative", err)
	}

	if err := ValidateValues(keyAt(hubbleServerKey), ctx); err == nil ||
		!strings.Contains(err.Error(), "hubble.tls.server.existingSecret") {
		t.Errorf("err = %v, want the existingSecret alternative", err)
	}

	// An empty key, and an unrelated value, pass.
	empty := keyAt("tls.ca.key")
	values.Set(empty, "tls.ca.key", "")

	if err := ValidateValues(empty, ctx); err != nil {
		t.Errorf("empty key: %v", err)
	}

	if err := ValidateValues(debugOn, ctx); err != nil {
		t.Errorf("unrelated value: %v", err)
	}

	// A kept key (an object that already carries it) passes.
	kept := ValidationContext{KubeProxy: cni.KubeProxyEnabled, KeptPrivateKeys: []string{"tls.ca.key"}}
	if err := ValidateValues(keyAt("tls.ca.key"), kept); err != nil {
		t.Errorf("kept key: %v", err)
	}
}

const (
	peerTLSKey = "tls"
	peerCertK  = "cert"
	peerCert   = "Y2VydA=="
)

func TestClusterMeshPeerTLSKeyIsRefused(t *testing.T) {
	ctx := ValidationContext{KubeProxy: cni.KubeProxyEnabled}
	pem := "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"

	mk := func(clusters any) map[string]any {
		return map[string]any{"clustermesh": map[string]any{"config": map[string]any{"clusters": clusters}}}
	}
	peer := func(peerTLS map[string]any) map[string]any {
		return map[string]any{"id": "b", peerTLSKey: peerTLS}
	}

	for name, u := range map[string]map[string]any{
		"list": mk([]any{peer(map[string]any{peerCertK: peerCert, "key": "c2VjcmV0", "caCert": peerCert})}),
		"dict": mk(map[string]any{"b": map[string]any{
			peerTLSKey: map[string]any{peerCertK: peerCert, "key": "c2VjcmV0"}}}),
		"pem raw":    mk([]any{map[string]any{"id": "b", "note": pem}}),
		"pem base64": mk([]any{map[string]any{"id": "b", "note": base64.StdEncoding.EncodeToString([]byte(pem))}}),
	} {
		err := ValidateValues(u, ctx)
		if err == nil || !strings.Contains(err.Error(), "clustermesh.config.clusters") {
			t.Errorf("%s: err = %v, want a refusal naming clustermesh.config.clusters", name, err)
		} else if strings.HasPrefix(name, "pem") == strings.Contains(err.Error(), "tls.key") {
			t.Errorf("%s: err = %v, tls.key named only for the key leaf", name, err)
		}

		if err != nil {
			want := "clustermesh.apiserver.tls.auto.enabled"
			if strings.HasPrefix(name, "pem") {
				want = "check the chart for the Secret option"
			}

			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: err = %v, want the advice %q", name, err, want)
			}
		}

		if err != nil && (strings.Contains(err.Error(), "c2VjcmV0") || strings.Contains(err.Error(), "AAAA")) {
			t.Errorf("%s: the error echoes key bytes: %v", name, err)
		}

		if err := checkTakeoverKeys(u); err == nil {
			t.Errorf("%s: the takeover preflight accepted it", name)
		}
	}

	// A cert with no key, and a peer that names a Secret, pass.
	for name, u := range map[string]map[string]any{
		"cert only":  mk([]any{peer(map[string]any{peerCertK: peerCert})}),
		"secret ref": mk([]any{map[string]any{"id": "b", peerTLSKey: map[string]any{"existingSecret": "peer-b"}}}),
	} {
		if err := ValidateValues(u, ctx); err != nil {
			t.Errorf("%s: %v", name, err)
		}

		if err := checkTakeoverKeys(u); err != nil {
			t.Errorf("%s: takeover: %v", name, err)
		}
	}
}

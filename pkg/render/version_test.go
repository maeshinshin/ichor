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

package render

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// pinnedVersion and pinnedFingerprint hold Version and the output fingerprint it was last bumped
// for. They move together: a render change updates the fingerprint and bumps Version.
const (
	pinnedVersion     = 2
	pinnedFingerprint = "sha256:6a2c70d35198067f560b518c4256a04c76b90bd5dbeee80fa0bf5b7b73f7ce36"
)

// leaves flattens a YAML stream into "doc<n>.path" -> value.
func leaves(t *testing.T, raw []byte) map[string]string {
	t.Helper()

	out := map[string]string{}
	dec := yaml.NewDecoder(bytes.NewReader(raw))

	for n := 0; ; n++ {
		var doc any

		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return out
			}

			t.Fatalf("decoding rendered configuration: %v", err)
		}

		var walk func(path string, v any)
		walk = func(path string, v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, c := range x {
					walk(path+"."+k, c)
				}
			case []any:
				for i, c := range x {
					walk(fmt.Sprintf("%s[%d]", path, i), c)
				}
			default:
				out[fmt.Sprintf("doc%d%s", n, path)] = fmt.Sprint(x)
			}
		}

		walk("", doc)
	}
}

// fingerprintCluster sets one field per input-dependent branch of the renderer, so a change to any
// of them moves the fingerprint. Feature -> field:
//   - registry mirror auth keyed by endpoint host: Spec.Registries + SystemInputs.RegistryAuth
//   - pod security exemptions (dedupe, kube-system): Spec.Security.PodSecurity.ExemptNamespaces
//   - firewall rules, extra sources, kubespan, allow rule: Spec.Security.Firewall
//   - firewall ports from the effective Cilium values: Spec.CNI.Cilium.Values tunnelPort and clusterHealthPort
//   - Cilium WireGuard port: Spec.Security.WireGuard
//   - kube-proxy replaced: Spec.CNI.KubeProxy
func fingerprintCluster() *ichorv1alpha1.TalosCluster {
	c := testCluster("false")
	c.Spec.Registries = []ichorv1alpha1.TalosClusterRegistry{{
		Host: "docker.io", Endpoints: []string{"http://192.168.0.8:5000"},
		AuthSecretRef: &corev1.LocalObjectReference{Name: "creds"},
	}}
	c.Spec.CNI.KubeProxy = "replaced"
	c.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{Values: &apiextensionsv1.JSON{Raw: []byte(`{"tunnelPort":8473,"clusterHealthPort":4241}`)}}
	c.Spec.Security = &ichorv1alpha1.TalosClusterSecurity{
		PodSecurity: &ichorv1alpha1.TalosClusterPodSecurity{
			Enforce: "restricted", ExemptNamespaces: []string{"kube-system", "monitoring", "monitoring"},
		},
		WireGuard: &ichorv1alpha1.TalosClusterWireGuard{Enabled: true},
		Firewall: &ichorv1alpha1.TalosClusterFirewall{
			Enabled: true, ControllerCIDRs: []string{"192.0.2.0/24"}, NodeCIDRs: []string{"10.0.0.0/24"},
			TalosAPICIDRs: []string{"172.16.0.0/12"}, APIServerCIDRs: []string{"203.0.113.0/24"}, KubeSpan: true,
			Allow: []ichorv1alpha1.TalosClusterFirewallRule{
				{Name: "ingress", Protocol: "tcp", Ports: []string{"80", "8000-8100"}, CIDRs: []string{"0.0.0.0/0"}},
			},
		},
	}

	return c
}

// renderFingerprint renders the same inputs under two different secrets bundles and hashes every
// key path, keeping a value only where both renders agree -- so secret material drops out and
// anything Ichor itself decides stays in. Each role renders fingerprintCluster on the current
// contract; a third render uses the v1.13 contract with a stored time-servers document, which
// takes the branches a contract without the Talos 1.14 documents takes.
func renderFingerprint(t *testing.T) string {
	t.Helper()

	newBundle := func(contract, shift string) *secrets.Bundle {
		vc, err := coreconfig.ParseContractFromVersion(contract)
		if err != nil {
			t.Fatalf("ParseContractFromVersion: %v", err)
		}

		d, _ := time.ParseDuration(shift)

		b, err := secrets.NewBundle(secrets.NewFixedClock(time.Now().Add(d)), vc)
		if err != nil {
			t.Fatalf("NewBundle: %v", err)
		}

		return b
	}

	legacy := fingerprintCluster()
	legacy.Spec.VersionContract = "v1.13"
	legacy.Spec.Security = nil // the hardening options need v1.14

	cases := []struct {
		name, role string
		cluster    *ichorv1alpha1.TalosCluster
		a, b       *secrets.Bundle
		patches    []ichorv1alpha1.TalosConfigPatch
	}{
		{"cp", roleControlPlane, fingerprintCluster(), testBundle(t), newBundle(versionContract, "1h"),
			[]ichorv1alpha1.TalosConfigPatch{sysctlPatch("p", layerCluster, 0, nil, "1")}},
		{"worker", roleWorker, fingerprintCluster(), testBundle(t), newBundle(versionContract, "1h"),
			[]ichorv1alpha1.TalosConfigPatch{sysctlPatch("p", layerCluster, 0, nil, "1")}},
		{"legacy-contract", roleControlPlane, legacy, newBundle("v1.13", "0h"), newBundle("v1.13", "1h"),
			[]ichorv1alpha1.TalosConfigPatch{storedDocumentsPatch(t, timeServersDocument)}},
	}

	var lines []string

	for _, tc := range cases {
		m := testMachine("node-"+tc.role, tc.role, map[string]string{labelEnv: "fingerprint"})
		m.Spec.Hostname = "node-" + tc.role
		sys := &SystemInputs{RegistryAuth: map[string]RegistryAuth{"docker.io": {Password: "pw"}}}

		a := leaves(t, []byte(mustRenderSys(t, tc.cluster, m, tc.a, tc.patches, sys)))
		b := leaves(t, []byte(mustRenderSys(t, tc.cluster, m, tc.b, tc.patches, sys)))

		for k, v := range a {
			if b[k] != v {
				v = "<varies>"
			}

			lines = append(lines, tc.name+":"+k+"="+v)
		}
	}

	slices.Sort(lines)

	return Hash([]byte(strings.Join(lines, "\n")))
}

// TestRenderVersionMatchesOutput fails when Render's output for fixed inputs changes without
// Version being bumped (and the pin above updated). It is what keeps an Ichor upgrade that
// changes rendering from being reported as node drift.
func TestRenderVersionMatchesOutput(t *testing.T) {
	got := renderFingerprint(t)

	if Version != pinnedVersion || got != pinnedFingerprint {
		t.Fatalf("render output or Version changed: Version=%d (pinned %d), fingerprint=%s (pinned %s).\n"+
			"If the output changed on purpose, bump Version in version.go and set pinnedVersion and "+
			"pinnedFingerprint here; if Version alone changed, set the pin to match.",
			Version, pinnedVersion, got, pinnedFingerprint)
	}
}

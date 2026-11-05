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
	"net/netip"
	"slices"
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

const (
	fwController = "192.168.10.0/24"
	fwNodes      = "10.0.0.0/24"
)

func secCluster(s *ichorv1alpha1.TalosClusterSecurity) *ichorv1alpha1.TalosCluster {
	c := testCluster("true")
	c.Spec.Security = s

	return c
}

func fwCluster(mut func(*ichorv1alpha1.TalosClusterFirewall)) *ichorv1alpha1.TalosCluster {
	fw := &ichorv1alpha1.TalosClusterFirewall{
		Enabled: true, ControllerCIDRs: []string{fwController}, NodeCIDRs: []string{fwNodes},
	}
	if mut != nil {
		mut(fw)
	}

	return secCluster(&ichorv1alpha1.TalosClusterSecurity{Firewall: fw})
}

// rules returns the names of every NetworkRuleConfig in out.
func ruleNames(out string) []string {
	var names []string

	for doc := range strings.SplitSeq(out, "\n---\n") {
		if !strings.Contains(doc, metricsRuleKind) {
			continue
		}

		for line := range strings.SplitSeq(doc, "\n") {
			if n, ok := strings.CutPrefix(line, "name: "); ok {
				names = append(names, n)
			}
		}
	}

	slices.Sort(names)

	return names
}

func ruleDoc(out, name string) string {
	return section(out, "name: "+name+"\n")
}

// TestSecurityDefaultOff proves ADR 0108's rule: a cluster without the options renders the same
// bytes as one with every option present but off.
func TestSecurityDefaultOff(t *testing.T) {
	bundle := testBundle(t)

	for _, role := range []string{roleControlPlane, roleWorker} {
		m := testMachine("n", role, nil)
		base := mustRender(t, testCluster("true"), m, bundle, nil)

		for name, s := range map[string]*ichorv1alpha1.TalosClusterSecurity{
			"empty":    {},
			"disabled": {Firewall: &ichorv1alpha1.TalosClusterFirewall{}, WireGuard: &ichorv1alpha1.TalosClusterWireGuard{}},
		} {
			if got := mustRender(t, secCluster(s), m, bundle, nil); got != base {
				t.Errorf("%s/%s: render differs from a cluster without spec.security", role, name)
			}
		}

		requireNone(t, base, "NetworkDefaultActionConfig", "ichor-fw-", "omitStages", "luks2")
	}
}

func TestFirewall(t *testing.T) {
	bundle := testBundle(t)
	cp := testMachine("cp", roleControlPlane, nil)
	worker := testMachine("w", roleWorker, nil)

	t.Run("control plane gets block and the full rule set", func(t *testing.T) {
		out := mustRender(t, fwCluster(nil), cp, bundle, nil)

		if !strings.Contains(section(out, metricsDefaultAct), "ingress: block") {
			t.Fatalf("default action is not block:\n%s", out)
		}

		want := []string{
			"ichor-fw-apid", "ichor-fw-cilium-health", "ichor-fw-cilium-tunnel", "ichor-fw-etcd",
			"ichor-fw-kubelet", "ichor-fw-kubernetes-api", "ichor-fw-trustd",
		}
		got := ruleNames(out)

		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("rules = %v, want %v", got, want)
		}

		requireAll(t, ruleDoc(out, "ichor-fw-apid"), fwController, fwNodes, "50000")
		requireAll(t, ruleDoc(out, "ichor-fw-kubernetes-api"), fwController, fwNodes, "10.244.0.0/16", "10.96.0.0/12", "6443")

		etcd := ruleDoc(out, "ichor-fw-etcd")
		requireAll(t, etcd, "2379-2380")
		requireNone(t, etcd, fwController)

		requireAll(t, ruleDoc(out, "ichor-fw-cilium-tunnel"), "8472", "protocol: udp")
	})

	t.Run("a worker gets no etcd, trustd or apiserver rule", func(t *testing.T) {
		got := strings.Join(ruleNames(mustRender(t, fwCluster(nil), worker, bundle, nil)), ",")
		if got != "ichor-fw-apid,ichor-fw-cilium-health,ichor-fw-cilium-tunnel,ichor-fw-kubelet" {
			t.Fatalf("worker rules = %s", got)
		}
	})

	t.Run("geneve moves the tunnel port", func(t *testing.T) {
		c := fwCluster(nil)
		c.Spec.CNI.Tunnel = "geneve"

		requireAll(t, ruleDoc(mustRender(t, c, worker, bundle, nil), "ichor-fw-cilium-tunnel"), "6081")
	})

	t.Run("control planes narrow etcd and kubelet", func(t *testing.T) {
		out := mustRender(t, fwCluster(func(f *ichorv1alpha1.TalosClusterFirewall) {
			f.ControlPlaneCIDRs = []string{"10.0.0.10/32"}
		}), cp, bundle, nil)

		etcd := ruleDoc(out, "ichor-fw-etcd")
		requireAll(t, etcd, "10.0.0.10/32")
		requireNone(t, etcd, fwNodes)
	})

	t.Run("wireguard, kubespan, extra sources and allow rules", func(t *testing.T) {
		c := fwCluster(func(f *ichorv1alpha1.TalosClusterFirewall) {
			f.KubeSpan = true
			f.TalosAPICIDRs = []string{"172.16.0.0/12"}
			f.APIServerCIDRs = []string{"203.0.113.0/24"}
			f.Allow = []ichorv1alpha1.TalosClusterFirewallRule{
				{Name: "ingress", Protocol: "tcp", Ports: []string{"80", "443", "8000-8100"}, CIDRs: []string{"0.0.0.0/0"}},
			}
		})
		c.Spec.Security.WireGuard = &ichorv1alpha1.TalosClusterWireGuard{Enabled: true}

		out := mustRender(t, c, cp, bundle, nil)

		for name, want := range map[string]string{
			"ichor-fw-cilium-wireguard": "51871", "ichor-fw-kubespan": "51820",
			"ichor-fw-apid": "172.16.0.0/12", "ichor-fw-kubernetes-api": "203.0.113.0/24",
			"ichor-fw-allow-ingress": "8000-8100",
		} {
			requireAll(t, ruleDoc(out, name), want)
		}
	})

	t.Run("the metrics rule coexists", func(t *testing.T) {
		c := fwCluster(nil)
		c.Spec.ControlPlaneMetrics = &ichorv1alpha1.TalosClusterControlPlaneMetrics{
			Enabled: true, AllowedCIDRs: []string{metricsCIDR},
		}

		if r := ruleDoc(mustRender(t, c, cp, bundle, nil), ruleNameControlPlaneMetrics); r == "" {
			t.Fatal("metrics rule missing")
		}
	})

	t.Run("enabled without sources is an error", func(t *testing.T) {
		for name, mut := range map[string]func(*ichorv1alpha1.TalosClusterFirewall){
			"controller": func(f *ichorv1alpha1.TalosClusterFirewall) { f.ControllerCIDRs = nil },
			"nodes":      func(f *ichorv1alpha1.TalosClusterFirewall) { f.NodeCIDRs = nil },
			"bad cidr":   func(f *ichorv1alpha1.TalosClusterFirewall) { f.NodeCIDRs = []string{"nope"} },
			"bad port": func(f *ichorv1alpha1.TalosClusterFirewall) {
				f.Allow = []ichorv1alpha1.TalosClusterFirewallRule{
					{Name: "x", Protocol: "tcp", Ports: []string{"0"}, CIDRs: []string{fwNodes}},
				}
			},
		} {
			if _, _, err := Render(fwCluster(mut), cp, bundle, nil, nil); err == nil {
				t.Errorf("%s: want an error", name)
			}
		}
	})

	t.Run("controller source preflight", func(t *testing.T) {
		c := fwCluster(nil)

		if err := ControllerSourceAllowed(c, netip.MustParseAddr("192.168.10.7")); err != nil {
			t.Errorf("inside: %v", err)
		}

		if err := ControllerSourceAllowed(c, netip.MustParseAddr("10.9.9.9")); err == nil {
			t.Error("outside must be refused")
		}

		if err := ControllerSourceAllowed(testCluster("true"), netip.MustParseAddr("10.9.9.9")); err != nil {
			t.Errorf("firewall off must not check: %v", err)
		}
	})
}

func TestPodSecurityAndAudit(t *testing.T) {
	bundle := testBundle(t)
	cp := testMachine("cp", roleControlPlane, nil)
	worker := testMachine("w", roleWorker, nil)

	ps := secCluster(&ichorv1alpha1.TalosClusterSecurity{PodSecurity: &ichorv1alpha1.TalosClusterPodSecurity{
		Enforce: "restricted", ExemptNamespaces: []string{"rook-ceph", "monitoring"},
	}})

	t.Run("pod security overrides only what is set and keeps kube-system", func(t *testing.T) {
		doc := section(mustRender(t, ps, cp, bundle, nil), "kind: KubeAdmissionControlConfig")
		requireAll(t, doc,
			"enforce: restricted", "audit: restricted", "warn: restricted", "- kube-system", "- monitoring", "- rook-ceph")
		requireNone(t, doc, "enforce: baseline")
	})

	t.Run("a worker is untouched", func(t *testing.T) {
		if mustRender(t, ps, worker, bundle, nil) != mustRender(t, testCluster("true"), worker, bundle, nil) {
			t.Fatal("worker render changed")
		}
	})

	presets := []string{
		ichorv1alpha1.AuditPolicyMetadata, ichorv1alpha1.AuditPolicyRBACChange, ichorv1alpha1.AuditPolicyWrites,
	}

	for _, policy := range presets {
		t.Run("audit "+policy, func(t *testing.T) {
			c := secCluster(&ichorv1alpha1.TalosClusterSecurity{Audit: &ichorv1alpha1.TalosClusterAudit{Policy: policy}})
			doc := section(mustRender(t, c, cp, bundle, nil), "kind: KubeAuditPolicyConfig")

			if strings.Count(doc, "level:") == 0 {
				t.Fatalf("no rules:\n%s", doc)
			}

			switch policy {
			case ichorv1alpha1.AuditPolicyMetadata:
				requireNone(t, doc, "level: Request")
			case ichorv1alpha1.AuditPolicyRBACChange:
				requireAll(t, doc, "RequestResponse", "rbac.authorization.k8s.io")
			case ichorv1alpha1.AuditPolicyWrites:
				requireAll(t, doc, "level: Request\n")
			}

			// Secrets stay at Metadata and the rule comes first.
			if policy != ichorv1alpha1.AuditPolicyMetadata {
				if i, j := strings.Index(doc, "secrets"), strings.Index(doc, "level: Request"); i < 0 || j < 0 || i > j {
					t.Errorf("secrets rule must precede body-logging rules:\n%s", doc)
				}
			}
		})
	}

	t.Run("a pre-1.14 contract is refused, not silently ignored", func(t *testing.T) {
		c := ps.DeepCopy()
		c.Spec.VersionContract = contractV112

		if _, _, err := Render(c, cp, bundle, nil, nil); err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestDiskEncryption(t *testing.T) {
	bundle := testBundle(t)
	cp := testMachine("cp", roleControlPlane, nil)

	enc := func(d ichorv1alpha1.TalosClusterDiskEncryption) *ichorv1alpha1.TalosCluster {
		return secCluster(&ichorv1alpha1.TalosClusterSecurity{DiskEncryption: &d})
	}

	t.Run("tpm renders STATE and EPHEMERAL, EPHEMERAL locked to STATE", func(t *testing.T) {
		yes := true
		tpm := ichorv1alpha1.TalosClusterDiskEncryption{Type: "tpm", RequireSecureBoot: &yes}
		out := mustRender(t, enc(tpm), cp, bundle, nil)

		state, eph := section(out, "name: STATE\n"), section(out, "name: EPHEMERAL\n")

		for _, doc := range []string{state, eph} {
			requireAll(t, doc, "provider: luks2", "tpm:", "checkSecurebootStatusOnEnroll: true")
		}

		requireNone(t, state, "lockToState")
		requireAll(t, eph, "lockToState: true")
	})

	t.Run("nodeID and kms", func(t *testing.T) {
		nodeID := ichorv1alpha1.TalosClusterDiskEncryption{Type: "nodeID"}
		requireAll(t, mustRender(t, enc(nodeID), cp, bundle, nil), "nodeID: {}")

		kms := ichorv1alpha1.TalosClusterDiskEncryption{Type: "kms", KMSEndpoint: "https://kms.example.com:4443"}
		requireAll(t, mustRender(t, enc(kms), cp, bundle, nil), "endpoint: https://kms.example.com:4443")
	})

	t.Run("a plaintext KMS endpoint is refused", func(t *testing.T) {
		for _, ep := range []string{"", "kms.example.com:4443", "grpc://kms.example.com:4443", "http://kms.example.com"} {
			c := enc(ichorv1alpha1.TalosClusterDiskEncryption{Type: "kms", KMSEndpoint: ep})
			if _, _, err := Render(c, cp, bundle, nil, nil); err == nil {
				t.Errorf("%q: want an error", ep)
			}
		}
	})
}

// kubernetesTalosAPIAccess is valid on control planes only; a cluster-layer patch that enables it
// renders on them and is left out of workers instead of failing their render.
func TestTalosAPIAccessRendersOnControlPlanesOnly(t *testing.T) {
	bundle := testBundle(t)
	on := true

	patch := configPatch("api-access", &typedconfig.Config{
		ConfigVersion: configVersionV1Alpha1,
		MachineConfig: &typedconfig.MachineConfig{MachineFeatures: &typedconfig.FeaturesConfig{
			KubernetesTalosAPIAccessConfig: &typedconfig.KubernetesTalosAPIAccessConfig{
				AccessEnabled: &on, AccessAllowedRoles: []string{"os:reader"},
				AccessAllowedKubernetesNamespaces: []string{"ops"},
			},
		}},
	})
	patches := []ichorv1alpha1.TalosConfigPatch{patch}

	cp := mustRender(t, testCluster("true"), testMachine("cp", roleControlPlane, nil), bundle, patches)
	requireAll(t, cp, "kubernetesTalosAPIAccess")

	requireNone(t, mustRender(t, testCluster("true"), testMachine("w", roleWorker, nil), bundle, patches),
		"kubernetesTalosAPIAccess")

	if patches[0].Spec.Documents[0].Config.MachineConfig.MachineFeatures.KubernetesTalosAPIAccessConfig == nil {
		t.Error("the caller's patch was modified")
	}
}

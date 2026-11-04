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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

const (
	metricsCIDR       = "10.20.0.0/16"
	metricsRuleKind   = "kind: NetworkRuleConfig"
	metricsBindAll    = "bind-address: 0.0.0.0"
	metricsEtcdArg    = "listen-metrics-urls: http://0.0.0.0:2381"
	metricsDefaultAct = "NetworkDefaultActionConfig"
)

func metricsCluster(enabled bool, cidrs ...string) *ichorv1alpha1.TalosCluster {
	c := testCluster("true")
	c.Spec.ControlPlaneMetrics = &ichorv1alpha1.TalosClusterControlPlaneMetrics{Enabled: enabled, AllowedCIDRs: cidrs}

	return c
}

// section returns the YAML document of out whose kind line is marker, or "".
func section(out, marker string) string {
	for doc := range strings.SplitSeq(out, "\n---\n") {
		if strings.Contains(doc, marker) {
			return doc
		}
	}

	return ""
}

// TestControlPlaneMetrics checks ADR 0069: enabled renders the three bind changes and one
// firewall rule that touches only the metrics ports, on control-plane nodes only.
func TestControlPlaneMetrics(t *testing.T) {
	bundle := testBundle(t)
	cp := testMachine("node-a", roleControlPlane, nil)
	worker := testMachine("node-w", roleWorker, nil)

	t.Run("enabled renders binds and a scoped rule on a control-plane node", func(t *testing.T) {
		out := mustRender(t, metricsCluster(true, metricsCIDR, "192.168.1.0/24"), cp, bundle, nil)

		for _, kind := range []string{"kind: KubeControllerManagerConfig", "kind: KubeSchedulerConfig"} {
			requireAll(t, section(out, kind), metricsBindAll)
		}

		requireAll(t, out, metricsEtcdArg)
		requireAll(t, section(out, metricsRuleKind), "protocol: tcp", "2381", "10257", "10259", metricsCIDR, "192.168.1.0/24")

		// The default action must stay Talos's own (accept): block would drop the API, etcd peers,
		// kubelet and the Talos API.
		requireNone(t, out, metricsDefaultAct)

		if strings.Count(out, metricsRuleKind) != 1 {
			t.Errorf("want exactly one rule document, got %d", strings.Count(out, metricsRuleKind))
		}
	})

	t.Run("the rule names no port besides the three metrics ports", func(t *testing.T) {
		out := mustRender(t, metricsCluster(true, metricsCIDR), cp, bundle, nil)
		requireNone(t, section(out, metricsRuleKind), "6443", "50000", "50001", "2379", "2380", "10250")
	})

	t.Run("a worker gets nothing", func(t *testing.T) {
		on := mustRender(t, metricsCluster(true, metricsCIDR), worker, bundle, nil)
		off := mustRender(t, testCluster("true"), worker, bundle, nil)

		if on != off {
			t.Fatalf("worker render changed:\n%s", on)
		}
	})

	t.Run("unset and disabled render exactly the same bytes", func(t *testing.T) {
		unset := mustRender(t, testCluster("true"), cp, bundle, nil)
		disabled := mustRender(t, metricsCluster(false, metricsCIDR), cp, bundle, nil)

		if unset != disabled {
			t.Fatal("enabled=false must not change the render")
		}

		requireNone(t, unset, metricsRuleKind, metricsBindAll, "listen-metrics-urls")
	})

	t.Run("enabled without a CIDR is an error, never an open port", func(t *testing.T) {
		if _, _, err := Render(metricsCluster(true), cp, bundle, nil, nil); err == nil {
			t.Fatal("want an error")
		}
	})

	t.Run("a pre-1.14 contract renders the legacy fields", func(t *testing.T) {
		c := metricsCluster(true, metricsCIDR)
		c.Spec.VersionContract = "v1.12"

		requireAll(t, mustRender(t, c, cp, bundle, nil), metricsBindAll, metricsEtcdArg, metricsRuleKind)
	})

	t.Run("a Config patch can still override etcd's argument (system layer)", func(t *testing.T) {
		patch := ichorv1alpha1.TalosConfigPatch{
			Namespace: testNamespace, Name: "narrow",
			Spec: ichorv1alpha1.TalosConfigPatchSpec{
				ClusterRef: corev1.LocalObjectReference{Name: testClusterName},
				Layer:      layerCluster,
				Documents: []ichorv1alpha1.TalosConfigPatchDocument{{
					Kind: kindConfig,
					Config: &typedconfig.Config{
						ConfigVersion: configVersionV1Alpha1,
						ClusterConfig: &typedconfig.ClusterConfig{EtcdConfig: &typedconfig.EtcdConfig{
							EtcdExtraArgs: map[string][]string{"listen-metrics-urls": {"http://127.0.0.1:2381"}},
						}},
					},
				}},
			},
		}

		out := mustRender(t, metricsCluster(true, metricsCIDR), cp, bundle, []ichorv1alpha1.TalosConfigPatch{patch})
		if !strings.Contains(out, "listen-metrics-urls: http://127.0.0.1:2381") || strings.Contains(out, metricsEtcdArg) {
			t.Fatalf("the patch must win over the system layer:\n%s", out)
		}
	})
}

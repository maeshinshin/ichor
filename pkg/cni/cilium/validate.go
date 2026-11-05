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
	"errors"
	"fmt"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/values"
)

// KubePrism is the cluster's Talos KubePrism setting
// (machine.features.kubePrism).
type KubePrism struct {
	Enabled bool
	// Port is the local port; 0 means Talos's default, 7445.
	Port int
}

// ValidationContext carries what the caller knows. A nil fact is unknown and
// its guard passes: the webhook cannot see the cluster. The controller
// re-checks with live facts before every Job (ADR 0099, decision 4; ADR 0129
// amendment 2026-11-05); internal/cnifacts builds them.
type ValidationContext struct {
	// KubeProxy and Tunnel select Ichor's own values (Tunnel: "", "vxlan",
	// "geneve").
	KubeProxy cni.KubeProxyMode
	Tunnel    string

	// GatewayAPICRDs is whether the Gateway API CRDs exist in the cluster.
	GatewayAPICRDs *bool
	// ForwardKubeDNSToHost is machine.features.hostDNS.forwardKubeDNSToHost
	// in the effective machine config.
	ForwardKubeDNSToHost *bool
	// KubePrism is the effective KubePrism setting.
	KubePrism *KubePrism
}

// ValidateValues is the one validation of user Cilium values (ADR 0099,
// decision 4). The TalosCluster webhook runs it with no facts, so only the
// chart schema, owned keys, unknown keys and the size cap apply there (ADR
// 0129); the Connect service adds the facts it can read for the three guards from
// the Talos guide (`ichor adopt` passes none), and so does the controller before
// every Helm Job (internal/cnifacts). It joins every problem it finds.
func ValidateValues(user map[string]any, c ValidationContext) error {
	// The control-plane count only sets a default (operator.replicas), which validation does not read.
	p, err := newPreset(c.KubeProxy, c.Tunnel, 0)
	if err != nil {
		return err
	}

	spec, err := valuesSpec()
	if err != nil {
		return err
	}

	defaults, owned, err := p.layers(c.KubeProxy)
	if err != nil {
		return err
	}

	effective := values.Effective(defaults, user, owned)

	if err := errors.Join(spec.Check(user, effective), guards(effective, c)); err != nil {
		return err
	}

	return templateCheck(user, effective)
}

// templateCheck renders the chart with the effective values, so the chart's own template validation
// (validate.yaml: a cluster name and id that do not go together, Hubble UI without Relay, ...) refuses at
// admission what would otherwise only fail the Helm Job again and again. It costs about 80 ms and 36 MB of
// garbage (measured, pkg/cni/cilium), so it runs only when the user set something: Ichor's own defaults
// are rendered by every bootstrap and test. Rendering is offline, so what the Job decides from the live
// cluster stays the Job's: monitoring.coreos.com/v1 is declared present (the chart refuses a ServiceMonitor
// without it), and values that make the chart look up a ConfigMap (k8sServiceHostRef) are not rendered.
func templateCheck(user, effective map[string]any) error {
	if len(user) == 0 {
		return nil
	}

	if ref, _ := values.Get(effective, "k8sServiceHostRef.name"); ref != nil && ref != "" {
		return nil
	}

	if _, err := renderChartWith(effective, "monitoring.coreos.com/v1"); err != nil {
		return fmt.Errorf("the chart refuses these values: %w", err)
	}

	return nil
}

func guards(effective map[string]any, c ValidationContext) error {
	var errs []error

	if on, _ := values.Get(effective, "bpf.masquerade"); on == true &&
		c.ForwardKubeDNSToHost != nil && *c.ForwardKubeDNSToHost {
		errs = append(errs, errors.New("bpf.masquerade=true needs machine.features.hostDNS.forwardKubeDNSToHost=false, "+
			"or CoreDNS breaks (Talos Cilium guide)"))
	}

	if on, _ := values.Get(effective, "gatewayAPI.enabled"); on == true &&
		c.GatewayAPICRDs != nil && !*c.GatewayAPICRDs {
		errs = append(errs, errors.New("gatewayAPI.enabled=true needs the Gateway API CRDs installed first"))
	}

	// Cilium's Gateway API prerequisites: "Cilium must be configured with the kube-proxy replacement, using
	// kubeProxyReplacement=true" (docs.cilium.io/en/stable/network/servicemesh/gateway-api/gateway-api, Cilium
	// 1.20, retrieved 2026-11-06); the operator otherwise logs "Gateway API support requires
	// kube-proxy-replacement enabled" and never accepts the GatewayClass (hardware run I). kubeProxyReplacement
	// is owned (spec.cni.kubeProxy), so the mode, not the values, decides.
	if on, _ := values.Get(effective, "gatewayAPI.enabled"); on == true && c.KubeProxy != cni.KubeProxyReplaced {
		errs = append(errs, errors.New("gatewayAPI.enabled=true needs kube-proxy replacement: Cilium's Gateway API "+
			"requires kubeProxyReplacement=true, so set spec.cni.kubeProxy=replaced first (the kube-proxy switch)"))
	}

	// The chart's default, "helm", renders the API server's certificates as Secrets in kube-system, which
	// the Helm Job's ServiceAccount cannot manage (ADR 0099 amendment); Ichor keeps cronJob, as for Hubble.
	if on, _ := values.Get(effective, "clustermesh.useAPIServer"); on == true {
		auto, _ := values.Get(effective, "clustermesh.apiserver.tls.auto.enabled")
		method, _ := values.Get(effective, "clustermesh.apiserver.tls.auto.method")

		if auto != false && method != "cronJob" {
			errs = append(errs, errors.New("clustermesh.useAPIServer=true needs clustermesh.apiserver.tls.auto.method=cronJob "+
				"(or clustermesh.apiserver.tls.auto.enabled=false with your own certificates): the chart's default creates "+
				"Secrets in kube-system, which Ichor's Helm Job may not manage"))
		}
	}

	if c.KubeProxy == cni.KubeProxyReplaced && c.KubePrism != nil {
		port := c.KubePrism.Port
		if port == 0 {
			port = kubePrismPort
		}

		if !c.KubePrism.Enabled || port != kubePrismPort {
			errs = append(errs, errors.New("kubeProxy=replaced needs KubePrism enabled on port 7445"))
		}
	}

	return errors.Join(errs...)
}

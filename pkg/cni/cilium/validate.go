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

	return errors.Join(spec.Check(user, effective), guards(effective, c))
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

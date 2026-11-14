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
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/values"
)

// OwnedKeys are the Cilium Helm values Ichor sets itself; a user value for one is a validation error. Each maps to
// the reason.
var OwnedKeys = map[string]string{
	keyIPAMMode: "Talos needs Kubernetes host-scope IPAM",
	"securityContext.capabilities.ciliumAgent":      "the Talos guide fixes the capability set Cilium runs with",
	"securityContext.capabilities.cleanCiliumState": "the Talos guide fixes the capability set Cilium runs with",
	"cgroup.autoMount.enabled":                      "Talos mounts cgroup2 itself, so Cilium must not",
	"cgroup.hostRoot":                               "Talos mounts cgroup2 at /sys/fs/cgroup",
	"k8sServiceHost":                                "KubePrism address, set only when spec.cni.kubeProxy is replaced",
	"k8sServicePort":                                "KubePrism port, set only when spec.cni.kubeProxy is replaced",
	"kubeProxyReplacement":                          "owned by the kube-proxy switch, spec.cni.kubeProxy",
	"tunnelProtocol":                                "owned by spec.cni.tunnel, which is immutable",

	// The namespace of the Roles that let Cilium read Secrets stays Cilium's own.
	"ingressController.secretsNamespace.name": secretsNamespaceReason,
	"gatewayAPI.secretsNamespace.name":        secretsNamespaceReason,
	"envoyConfig.secretsNamespace.name":       secretsNamespaceReason,
	"bgpControlPlane.secretsNamespace.name":   secretsNamespaceReason,
	"tls.secretsNamespace.name":               secretsNamespaceReason,

	// The agent reads Secrets only in that namespace, never cluster-wide.
	keyReadSecretsOnly: clusterWideSecretsReason,
	keySecretsBackend:  clusterWideSecretsReason,
}

const clusterWideSecretsReason = "the Cilium agent, a privileged pod on every node, may read Secrets only in " +
	"cilium-secrets, not cluster-wide"

const secretsNamespaceReason = "Cilium may read Secrets only in its own namespace, cilium-secrets"

var loadChart = sync.OnceValues(freshChart)

// compiledSchema compiles the chart's values.schema.json once: compiling it per
// validation was most of ValidateValues' allocations.
var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	ch, err := loadChart()
	if err != nil {
		return nil, err
	}

	return values.CompileSchema(ch)
})

func valuesSpec() (values.Spec, error) {
	ch, err := loadChart()
	if err != nil {
		return values.Spec{}, err
	}

	schema, err := compiledSchema()
	if err != nil {
		return values.Spec{}, err
	}

	return values.Spec{Chart: ch, Owned: OwnedKeys, Schema: schema}, nil
}

// WithValues returns a copy of p whose bootstrap manifest also applies the
// user's Cilium values (spec.cni.cilium.values). Nil or empty changes nothing.
func (p Preset) WithValues(user map[string]any) Preset {
	p.user = user

	return p
}

// layers splits Ichor's own values for kubeProxy into the overridable
// defaults and the owned keys.
func (p Preset) layers(kubeProxy cni.KubeProxyMode) (defaults, owned map[string]any, err error) {
	raw, err := json.Marshal(p.valuesFor(kubeProxy))
	if err != nil {
		return nil, nil, fmt.Errorf("cilium: marshalling Ichor values: %w", err)
	}

	if err := json.Unmarshal(raw, &defaults); err != nil {
		return nil, nil, fmt.Errorf("cilium: reading Ichor values: %w", err)
	}

	owned = map[string]any{}

	for path := range OwnedKeys {
		if v, ok := values.Get(defaults, path); ok {
			values.Set(owned, path, v)
			values.Delete(defaults, path)
		}
	}

	return defaults, owned, nil
}

func (p Preset) effective(kubeProxy cni.KubeProxyMode) (map[string]any, error) {
	defaults, owned, err := p.layers(kubeProxy)
	if err != nil {
		return nil, err
	}

	spec, err := valuesSpec()
	if err != nil {
		return nil, err
	}

	if errs := spec.CheckOwned(p.user); len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return values.Effective(defaults, p.user, owned), nil
}

// newPreset is the Preset for a kube-proxy mode, tunnel and control-plane count, the mode and tunnel
// validated.
func newPreset(kubeProxy cni.KubeProxyMode, tunnel string, controlPlanes int) (Preset, error) {
	if err := kubeProxy.Validate(); err != nil {
		return Preset{}, err
	}

	p, err := New().WithTunnel(tunnel)
	if err != nil {
		return Preset{}, err
	}

	return p.WithControlPlanes(controlPlanes), nil
}

// Layers returns Ichor's own values split into the overridable defaults and
// the owned keys, for showing the layers separately.
// tunnel is "", "vxlan" or "geneve"; controlPlanes is the cluster's control-plane count and nodes how many Nodes it has
// now (each 0 unknown; Preset.WithNodes).
func Layers(
	kubeProxy cni.KubeProxyMode, tunnel string, controlPlanes, nodes int,
) (defaults, owned map[string]any, err error) {
	p, err := newPreset(kubeProxy, tunnel, controlPlanes)
	if err != nil {
		return nil, nil, err
	}

	return p.WithNodes(nodes).layers(kubeProxy)
}

// EffectiveValues returns the Helm values Cilium is rendered with: Ichor's
// defaults, then user, then the Ichor-owned keys.
// tunnel is "", "vxlan" or "geneve"; controlPlanes is the cluster's control-plane count (0 unknown, which
// keeps the chart's operator replica default). A user value for an owned key is an error naming the key.
func EffectiveValues(
	user map[string]any, kubeProxy cni.KubeProxyMode, tunnel string, controlPlanes int,
) (map[string]any, error) {
	p, err := newPreset(kubeProxy, tunnel, controlPlanes)
	if err != nil {
		return nil, err
	}

	return p.WithValues(user).effective(kubeProxy)
}

// EffectiveValuesForMode is EffectiveValues for what cluster stores (spec.cni.tunnel and
// spec.cni.cilium.values) with the kube-proxy mode given, not read from spec.cni.kubeProxy: the
// controller applies the mode the cluster currently runs, and only a kube-proxy switch applies
// the spec's. An empty mode reads as enabled.
func EffectiveValuesForMode(cluster *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode) (map[string]any, error) {
	return EffectiveValuesForNodes(cluster, mode, 0)
}

// EffectiveValuesForNodes is EffectiveValuesForMode for a cluster that has nodes Nodes right now (0
// unknown). The operator holds host ports and wants one pod per node, so its replicas follow the nodes
// that exist, not spec.controlPlane.count: an HA cluster is bootstrapped with one node and the others
// join later. The caller must pass the same count to everything that
// hashes or applies the values.
func EffectiveValuesForNodes(
	cluster *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode, nodes int,
) (map[string]any, error) {
	c := cluster.Spec.CNI

	var user map[string]any

	if c.Cilium != nil && c.Cilium.Values != nil && len(c.Cilium.Values.Raw) > 0 {
		if err := json.Unmarshal(c.Cilium.Values.Raw, &user); err != nil {
			return nil, fmt.Errorf("cilium: reading spec.cni.cilium.values: %w", err)
		}
	}

	if mode == "" {
		mode = cni.KubeProxyEnabled
	}

	p, err := newPreset(mode, c.Tunnel, int(cluster.Spec.ControlPlane.Count))
	if err != nil {
		return nil, err
	}

	eff, err := p.WithNodes(nodes).WithValues(user).effective(mode)
	if err != nil {
		return nil, err
	}

	// A Cilium patch an earlier Ichor ran stays until the embedded chart reaches it, so an Ichor upgrade never steps
	// Cilium back (revised). Nothing sets a new one.
	if o := ImageOverrideActive(cluster); o != nil {
		if err := WithImageOverrides(eff, o.Images); err != nil {
			return nil, err
		}
	}

	// spec.security.wireGuard is typed and wins over a conflicting user value.
	if s := cluster.Spec.Security; s != nil && s.WireGuard != nil && s.WireGuard.Enabled {
		w := s.WireGuard

		values.Set(eff, "encryption.enabled", true)
		values.Set(eff, "encryption.type", "wireguard")
		values.Set(eff, "encryption.nodeEncryption", w.NodeEncryption)
		// The agents read encryption only at start and the DaemonSet carries no config hash by
		// default: this makes the chart roll them (rolling update) when cilium-config changes.
		values.Set(eff, "rollOutCiliumPods", true)
	}

	return eff, nil
}

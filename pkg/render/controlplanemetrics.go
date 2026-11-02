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
	"fmt"
	"net/netip"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	"github.com/siderolabs/talos/pkg/machinery/config/types/meta"
	"github.com/siderolabs/talos/pkg/machinery/config/types/network"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	"github.com/siderolabs/talos/pkg/machinery/nethelpers"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The metrics ports Talos 1.14.3 leaves on localhost (ADR 0069).
const (
	portControllerManagerMetrics = 10257 // kube-controller-manager secure port (HTTPS)
	portSchedulerMetrics         = 10259 // kube-scheduler secure port (HTTPS)
	portEtcdMetrics              = 2381  // etcd listen-metrics-urls (plain HTTP)

	// ruleNameControlPlaneMetrics names the NetworkRuleConfig document.
	ruleNameControlPlaneMetrics = "ichor-control-plane-metrics"

	argBindAddress       = "bind-address"
	argListenMetricsURLs = "listen-metrics-urls"
)

// injectControlPlaneMetrics renders spec.controlPlaneMetrics (ADR 0069) onto control-plane
// nodes: controller-manager and scheduler bind on all addresses, etcd serves its metrics on
// 0.0.0.0:2381, and one ingress firewall rule drops connections to exactly those three ports from
// any source outside spec.controlPlaneMetrics.allowedCIDRs. The firewall's default action is left
// alone (accept), so no other port is affected. It is a system-layer step (below every patch);
// unset or disabled returns cfg untouched, so the render is byte-identical to before.
func injectControlPlaneMetrics(
	cfg coreconfig.Provider, contract *coreconfig.VersionContract,
	cluster *ichorv1alpha1.TalosCluster, machineType machine.Type,
) (coreconfig.Provider, error) {
	spec := cluster.Spec.ControlPlaneMetrics
	if spec == nil || !spec.Enabled || machineType != machine.TypeControlPlane {
		return cfg, nil
	}

	subnets, err := metricsSubnets(spec.AllowedCIDRs)
	if err != nil {
		return nil, err
	}

	all := meta.Args{argBindAddress: meta.NewArgValue("0.0.0.0", nil)}
	etcd := meta.Args{argListenMetricsURLs: meta.NewArgValue(fmt.Sprintf("http://0.0.0.0:%d", portEtcdMetrics), nil)}

	docs := []config.Document{metricsRule(subnets)}

	// etcd's arguments have no standalone document in 1.14.3: cluster.etcd.extraArgs is the only
	// form, so the legacy Config document is always rendered and a pre-1.14 contract adds the
	// controller-manager and scheduler to it.
	//nolint:staticcheck // the legacy blocks are the only form these accept
	legacy := &mv1alpha1.Config{
		ConfigVersion: mv1alpha1.Version,
		ClusterConfig: &mv1alpha1.ClusterConfig{EtcdConfig: &mv1alpha1.EtcdConfig{EtcdExtraArgs: etcd}},
	}

	if contract.MultidocKubernetesConfigSupported() {
		cm := k8s.NewKubeControllerManagerConfigV1Alpha1()
		cm.PodArgs = all

		sched := k8s.NewKubeSchedulerConfigV1Alpha1()
		sched.PodArgs = all

		docs = append(docs, cm, sched)
	} else {
		//nolint:staticcheck // see above
		legacy.ClusterConfig.ControllerManagerConfig = &mv1alpha1.ControllerManagerConfig{ExtraArgsConfig: all}
		//nolint:staticcheck // see above
		legacy.ClusterConfig.SchedulerConfig = &mv1alpha1.SchedulerConfig{ExtraArgsConfig: all}
	}

	return mergeDocument(cfg, "control-plane metrics", append(docs, legacy)...)
}

// metricsSubnets parses allowedCIDRs, masked to their network address. Empty is an error: with
// the firewall's default action at accept, a rule without a subnet would be no restriction.
func metricsSubnets(cidrs []string) ([]netip.Prefix, error) {
	if len(cidrs) == 0 {
		return nil, fmt.Errorf("render: controlPlaneMetrics is enabled but allowedCIDRs is empty")
	}

	return parsePrefixes("render: controlPlaneMetrics.allowedCIDRs", cidrs)
}

// metricsRule is the one firewall document: TCP to the three metrics ports is accepted only from
// subnets. Under Talos's default action (accept) the rule is rendered as "drop when the source is
// not in subnets"; lo, KubeSpan, SideroLink and established connections are accepted before it
// (nftables_chain_config.go in Talos 1.14.3), so node-local scrapes keep working.
func metricsRule(subnets []netip.Prefix) *network.RuleConfigV1Alpha1 {
	r := network.NewRuleConfigV1Alpha1()
	r.MetaName = ruleNameControlPlaneMetrics
	r.PortSelector.Protocol = nethelpers.ProtocolTCP
	r.PortSelector.Ports = network.PortRanges{
		{Lo: portEtcdMetrics, Hi: portEtcdMetrics},
		{Lo: portControllerManagerMetrics, Hi: portControllerManagerMetrics},
		{Lo: portSchedulerMetrics, Hi: portSchedulerMetrics},
	}

	for _, s := range subnets {
		r.Ingress = append(r.Ingress, network.IngressRule{Subnet: s})
	}

	return r
}

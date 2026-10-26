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

// Package fieldmeta answers, for a Talos config field path, what Ichor's
// mirror types can't carry on their own (design section 4.4; ADR 0003): does
// changing this field need a reboot, is it only honoured at install time,
// and which Talos version does it need. The default for any path not
// covered below is reboot-required, the safe direction: guessing "no
// reboot" on a field that needs one leaves a node running a configuration
// it does not have.
//
// Paths are dot-separated, spelled the way machinery's own `yaml` tags (and
// talos.dev) spell the field, rooted at the kind name for the two small
// standalone documents Ichor allows today ("SysctlConfig",
// "KernelModuleConfig") and at "machine"/"cluster" for the legacy "Config"
// document (e.g. "machine.install", "cluster.apiServer.extraArgs").
//
// Why the table is short: Talos does not carry this information anywhere a
// generator could read it.
//
//   - machinery's docgen comments (config/types/.../*.go — the same source
//     talos.dev's reference pages render from) say "reboot" exactly twice in
//     the whole v1alpha1.Config tree (registry auth, kernel module
//     parameters) and never say "install-time" or "introduced in" for any
//     field.
//   - Talos's own ApplyConfiguration RPC
//     (internal/app/machined/internal/server/v1alpha1 in the talos module)
//     has no per-field table either: "no-reboot" mode always writes the
//     config to the COSI resource layer immediately, and each subsystem's
//     controller decides on its own whether it needs to restart something.
//     A full node reboot is only needed for the fields no controller
//     re-reads after boot (disk layout, kernel modules, the install image).
//     That is domain knowledge about Talos, not something read out of a
//     file, which is why it is cited per rule below instead of derived.
//
// The one thing machinery does carry is version gating, through
// config.VersionContract: its Greater-gated methods are read directly for
// the "introduced in" column instead of guessed.
package fieldmeta

import (
	"strings"

	machineryconfig "github.com/siderolabs/talos/pkg/machinery/config"
)

// rule is one exception to the reboot-required default.
type rule struct {
	prefix string

	// safe means a plain apply (no reboot) is sufficient. Ignored when
	// installTimeOnly is set, since that needs a different operation
	// entirely (reset and reinstall), not a reboot either way.
	safe bool

	// installTimeOnly means Talos only reads this field during
	// installation/imaging; changing it on a running node needs a reset and
	// reinstall, not an apply of any kind.
	installTimeOnly bool

	// introducedAfter mirrors config.VersionContract.Greater's own
	// semantics: the path is only valid when the cluster's pinned contract
	// is strictly greater than this one. Nil means no known floor.
	introducedAfter *machineryconfig.VersionContract
}

// rules holds only exceptions; everything else defaults to reboot-required.
// Longest matching prefix wins; the Kube*Config ".image" entries rely on it
// to carve the image out of their document's safe rule.
var rules = []rule{
	// SysctlConfig and KernelModuleConfig only exist as their own documents
	// from Talos >1.13 (machinery config.VersionContract's
	// MultidocSysctlConfigSupported / MultidocKernelModuleConfigSupported);
	// before that the same settings were only reachable inline under
	// machine.sysctls / machine.kernel.modules.
	{prefix: "SysctlConfig", safe: true, introducedAfter: machineryconfig.TalosVersion1_13},
	{prefix: "KernelModuleConfig", introducedAfter: machineryconfig.TalosVersion1_13},

	// machine.install is only read during `talosctl install`/imaging and
	// ignored afterwards (architecture.md, "Nodes" section: disk roles are
	// "fixed at install time ... changing system or ephemeral later is a
	// reset-and-reinstall operation").
	{prefix: "machine.install", installTimeOnly: true},

	// The time syncer restarts itself when its config changes; confirmed
	// live, no reboot (siderolabs/talos time-sync docs and engineering
	// discussion: "time, resolver and sysctl changes apply live without
	// requiring a reboot").
	{prefix: "machine.time", safe: true},

	// DNS resolver settings apply through that same live path.
	{prefix: "machine.network.nameservers", safe: true},
	{prefix: "machine.network.searchDomains", safe: true},
	{prefix: "machine.network.disableSearchDomain", safe: true},

	// Kubernetes control-plane components are static pods Talos
	// regenerates on config change; the kubelet restarts the container in
	// place, no node reboot (confirmed: "restarts to the
	// scheduler/controller-manager static pods occur when applying such
	// changes; no node reboot is expected", and the same for apiServer).
	// cluster.proxy is deliberately absent: architecture.md's open
	// questions says Ichor doesn't yet know what happens to the kube-proxy
	// DaemonSet when it's toggled live, so it stays reboot-required.
	{prefix: "cluster.apiServer", safe: true},
	{prefix: "cluster.controllerManager", safe: true},
	{prefix: "cluster.scheduler", safe: true},

	// VolumeConfig.provisioning (which disk a system volume -- STATE or
	// EPHEMERAL -- lives on, and its min/max size) is install-time-only,
	// the same as machine.install and for the same underlying reason:
	// architecture.md's "Nodes" section says plainly that changing which
	// disk `system` or `ephemeral` lives on later is a reset-and-reinstall
	// operation. Confirmed independently in Talos's own source
	// (internal/app/machined/pkg/controllers/block/volume_manager.go's
	// processVolumeConfig): the disk selector is only consulted, and the
	// partition only grown, while a volume is in the Waiting/Missing/
	// Located phase; once a volume reaches VolumePhaseReady the state
	// machine does nothing further, so neither a later selector nor size
	// change does anything until the volume is reset back to missing.
	//
	// VolumeConfig.filesystem, .encryption and .mount are deliberately NOT
	// included: whether changing them on an already-provisioned volume is
	// live, reboot-sufficient or install-time-only was not verified (the
	// state machine's Format/HandleEncryption steps are gated the same way
	// provisioning is, which suggests install-time-only, but changing
	// encryption or filesystem settings is consequential enough that a
	// suggestive read isn't enough to act on), so they stay at the
	// reboot-required default. .trim and .scrub are also excluded on
	// purpose: both are read by their own continuously-scheduled
	// controllers (volume_trim.go, fs_scrub.go), not this provisioning
	// state machine, and so are very likely live -- but that is also not
	// independently confirmed, so they too stay at the default rather than
	// being guessed into "safe".
	//
	// UserVolumeConfig and RawVolumeConfig deliberately have no entry:
	// architecture.md says assigning `storage` or `userVolume` to a
	// previously unused disk does not need a reset, and Talos's own
	// documented no-reboot allow-list (docs.siderolabs.com, "Edit Machine
	// Configuration") does not mention any volume config kind, so the
	// reboot-required default -- not installTimeOnly, not safe -- is each
	// kind's correct classification already, with no rule needed.
	{prefix: "VolumeConfig.provisioning", installTimeOnly: true},

	// The rules below were verified against the Talos v1.14.3 source (ADR 0009,
	// "Multi-document and registry paths"); the table there lists the files.
	// Paths a rule does not name stay reboot-required.

	// Registry mirrors (and TLS) are rendered into containerd's hosts.toml
	// files, which Talos rewrites on every config change and containerd reads
	// per pull. Registry AUTH is deliberately absent: it goes into the CRI
	// plugin's own config, and machinery's docs say it needs a reboot.
	{prefix: "machine.registries.mirrors", safe: true},

	// Node labels, annotations and taints are reconciled onto the Kubernetes
	// Node object by NodeLabelSpec/NodeAnnotationSpec/NodeTaintSpec and
	// NodeApplyController, which also removes what it no longer owns. The
	// legacy fields and the standalone KubeNodeConfig document feed the same
	// controllers; cluster.allowScheduling* is read only to derive the
	// control-plane taint. KubeNodeConfig.nodeIP is deliberately absent.
	{prefix: "KubeNodeConfig.labels", safe: true},
	{prefix: "KubeNodeConfig.annotations", safe: true},
	{prefix: "KubeNodeConfig.taints", safe: true},
	{prefix: "machine.nodeLabels", safe: true},
	{prefix: "machine.nodeAnnotations", safe: true},
	{prefix: "machine.nodeTaints", safe: true},
	{prefix: "cluster.allowSchedulingOnControlPlanes", safe: true},
	{prefix: "cluster.allowSchedulingOnMasters", safe: true},

	// The standalone static-pod kinds are the multi-document form of
	// cluster.apiServer / controllerManager / scheduler above: the same
	// controller (controllers/k8s/control_plane.go) reads both.
	//
	// The .image entries are NOT safe on purpose; they are not an oversight,
	// do not "complete" them. Talos would apply an image change without a
	// reboot, but a safe rule would make the machine controller apply every
	// node the moment spec.kubernetesVersion changes, destroying the
	// one-node-at-a-time order of the Kubernetes upgrade executor (ADR 0057),
	// which applies the image fields itself with an explicit no-reboot apply.
	// The same goes for KubeProxyConfig.image and KubeletConfig.image, which
	// have no rule at all (so their whole documents stay reboot-required).
	{prefix: "KubeAPIServerConfig", safe: true},
	{prefix: "KubeAPIServerConfig.image"},
	{prefix: "KubeControllerManagerConfig", safe: true},
	{prefix: "KubeControllerManagerConfig.image"},
	{prefix: "KubeSchedulerConfig", safe: true},
	{prefix: "KubeSchedulerConfig.image"},

	// The ingress firewall (ADR 0069). NfTablesChainConfigController
	// (internal/app/machined/pkg/controllers/network/nftables_chain_config.go) takes the active
	// MachineConfig as a watched input and rewrites the nftables chains on every change, and
	// ApplyConfiguration's no-reboot mode persists the config without restarting anything. Both
	// kinds feed that one controller. cluster.etcd.* is deliberately absent: the etcd service reads
	// its extra arguments once, when it starts (services/etcd.go), so a changed listen-metrics-urls
	// stays reboot-required.
	{prefix: "NetworkRuleConfig", safe: true},
	{prefix: "NetworkDefaultActionConfig", safe: true},
}

func matchesPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+".")
}

// classify looks up a single field path against rules, longest prefix wins.
func classify(path string) rule {
	best := -1

	var matched rule

	for _, r := range rules {
		if matchesPrefix(path, r.prefix) && len(r.prefix) > best {
			best = len(r.prefix)
			matched = r
		}
	}

	return matched
}

// Impact is what a set of changed field paths means for applying them.
type Impact struct {
	// RebootRequired is true if any path needs a plain reboot to take
	// effect.
	RebootRequired bool

	// InstallTimeOnly lists the paths that need a reset and reinstall
	// rather than any kind of apply.
	InstallTimeOnly []string

	// Unsupported lists the paths that need a newer Talos version than the
	// one passed to Assess.
	Unsupported []string
}

// Assess classifies a set of changed field paths against the cluster's
// pinned Talos version contract (design principle 4). pinned follows
// config.VersionContract's own convention: nil means the current/latest
// Talos, so nothing is ever reported unsupported against it.
func Assess(paths []string, pinned *machineryconfig.VersionContract) Impact {
	var impact Impact

	for _, p := range paths {
		r := classify(p)

		switch {
		case r.installTimeOnly:
			impact.InstallTimeOnly = append(impact.InstallTimeOnly, p)
		case !r.safe:
			impact.RebootRequired = true
		}

		if r.introducedAfter != nil && !pinned.Greater(r.introducedAfter) {
			impact.Unsupported = append(impact.Unsupported, p)
		}
	}

	return impact
}

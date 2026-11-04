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

package fieldmeta

import (
	"reflect"
	"testing"

	machineryconfig "github.com/siderolabs/talos/pkg/machinery/config"
)

// Paths reused across several test cases below.
const (
	pathClusterProxyDisabled = "cluster.proxy.disabled"     // uncovered on purpose, see architecture.md's open questions
	pathMachineNetworkIfaces = "machine.network.interfaces" // uncovered on purpose: NIC changes stay reboot-required
	pathMachineInstallDisk   = "machine.install.disk"
	pathSysctlForward        = "SysctlConfig.params.net.ipv4.ip_forward"
	pathKubeletMounts        = "machine.kubelet.extraMounts"
	pathKubeProxyArgs        = "KubeProxyConfig.extraArgs"
	pathKubeAuthJWT          = "KubeAuthenticationConfig.configuration.jwt"
	pathUnattendedSelector   = "UnattendedInstallConfig.provisioning.diskSelector.match"

	pathVolumeConfigSelector = "VolumeConfig.provisioning.diskSelector.match"
	pathLinkAliasSelector    = "LinkAliasConfig.selector.match"
	// Uncovered on purpose: trim is read by its own scheduled controller,
	// not the provisioning state machine, see fieldmeta.go.
	pathVolumeConfigTrim = "VolumeConfig.trim.enabled"
	// Uncovered on purpose: architecture.md says assigning a previously
	// unused disk to these roles does not need a reset.
	pathUserVolumeConfigSelector = "UserVolumeConfig.provisioning.diskSelector.match"
	pathRawVolumeConfigSelector  = "RawVolumeConfig.provisioning.diskSelector.match"
)

func TestAssess_RebootPathsNameTheFieldsThatNeedTheReboot(t *testing.T) {
	impact := Assess([]string{pathClusterProxyDisabled, pathMachineInstallDisk, pathMachineNetworkIfaces}, nil)

	want := []string{pathClusterProxyDisabled, pathMachineNetworkIfaces}
	if !reflect.DeepEqual(impact.RebootPaths, want) {
		t.Fatalf("RebootPaths = %v, want %v (the install-time-only path is not one)", impact.RebootPaths, want)
	}
}

// wantRebootRequired fails unless every path, assessed alone, needs a plain reboot and nothing else.
func wantRebootRequired(t *testing.T, paths ...string) {
	t.Helper()

	for _, path := range paths {
		got := Assess([]string{path}, nil)
		if !got.RebootRequired || len(got.InstallTimeOnly) != 0 || len(got.APIServerRestartPaths) != 0 {
			t.Errorf("Assess(%q) = %+v, want reboot-required only", path, got)
		}
	}
}

func TestAssess_UnknownPathIsRebootRequired(t *testing.T) {
	wantRebootRequired(t, pathClusterProxyDisabled)
}

// TestAssess_UserAndRawVolumeConfigStayRebootRequired pins a deliberate
// choice, not an oversight: unlike VolumeConfig (system volumes),
// UserVolumeConfig and RawVolumeConfig have no fieldmeta rule at all, since
// architecture.md says assigning one of their roles to a previously unused
// disk does not need a reset. The reboot-required default is already their
// correct classification; this test is what would fail if a future change
// added an (incorrect) installTimeOnly or safe rule for either.
func TestAssess_UserAndRawVolumeConfigStayRebootRequired(t *testing.T) {
	wantRebootRequired(t, pathUserVolumeConfigSelector, pathRawVolumeConfigSelector)
}

// TestAssess_HostnameStaysRebootRequired pins spec.hostname's two rendered forms (HostnameConfig
// from the v1.14 contract, machine.network.hostname before it). Talos's HostnameConfigController
// would apply a new name live, but the kubelet registers the Kubernetes node under the name it
// started with, so a rename is only settled by a reboot; no rule is added on purpose.
func TestAssess_HostnameStaysRebootRequired(t *testing.T) {
	wantRebootRequired(t, "HostnameConfig.hostname", "HostnameConfig.auto", "machine.network.hostname")
}

// TestAssess_Rules covers every rule in the table: one path that matches it
// and a sibling path that does not (and so stays reboot-required).
func TestAssess_Rules(t *testing.T) {
	cases := []struct {
		name           string
		match          string
		nonMatch       string
		wantInstallSet bool
	}{
		{name: "SysctlConfig is safe", match: pathSysctlForward, nonMatch: "KernelModuleConfig.parameters"},
		{name: "machine.time is safe", match: "machine.time.servers", nonMatch: "machine.network.hostname"},
		{
			name: "machine.network.nameservers is safe", match: "machine.network.nameservers",
			nonMatch: pathMachineNetworkIfaces,
		},
		{
			name: "machine.network.searchDomains is safe", match: "machine.network.searchDomains",
			nonMatch: pathMachineNetworkIfaces,
		},
		{
			name: "machine.network.disableSearchDomain is safe", match: "machine.network.disableSearchDomain",
			nonMatch: pathMachineNetworkIfaces,
		},
		{name: "cluster.apiServer is safe", match: "cluster.apiServer.extraArgs", nonMatch: pathClusterProxyDisabled},
		{
			name: "cluster.controllerManager is safe", match: "cluster.controllerManager.extraArgs",
			nonMatch: pathClusterProxyDisabled,
		},
		{name: "cluster.inlineManifests is safe", match: "cluster.inlineManifests", nonMatch: pathClusterProxyDisabled},
		{
			name: "cluster.inlineManifests entries are safe", match: "cluster.inlineManifests.0.contents",
			nonMatch: pathClusterProxyDisabled,
		},
		{
			name: "KubeInlineManifestConfig is safe", match: "KubeInlineManifestConfig.manifest",
			nonMatch: pathClusterProxyDisabled,
		},
		{name: "cluster.extraManifests is safe", match: "cluster.extraManifests", nonMatch: pathClusterProxyDisabled},
		{
			name: "cluster.extraManifestHeaders is safe", match: "cluster.extraManifestHeaders.x",
			nonMatch: pathClusterProxyDisabled,
		},
		{name: "cluster.scheduler is safe", match: "cluster.scheduler.extraArgs", nonMatch: pathClusterProxyDisabled},
		{
			name: "KubeAuthenticationConfig is safe", match: pathKubeAuthJWT,
			nonMatch: "KubeletConfig.image",
		},
		{name: "LinkConfig is safe", match: "LinkConfig.addresses", nonMatch: pathLinkAliasSelector},
		{name: "VLANConfig is safe", match: "VLANConfig.vlanID", nonMatch: pathLinkAliasSelector},
		{name: "DHCPv4Config is safe", match: "DHCPv4Config.routeMetric", nonMatch: pathLinkAliasSelector},
		{
			name: "ResolverConfig.nameservers is safe", match: "ResolverConfig.nameservers",
			nonMatch: "ResolverConfig.hostDNS.enabled",
		},
		{
			name: "machine.install is install-time-only, not reboot", match: pathMachineInstallDisk,
			nonMatch: "machine.installer.anything", wantInstallSet: true,
		},
		{
			name:     "UnattendedInstallConfig is install-time-only, not reboot",
			match:    pathUnattendedSelector,
			nonMatch: "KubeFlannelCNIConfig.backendType", wantInstallSet: true,
		},
		{
			name: "VolumeConfig.provisioning is install-time-only, not reboot", match: pathVolumeConfigSelector,
			nonMatch: pathVolumeConfigTrim, wantInstallSet: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matchImpact := Assess([]string{tc.match}, nil)
			if matchImpact.RebootRequired {
				t.Errorf("Assess(%q).RebootRequired = true, want false", tc.match)
			}
			if tc.wantInstallSet && !reflect.DeepEqual(matchImpact.InstallTimeOnly, []string{tc.match}) {
				t.Errorf("Assess(%q).InstallTimeOnly = %v, want [%q]", tc.match, matchImpact.InstallTimeOnly, tc.match)
			}
			if !tc.wantInstallSet && len(matchImpact.InstallTimeOnly) != 0 {
				t.Errorf("Assess(%q).InstallTimeOnly = %v, want empty", tc.match, matchImpact.InstallTimeOnly)
			}

			nonMatchImpact := Assess([]string{tc.nonMatch}, nil)
			if !nonMatchImpact.RebootRequired {
				t.Errorf("Assess(%q) (sibling, uncovered) = %+v, want RebootRequired true", tc.nonMatch, nonMatchImpact)
			}
			if len(nonMatchImpact.InstallTimeOnly) != 0 {
				t.Errorf("Assess(%q) (sibling) = %+v, want no InstallTimeOnly", tc.nonMatch, nonMatchImpact)
			}
		})
	}
}

func TestAssess_VersionFloor(t *testing.T) {
	cases := []struct {
		name            string
		pinned          *machineryconfig.VersionContract
		wantUnsupported bool
	}{
		{name: "older than introduced", pinned: machineryconfig.TalosVersion1_12, wantUnsupported: true},
		{name: "exactly the floor version", pinned: machineryconfig.TalosVersion1_13, wantUnsupported: true},
		{name: "newer than introduced", pinned: machineryconfig.TalosVersion1_14, wantUnsupported: false},
		{name: "nil pinned means current, always supported", pinned: nil, wantUnsupported: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			impact := Assess([]string{pathSysctlForward}, tc.pinned)
			gotUnsupported := len(impact.Unsupported) == 1
			if gotUnsupported != tc.wantUnsupported {
				t.Fatalf("Assess(pinned=%v).Unsupported = %v, want unsupported=%v",
					tc.pinned, impact.Unsupported, tc.wantUnsupported)
			}
		})
	}

	// A path with no known version floor (the legacy Config document) is
	// never reported unsupported, regardless of how old the pinned version
	// is.
	impact := Assess([]string{pathMachineInstallDisk}, machineryconfig.TalosVersion1_0)
	if len(impact.Unsupported) != 0 {
		t.Fatalf("Assess(path with no version floor) = %+v, want no Unsupported entries", impact)
	}
}

func TestAssess_SafeAndUnsupportedAreIndependent(t *testing.T) {
	// SysctlConfig is both "safe" (no reboot) and version-gated: on an old
	// pinned version it's unsupported *and* would need no reboot if it were
	// supported. The two facts don't override each other.
	impact := Assess([]string{pathSysctlForward}, machineryconfig.TalosVersion1_12)

	if impact.RebootRequired {
		t.Fatalf("Assess(SysctlConfig path).RebootRequired = true, want false")
	}
	if len(impact.Unsupported) != 1 {
		t.Fatalf("Assess(SysctlConfig path on Talos 1.12).Unsupported = %v, want one entry", impact.Unsupported)
	}
}

func TestAssess_AggregatesAcrossMultiplePaths(t *testing.T) {
	impact := Assess([]string{
		"machine.time.servers",   // safe
		pathClusterProxyDisabled, // reboot-required (unknown/uncertain)
		pathMachineInstallDisk,   // install-time-only
	}, nil)

	if !impact.RebootRequired {
		t.Fatalf("Assess(mixed paths).RebootRequired = false, want true (cluster.proxy.disabled is unknown)")
	}
	if len(impact.InstallTimeOnly) != 1 || impact.InstallTimeOnly[0] != pathMachineInstallDisk {
		t.Fatalf("Assess(mixed paths).InstallTimeOnly = %v, want [%s]", impact.InstallTimeOnly, pathMachineInstallDisk)
	}
}

// TestAssess_NoRebootRules covers the rules "multi-document and
// registry paths" addendum verified against the Talos v1.14.3 source: each
// path applies without a reboot, and its uncovered sibling still needs one.
func TestAssess_NoRebootRules(t *testing.T) {
	cases := []struct{ name, safe, sibling string }{
		{
			"registry mirrors", "machine.registries.mirrors.docker.io.endpoints",
			pathKubeletMounts,
		},
		{"registry auth", "machine.registries.config.docker.io.auth.password", pathKubeletMounts},
		{"registry tls", "machine.registries.config.registry.local:5000.tls.ca", pathKubeletMounts},
		{"RegistryAuthConfig", "RegistryAuthConfig.password", pathKubeletMounts},
		{"RegistryTLSConfig", "RegistryTLSConfig.ca", pathKubeletMounts},
		{"RegistryMirrorConfig", "RegistryMirrorConfig.endpoints", pathKubeletMounts},
		{"KubeNodeConfig labels", "KubeNodeConfig.labels.x", "KubeNodeConfig.nodeIP.validSubnets"},
		{"KubeNodeConfig annotations", "KubeNodeConfig.annotations.x", "KubeNodeConfig.nodeIP.validSubnets"},
		{"KubeNodeConfig taints", "KubeNodeConfig.taints.node-role.kubernetes.io/control-plane", "KubeNodeConfig.nodeIP"},
		{"machine.nodeLabels", "machine.nodeLabels.x", pathKubeletMounts},
		{"machine.nodeAnnotations", "machine.nodeAnnotations.x", pathKubeletMounts},
		{"machine.nodeTaints", "machine.nodeTaints.x", pathKubeletMounts},
		{"cluster.allowSchedulingOnControlPlanes", "cluster.allowSchedulingOnControlPlanes", pathClusterProxyDisabled},
		{"cluster.allowSchedulingOnMasters", "cluster.allowSchedulingOnMasters", pathClusterProxyDisabled},
		{"KubeAPIServerConfig args", "KubeAPIServerConfig.extraArgs.x", pathKubeProxyArgs},
		{"KubeControllerManagerConfig args", "KubeControllerManagerConfig.extraArgs.x", pathKubeProxyArgs},
		{"KubeSchedulerConfig args", "KubeSchedulerConfig.extraArgs.x", pathKubeProxyArgs},
		{"NetworkRuleConfig", "NetworkRuleConfig.ingress", "cluster.etcd.extraArgs.listen-metrics-urls"},
		{"NetworkDefaultActionConfig", "NetworkDefaultActionConfig.ingress", "cluster.etcd.extraArgs.listen-metrics-urls"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Assess([]string{tc.safe}, nil); got.RebootRequired {
				t.Errorf("Assess(%q).RebootRequired = true, want false", tc.safe)
			}

			if got := Assess([]string{tc.sibling}, nil); !got.RebootRequired {
				t.Errorf("Assess(%q) (sibling) = %+v, want RebootRequired true", tc.sibling, got)
			}
		})
	}
}

// TestAssess_APIServerRestartClass pins the audit in fieldmeta.go: everything
// Talos applies by re-rendering the kube-apiserver static pod is reboot-free but its own class, so
// the machine controller leaves it to the config operation; the controller-manager and scheduler
// documents stay plainly safe.
func TestAssess_APIServerRestartClass(t *testing.T) {
	for _, path := range []string{
		pathKubeAuthJWT, "KubeAPIServerConfig.extraArgs.x",
		"KubeAdmissionControlConfig.plugins", "KubeAuditPolicyConfig.policy", "KubeAuthorizerConfig.authorizers",
		"cluster.apiServer.extraArgs", "cluster.apiServer.certSANs", "cluster.apiServer.admissionControl",
		"cluster.apiServer.auditPolicy",
	} {
		got := Assess([]string{path}, nil)
		if got.RebootRequired || len(got.APIServerRestartPaths) != 1 || len(got.InstallTimeOnly) != 0 {
			t.Errorf("Assess(%q) = %+v, want exactly one APIServerRestartPath and no reboot", path, got)
		}
	}

	plain := []string{"cluster.controllerManager.extraArgs", "KubeSchedulerConfig.extraArgs.x", pathSysctlForward}
	for _, path := range plain {
		if got := Assess([]string{path}, nil); got.RebootRequired || len(got.APIServerRestartPaths) != 0 {
			t.Errorf("Assess(%q) = %+v, want plainly safe", path, got)
		}
	}

	// The image stays reboot-required (the Kubernetes upgrade executor owns it).
	wantRebootRequired(t, "KubeAPIServerConfig.image")

	// The legacy paths are the only spelling a pre-1.14 contract has: the class holds there too, and
	// their images stay with the Kubernetes upgrade, never safe or apiServerRestart.
	for _, pinned := range []*machineryconfig.VersionContract{
		machineryconfig.TalosVersion1_12, machineryconfig.TalosVersion1_13,
	} {
		if got := Assess([]string{"cluster.apiServer.certSANs"}, pinned); got.RebootRequired ||
			len(got.APIServerRestartPaths) != 1 {
			t.Errorf("legacy cluster.apiServer.certSANs on %v = %+v, want one APIServerRestartPath", pinned, got)
		}

		for _, path := range []string{
			"cluster.apiServer.image", "cluster.controllerManager.image", "cluster.scheduler.image",
		} {
			if got := Assess([]string{path}, pinned); !got.RebootRequired || len(got.APIServerRestartPaths) != 0 {
				t.Errorf("Assess(%q, %v) = %+v, want reboot-required only", path, pinned, got)
			}
		}
	}

	// A mixed set reports each class on its own list.
	mixed := Assess([]string{pathKubeAuthJWT, pathClusterProxyDisabled}, nil)
	if !mixed.RebootRequired || len(mixed.APIServerRestartPaths) != 1 || len(mixed.RebootPaths) != 1 {
		t.Errorf("mixed = %+v", mixed)
	}
}

// TestAssess_KubernetesImagesStayRebootRequired pins a deliberate omission
// (fieldmeta.go): a component image path must never be no-reboot,
// or a spec.kubernetesVersion bump would be applied to every node at once by
// the machine controller instead of one at a time by the upgrade executor.
func TestAssess_KubernetesImagesStayRebootRequired(t *testing.T) {
	wantRebootRequired(t,
		"KubeAPIServerConfig.image", "KubeControllerManagerConfig.image", "KubeSchedulerConfig.image",
		"KubeProxyConfig.image", "KubeletConfig.image",
		"cluster.apiServer.image", "cluster.controllerManager.image", "cluster.scheduler.image",
	)
}

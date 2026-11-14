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

package main

import (
	"fmt"
	"slices"
	"strings"
)

// shippedKind is one kind that has a field in TalosConfigPatchDocument.
type shippedKind struct {
	// kind is the key in kindClasses (what Talos registers).
	kind string
	// what completes "<field> is a Talos ...; populated when kind is <kind>" in the field's doc; empty means
	// "<kind> document". A "\n" starts a new comment line.
	what string
}

// shipped lists the kinds that have a CRD field, in field order. It is append-only: a CRD field, once an
// installed build has had it, is never removed or renamed (docs/adr/0150-*), so a kind stays here even if
// it is later reclassified away from accept or Talos stops registering it. The generator reports the first
// and refuses the second. To ship an accept kind, append it here and run `make generate manifests`.
var shipped = []shippedKind{
	{"SysctlConfig", ""},
	{"KernelModuleConfig", ""},
	{legacyKind, "v1alpha1 Config document"},
	{"VolumeConfig", "VolumeConfig document (a system volume, e.g. STATE or EPHEMERAL,\nplaced on its own disk)"},
	{"UserVolumeConfig", ""},
	{"RawVolumeConfig", "RawVolumeConfig document (an unformatted partition for a CSI\nprovisioner)"},
	{"ResolverConfig", "ResolverConfig document (DNS nameservers and search domains)"},
	{"BGPInstanceConfig", ""},
	{"BlackholeRouteConfig", ""},
	{"BondConfig", ""},
	{"BridgeConfig", ""},
	{"ContainerConfig", ""},
	{"DHCPv6Config", ""},
	{"DummyLinkConfig", ""},
	{"EnvironmentConfig", ""},
	{"EtcFileConfig", ""},
	{"EthernetConfig", ""},
	{"EventSinkConfig", ""},
	{"ExistingVolumeConfig", ""},
	{"ExtensionServiceConfig", ""},
	{"ExternalVolumeConfig", ""},
	{"FilesystemScrubConfig", ""},
	{"FilesystemTrimConfig", ""},
	{"HTTPProbeConfig", ""},
	{"ImageCacheConfig", ""},
	{"ImageVerificationConfig", ""},
	{"KmsgLogConfig", ""},
	{"KubeAPIServerConfig", ""},
	{"KubeClusterConfig", ""},
	{"KubeControllerManagerConfig", ""},
	{"KubeCoreDNSConfig", ""},
	{"KubeInlineManifestConfig", ""},
	{"KubeNetworkConfig", ""},
	{"KubeNodeConfig", ""},
	{"KubePrismConfig", ""},
	{"KubeSpanConfig", ""},
	{"KubeSpanEndpointsConfig", ""},
	{"KubeTalosAPIAccessConfig", ""},
	{"LVMLogicalVolumeConfig", ""},
	{"LVMVolumeGroupConfig", ""},
	{"Layer2VIPConfig", ""},
	{"OOMConfig", ""},
	{"PCIDriverRebindConfig", ""},
	{"RAIDArrayConfig", ""},
	{"RegistryMirrorConfig", ""},
	{"RoutingRuleConfig", ""},
	{"StaticHostConfig", ""},
	{"SwapVolumeConfig", ""},
	{"SysfsConfig", ""},
	{"TCPProbeConfig", ""},
	{"TimeSyncConfig", ""},
	{"TrustedRootsConfig", ""},
	{"UdevRulesConfig", ""},
	{"VRFConfig", ""},
	{"VethConfig", ""},
	{"WatchdogTimerConfig", ""},
	{"ZswapConfig", ""},
	{"KubeAuditPolicyConfig", ""},
	{"KubeAuthorizerConfig", ""},
	{"KubeCredentialProviderConfig", ""},
	{"KubeExternalManifestConfig", ""},
	{"KubeletConfig", ""},
	{"KubeProxyConfig", ""},
	{"KubeSchedulerConfig", ""},
	{"KubeStaticPodConfig", ""},
}

// legacyKind is how Talos registers the legacy machine and cluster document; Ichor calls it legacyName.
const (
	legacyKind = "v1alpha1"
	legacyName = "Config"
)

// ichorName is the discriminator value of a registered kind.
func ichorName(registeredKind string) string {
	if registeredKind == legacyKind {
		return legacyName
	}

	return registeredKind
}

// deriveRoots resolves every shipped kind against Talos's registry (reg) and the classification table. The
// package path, type, kind constant and API version all come from the registry; nothing is written per kind.
// The notes name each shipped kind that is no longer classed accept: its field stays (append-only).
func deriveRoots(reg map[string]registered, classes map[string]kindRow, list []shippedKind) (roots []rootKind, notes []string, err error) {
	seen := map[string]bool{}

	for _, s := range list {
		if seen[s.kind] {
			return nil, nil, fmt.Errorf("shipped lists kind %q twice", s.kind)
		}

		seen[s.kind] = true

		c, ok := classes[s.kind]
		if !ok {
			return nil, nil, fmt.Errorf("shipped kind %q has no row in kindClasses", s.kind)
		}

		r, ok := reg[s.kind]
		if !ok {
			return nil, nil, fmt.Errorf("shipped kind %q is no longer registered by Talos: its CRD field cannot be removed, "+
				"so keep it by hand and do not delete it from shipped", s.kind)
		}

		if r.ambiguous {
			return nil, nil, fmt.Errorf("shipped kind %q is registered with several versions or types; mirrorgen mirrors exactly one", s.kind)
		}

		if c.class != accept {
			notes = append(notes, fmt.Sprintf("%s is classed %s but shipped: its field stays, the controller must refuse it", s.kind, c.class))
		}

		what := s.what
		if what == "" {
			what = ichorName(s.kind) + " document"
		}

		roots = append(roots, rootKind{
			pkgPath: r.pkgPath, typeName: r.typeName, kind: ichorName(s.kind),
			constPkg: r.constPkg, constName: r.constName, apiVersion: r.apiVersion, what: what,
		})
	}

	return roots, notes, nil
}

// pendingKinds returns the accept kinds in classes that are not shipped yet, sorted.
func pendingKinds(classes map[string]kindRow, list []shippedKind) []string {
	var out []string

	for k, c := range classes {
		if c.class == accept && !slices.ContainsFunc(list, func(s shippedKind) bool { return s.kind == k }) {
			out = append(out, k)
		}
	}

	slices.Sort(out)

	return out
}

// fieldName is the JSON name of a kind's union field: the kind with its leading capitals lowered, keeping the
// last of a capital run that is followed by a lowercase letter (BGPInstanceConfig -> bgpInstanceConfig),
// unless that letter begins a version marker (DHCPv6Config -> dhcpv6Config).
func fieldName(kind string) string {
	i := 0
	for i < len(kind) && kind[i] >= 'A' && kind[i] <= 'Z' {
		i++
	}

	// A lowercase letter followed by a digit is a version marker (DHCPv6Config), not the start of a word.
	if i > 1 && i < len(kind) && kind[i] >= 'a' && kind[i] <= 'z' && (i+1 == len(kind) || kind[i+1] < '0' || kind[i+1] > '9') {
		i--
	}

	return strings.ToLower(kind[:i]) + kind[i:]
}

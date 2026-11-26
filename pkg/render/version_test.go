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
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

// pinnedVersion and pinnedFingerprint hold Version and the output fingerprint it was last bumped
// for. They move together: a render change updates the fingerprint and bumps Version.
const (
	fpReader     = "os:reader"
	fpDNS1       = "1.1.1.1"
	fpDNS2       = "9.9.9.9"
	fpWWID       = "naa.1"
	fpByID       = "/dev/disk/by-id/x"
	fpStaged     = "v1.36.1"
	fpTPM        = "tpm"
	fpKMS        = "kms"
	fpRestricted = "restricted"
	fpEnabled    = "enabled"

	kubeProxyReplaced = "replaced"

	pinnedVersion     = 2
	pinnedFingerprint = "sha256:d6f54a89419d753b60f464b4140b27aa4e6ac699cbc6b6af785af43db2f558dc"
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

// fingerprintCA is a fixed CA (a generated one would move the fingerprint on every run).
const fingerprintCA = `-----BEGIN CERTIFICATE-----
MIIBZTCCAQugAwIBAgIBATAKBggqhkjOPQQDAjAZMRcwFQYDVQQDEw5maW5nZXJw
cmludC1jYTAgFw0yNjAxMDEwMDAwMDBaGA8yMTI2MDEwMTAwMDAwMFowGTEXMBUG
A1UEAxMOZmluZ2VycHJpbnQtY2EwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAASZ
W2MKhLsB+P96wDsgkd0/oVFXxeZU0ubxQWfoymaStu2mIOVhkiEAbsdSQQk7MnFW
3ow0S9f8eHKSOkcCwHygo0IwQDAOBgNVHQ8BAf8EBAMCAgQwDwYDVR0TAQH/BAUw
AwEB/zAdBgNVHQ4EFgQUb8AmjetAl6Vx+T4tbvcb/V4B13owCgYIKoZIzj0EAwID
SAAwRQIgYH2Cp++B3Jj8XxbO/Dbj37XTbggND7wmUHCAlUBeKcMCIQCjbMunVTsI
E26+NfEqbodMpZdSnwkEGVjJpktw6g5cSw==
-----END CERTIFICATE-----
`

// fingerprintCluster sets one field per input-dependent branch of the renderer, so a change to any
// of them moves the fingerprint. TestFingerprintCoversEveryInput fails for a field of the spec or of
// SystemInputs that no fixture sets. Feature -> field:
//   - registry mirror auth keyed by endpoint host: Spec.Registries + SystemInputs.RegistryAuth
//   - pod security exemptions (dedupe, kube-system): Spec.Security.PodSecurity.ExemptNamespaces
//   - firewall rules, extra sources, kubespan, allow rule: Spec.Security.Firewall
//   - firewall ports from the effective Cilium values: Spec.CNI.Cilium.Values tunnelPort and clusterHealthPort
//   - Cilium WireGuard port: Spec.Security.WireGuard
//   - kube-proxy replaced: Spec.CNI.KubeProxy
//   - audit policy, kubelet serving certificates, disk encryption (tpm), containerd: Spec.Security, Spec.Containerd
//   - authentication, Ichor and external issuers: Spec.Authentication + SystemInputs.OIDCTrust
//   - control-plane metrics: Spec.ControlPlaneMetrics
//   - pod and service subnets, cert SANs, no default node labels: Spec.Network, Spec.CertSANs, Spec.ControlPlane
//   - the longhorn-v1 storage preset: SystemInputs.Storage
func fingerprintCluster() *ichorv1alpha1.TalosCluster {
	off := false

	c := testCluster("false")
	c.Spec.Registries = []ichorv1alpha1.TalosClusterRegistry{{
		Host: dockerIO, Endpoints: []string{"http://192.168.0.8:5000"},
		AuthSecretRef: &corev1.LocalObjectReference{Name: "creds"},
	}, {
		Host: "ghcr.io", Endpoints: []string{"https://mirror.example.test"},
		Username: "puller", SkipFallback: true, OverridePath: true,
	}}
	c.Spec.CNI.Tunnel = "geneve"
	c.Spec.CNI.KubeProxy = kubeProxyReplaced
	ports := &apiextensionsv1.JSON{Raw: []byte(`{"tunnelPort":8473,"clusterHealthPort":4241}`)}
	c.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{Values: ports}
	c.Spec.Security = &ichorv1alpha1.TalosClusterSecurity{
		PodSecurity: &ichorv1alpha1.TalosClusterPodSecurity{
			Enforce: "baseline", Audit: fpRestricted, Warn: fpRestricted,
			ExemptNamespaces: []string{"kube-system", "observability", "observability"},
		},
		WireGuard: &ichorv1alpha1.TalosClusterWireGuard{Enabled: true, NodeEncryption: true},
		Firewall: &ichorv1alpha1.TalosClusterFirewall{
			Enabled: true, ControllerCIDRs: []string{"192.0.2.0/24"}, ControlPlaneCIDRs: []string{"10.2.0.0/16"},
			NodeCIDRs:     []string{"10.0.0.0/24"},
			TalosAPICIDRs: []string{"172.20.0.0/14"}, APIServerCIDRs: []string{"198.51.100.0/24"}, KubeSpan: true,
			Allow: []ichorv1alpha1.TalosClusterFirewallRule{
				{Name: "ingress", Protocol: "udp", Ports: []string{"80", "9000-9100"}, CIDRs: []string{"0.0.0.0/0"}},
			},
		},
		Audit:                      &ichorv1alpha1.TalosClusterAudit{Policy: ichorv1alpha1.AuditPolicyWrites},
		KubeletServingCertificates: &ichorv1alpha1.TalosClusterKubeletServing{Enabled: true},
		DiskEncryption:             &ichorv1alpha1.TalosClusterDiskEncryption{Type: fpTPM, RequireSecureBoot: &off},
		SecureBoot:                 &ichorv1alpha1.TalosClusterSecureBoot{Mode: ichorv1alpha1.SecureBootRequired},
	}
	c.Spec.Containerd = &ichorv1alpha1.TalosClusterContainerd{DeviceOwnershipFromSecurityContext: true}
	c.Spec.ControlPlaneMetrics = &ichorv1alpha1.TalosClusterControlPlaneMetrics{
		Enabled: true, AllowedCIDRs: []string{"10.1.0.0/16"},
	}
	c.Spec.Network = &ichorv1alpha1.TalosClusterNetwork{
		PodSubnets: []string{"10.50.0.0/16"}, ServiceSubnets: []string{"10.60.0.0/16"},
	}
	c.Spec.CertSANs = []string{"api.example.test"}
	c.Spec.ControlPlane.OmitDefaultNodeLabels = true
	c.Spec.Authentication = &ichorv1alpha1.TalosClusterAuthentication{
		Ichor:    &ichorv1alpha1.TalosClusterIchorIssuer{Enabled: true, URL: "https://ichor.example.test"},
		External: []ichorv1alpha1.TalosClusterExternalIssuer{externalIssuer()},
	}
	c.Spec.Authentication.External[0].CertificateAuthority = fingerprintCA

	return c
}

// fingerprintSystem is the system layer's inputs of every case that is not the legacy one.
func fingerprintSystem() *SystemInputs {
	return &SystemInputs{
		RegistryAuth: map[string]RegistryAuth{dockerIO: {Password: "pw"}},
		OIDCTrust:    &OIDCTrust{DiscoveryURL: "https://10.96.0.10", CAPEM: fingerprintCA},
		Storage:      []ichorv1alpha1.TalosStorage{testStorage("longhorn", map[string]string{labelEnv: "fingerprint"})},
	}
}

// fingerprintMachine is the machine of a case: its static network on a VLAN with DNS, its disks
// and its install target.
func fingerprintMachine(role string, install ichorv1alpha1.TalosMachineDisk) *ichorv1alpha1.TalosMachine {
	m := testMachine("node-"+role, role, map[string]string{labelEnv: "fingerprint"})
	m.Spec.Hostname = "node-" + role
	m.Spec.Network = &ichorv1alpha1.TalosMachineNetwork{
		MAC: testNICMAC, Addressing: ichorv1alpha1.MachineAddressingStatic, Addresses: []string{"10.0.0.10/24"},
		Gateway: "10.0.0.1", DNSServers: []string{fpDNS1, fpDNS2}, VLANID: 100,
	}
	m.Spec.Disks = []ichorv1alpha1.TalosMachineDiskRole{
		{Role: ichorv1alpha1.MachineDiskRoleEphemeral, Disk: ichorv1alpha1.TalosMachineDisk{Serial: "EPH1"}},
		{Role: ichorv1alpha1.MachineDiskRoleUserVolume, Name: "data", MaxSize: "200GiB",
			Disk: ichorv1alpha1.TalosMachineDisk{WWID: fpWWID}},
		{Role: ichorv1alpha1.MachineDiskRoleUserVolume, Name: "scratch",
			Disk: ichorv1alpha1.TalosMachineDisk{ByIDPath: fpByID}},
		{Role: ichorv1alpha1.MachineDiskRoleUserVolume, Name: "fast",
			Disk: ichorv1alpha1.TalosMachineDisk{DevPath: "/dev/sdb"}},
		{Role: ichorv1alpha1.MachineDiskRoleUserVolume, Name: "bulk",
			Disk: ichorv1alpha1.TalosMachineDisk{BusPath: "/pci0000:00/0000:00:17.0/ata1"}},
	}
	m.Spec.Install = &ichorv1alpha1.TalosMachineInstall{
		Disk: install, Image: "factory.talos.dev/metal-installer/abc:v1.14.3", Wipe: true,
	}

	return m
}

// fingerprintCases are the renders the fingerprint covers: a control plane and a worker on the
// current contract, a control plane that only takes the status-driven branches, a worker with a
// Talos API access patch, a control plane whose patch moves the apiserver port with the firewall on,
// and a control plane on the v1.13 contract.
func fingerprintCases(t *testing.T, newBundle func(contract, shift string) *secrets.Bundle) []fingerprintCase {
	t.Helper()

	on := true
	access := &ichorconfig.MachineFeaturesKubernetesTalosAPIAccess{
		Enabled: &on, AllowedRoles: []string{fpReader}, AllowedKubernetesNamespaces: []string{"ops"},
	}
	sysctl := sysctlPatch("p", layerCluster, 0, nil, "1")

	// The CNI hand-over, the kube-proxy mode and the kubelet serving latch come from status.
	statusDriven := fingerprintCluster()
	statusDriven.Spec.Security.KubeletServingCertificates.VerifyFromAPIServer = true
	statusDriven.Status.Conditions = []metav1.Condition{
		{Type: ConditionCNIHandover, Status: metav1.ConditionTrue, Reason: "Reached"},
		{Type: ichorv1alpha1.ConditionKubeletServingVerified, Status: metav1.ConditionTrue, Reason: "Reached"},
	}
	statusDriven.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{KubeProxy: fpEnabled}

	legacy := fingerprintCluster()
	legacy.Spec.VersionContract = contractV113
	legacy.Spec.Security = nil // the hardening options need v1.14
	legacy.Spec.Authentication = nil
	legacy.Spec.ControlPlaneMetrics = nil
	legacy.Spec.Containerd = nil

	diskByID := ichorv1alpha1.TalosMachineDisk{ByIDPath: fpByID}
	diskWWID := ichorv1alpha1.TalosMachineDisk{WWID: fpWWID}
	diskSerial := ichorv1alpha1.TalosMachineDisk{Serial: "SN1"}
	diskDev := ichorv1alpha1.TalosMachineDisk{DevPath: "/dev/sda"}
	diskBus := ichorv1alpha1.TalosMachineDisk{BusPath: "/pci0000:00/0000:00:17.0/ata2"}

	// A worker takes the other disk-encryption key source, and a control plane the unmanaged CNI.
	kms := fingerprintCluster()
	kms.Spec.Security.DiskEncryption = &ichorv1alpha1.TalosClusterDiskEncryption{
		Type: fpKMS, KMSEndpoint: "https://kms.example.test",
	}

	unmanaged := fingerprintCluster()
	unmanaged.Spec.CNI.Preset = ichorv1alpha1.TalosClusterCNIPresetUnmanaged
	unmanaged.Spec.CNI.Cilium = nil
	unmanaged.Spec.CNI.Unmanaged = &ichorv1alpha1.TalosClusterCNIUnmanaged{TalosCNI: "flannel"}

	cur := func() (*secrets.Bundle, *secrets.Bundle) { return testBundle(t), newBundle(versionContract, "1h") }

	var out []fingerprintCase

	add := func(name string, c *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
		sys *SystemInputs, patches ...ichorv1alpha1.TalosConfigPatch,
	) {
		a, b := cur()
		out = append(out, fingerprintCase{name, c, m, a, b, patches, sys})
	}

	add("cp", fingerprintCluster(), fingerprintMachine(roleControlPlane, diskByID), fingerprintSystem(), sysctl)
	add("worker", fingerprintCluster(), fingerprintMachine(roleWorker, diskWWID), fingerprintSystem(), sysctl)
	add("cp-status", statusDriven, fingerprintMachine(roleControlPlane, diskSerial), fingerprintSystem())
	add("worker-api-access", fingerprintCluster(), fingerprintMachine(roleWorker, diskDev), fingerprintSystem(),
		configPatch("access", &ichorconfig.Config{Machine: &ichorconfig.Machine{
			Features: &ichorconfig.MachineFeatures{KubernetesTalosAPIAccess: access},
		}}))
	add("worker-kms", kms, fingerprintMachine(roleWorker, diskByID), fingerprintSystem())
	add("cp-unmanaged-cni", unmanaged, fingerprintMachine(roleControlPlane, diskByID), fingerprintSystem())
	add("cp-staged", fingerprintCluster(), fingerprintMachine(roleControlPlane, diskByID), func() *SystemInputs {
		sys := fingerprintSystem()
		sys.Staged = &StagedKubernetes{
			ControllerManagerVersion: fpStaged, SchedulerVersion: fpStaged,
			KubeletVersion: fpStaged, KubeProxyVersion: fpStaged,
		}

		return sys
	}())
	add("cp-api-access", fingerprintCluster(), fingerprintMachine(roleControlPlane, diskBus), fingerprintSystem(),
		configPatch("access", &ichorconfig.Config{Machine: &ichorconfig.Machine{
			Features: &ichorconfig.MachineFeatures{KubernetesTalosAPIAccess: access},
		}}))
	add("cp-apiserver-port", fingerprintCluster(), fingerprintMachine(roleControlPlane, diskByID), fingerprintSystem(),
		configPatch("port", &ichorconfig.Config{Cluster: &ichorconfig.Cluster{
			ControlPlane: &ichorconfig.ClusterControlPlane{LocalAPIServerPort: 7443},
		}}))

	out = append(out, fingerprintCase{
		"legacy-contract", legacy, fingerprintMachine(roleControlPlane, diskByID),
		newBundle(contractV113, "0h"), newBundle(contractV113, "1h"),
		[]ichorv1alpha1.TalosConfigPatch{storedDocumentsPatch(t, timeServersDocument)},
		&SystemInputs{RegistryAuth: map[string]RegistryAuth{dockerIO: {Password: "pw"}}},
	})

	return out
}

type fingerprintCase struct {
	name    string
	cluster *ichorv1alpha1.TalosCluster
	machine *ichorv1alpha1.TalosMachine
	a, b    *secrets.Bundle
	patches []ichorv1alpha1.TalosConfigPatch
	sys     *SystemInputs
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

	var lines []string

	for _, tc := range fingerprintCases(t, newBundle) {
		a := leaves(t, []byte(mustRenderSys(t, tc.cluster, tc.machine, tc.a, tc.patches, tc.sys)))
		b := leaves(t, []byte(mustRenderSys(t, tc.cluster, tc.machine, tc.b, tc.patches, tc.sys)))

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

// fingerprintUnaffecting lists the input fields no fixture sets because they cannot change the
// render. Every entry names why; a field that is not here must be set by a case of fingerprintCases.
const (
	reasonApproval  = "an audit record of the approval; Render does not read it"
	reasonCustomCNI = "the custom CNI is installed by the controller's Helm path, not rendered into the machine config"
)

var fingerprintUnaffecting = map[string]string{
	"TalosMachineSpec.ApprovalState": "the approval gate acts before a render; Render does not read it",

	"TalosMachineSpec.ApprovalDecision.Actor": reasonApproval,

	"TalosMachineSpec.ApprovalDecision.Reason": reasonApproval,

	"TalosMachineSpec.ApprovalDecision.At": reasonApproval,

	"TalosMachineSpec.Identity.Serial": "matches a discovered machine in the controller, not in Render",

	"TalosMachineSpec.Identity.MACAddresses": "matches a discovered machine in the controller, not in Render",

	"TalosClusterSpec.DriftPolicy": "decides what the controller does with drift, not what is rendered",

	"TalosClusterSpec.ImageFactoryURL": "the controller builds installer images from it; Render does not read it",

	"TalosClusterSpec.Schematic.ID": "the controller builds installer images from it; Render does not read it",

	"TalosClusterSpec.Discovery.Addresses": "read by pkg/endpointguard, not by Render",

	"TalosClusterSpec.Discovery.CIDRs": "read by pkg/endpointguard, not by Render",

	"TalosClusterSpec.Security.KernelLockdown": "read by pkg/secureboot's status check and the schematic, not by Render",

	"TalosClusterSpec.CNI.Custom.Source.RepoURL": reasonCustomCNI,

	"TalosClusterSpec.CNI.Custom.Source.TargetRevision": reasonCustomCNI,

	"TalosClusterSpec.CNI.Custom.Source.Path": reasonCustomCNI,

	"TalosClusterSpec.CNI.Custom.Source.Chart": reasonCustomCNI,

	"SystemInputs.Storage[].Spec.EvacuationTimeout": "read by the node-operation hook (layer 4); layer 1 does not read it",

	"SystemInputs.CARotation.Stage": "the CA rotation executor's input; New is stored key material, not fixable here",

	"SystemInputs.CARotation.New": "as Stage",

	"SystemInputs.KeyRotation.Stage": "the key rotation executor's input; New is stored key material, not fixable here",

	"SystemInputs.KeyRotation.New": "as Stage",
}

// fingerprintCoverage walks v's type and returns, for every leaf field, whether v sets it.
// Structs of the Ichor API and of this package are descended into (through pointers, and into slice
// elements); anything else, and every map, slice and scalar, is a leaf.
func fingerprintCoverage(v reflect.Value, path string, out map[string]bool) {
	ty := v.Type()

	switch {
	case ty.Kind() == reflect.Pointer && ty.Elem().Kind() == reflect.Struct:
		if v.IsNil() {
			v = reflect.Zero(ty.Elem())
		} else {
			v = v.Elem()
		}

		fingerprintCoverage(v, path, out)

		return
	case ty.Kind() == reflect.Slice && ty.Elem().Kind() == reflect.Struct && descends(ty.Elem()):
		out[path] = out[path] || v.Len() > 0

		elem := reflect.New(ty.Elem()).Elem()
		if v.Len() > 0 {
			elem = v.Index(0)
		}

		fingerprintCoverage(elem, path+"[]", out)

		for i := 1; i < v.Len(); i++ {
			fingerprintCoverage(v.Index(i), path+"[]", out)
		}

		return
	case ty.Kind() == reflect.Struct && descends(ty):
		for i := range ty.NumField() {
			if f := ty.Field(i); f.Name != "ObjectMeta" && f.Name != "TypeMeta" && f.Name != "Status" {
				fingerprintCoverage(v.Field(i), path+"."+f.Name, out)
			}
		}

		return
	}

	out[path] = out[path] || !v.IsZero()
}

func descends(ty reflect.Type) bool {
	return strings.HasPrefix(ty.PkgPath(), "github.com/maeshinshin/ichor/api/") ||
		ty.PkgPath() == reflect.TypeFor[SystemInputs]().PkgPath()
}

// TestFingerprintCoversEveryInput: a field of the cluster spec, the machine spec or SystemInputs
// that no fingerprint case sets is a render branch the pinned fingerprint cannot see (ADR 0088). Set
// it in fingerprintCluster, fingerprintMachine or fingerprintSystem, or, when it cannot change the
// render, list it in fingerprintUnaffecting with the reason.
func TestFingerprintCoversEveryInput(t *testing.T) {
	covered := map[string]bool{}

	for _, tc := range fingerprintCases(t, func(string, string) *secrets.Bundle { return nil }) {
		fingerprintCoverage(reflect.ValueOf(tc.cluster.Spec), "TalosClusterSpec", covered)
		fingerprintCoverage(reflect.ValueOf(tc.machine.Spec), "TalosMachineSpec", covered)
		fingerprintCoverage(reflect.ValueOf(*tc.sys), "SystemInputs", covered)
	}

	for path, set := range covered {
		_, listed := fingerprintUnaffecting[path]

		switch {
		case !set && !listed:
			t.Errorf("%s is zero in every fingerprint case", path)
		case set && listed:
			t.Errorf("%s is set by a fingerprint case, so remove it from fingerprintUnaffecting", path)
		}
	}

	for path := range fingerprintUnaffecting {
		if _, ok := covered[path]; !ok {
			t.Errorf("fingerprintUnaffecting names %s, which is no field", path)
		}
	}
}

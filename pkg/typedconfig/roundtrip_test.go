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

package typedconfig

import (
	"testing"
	"time"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	mcel "github.com/siderolabs/talos/pkg/machinery/cel"
	"github.com/siderolabs/talos/pkg/machinery/cel/celenv"
	mblock "github.com/siderolabs/talos/pkg/machinery/config/types/block"
	mmeta "github.com/siderolabs/talos/pkg/machinery/config/types/meta"
	mruntime "github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	rblock "github.com/siderolabs/talos/pkg/machinery/resources/block"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
	kubeletv1 "k8s.io/kubelet/config/v1"
)

// These are the generator's correctness check: a machinery
// document converted to its mirror and back must render to byte-identical
// YAML. A machinery bump that removes or retypes a field fails to compile
// (the conversion references the field by name) or fails here, instead of
// silently changing what Ichor's API accepts.

const apiVersionV1Alpha1 = "v1alpha1"

func TestRoundTripSysctlConfig(t *testing.T) {
	orig := &mruntime.SysctlConfigV1Alpha1{
		MetaKind: mruntime.SysctlConfigKind, MetaAPIVersion: apiVersionV1Alpha1,
		Params: map[string]string{
			"net.ipv4.ip_forward": "1",
			"kernel.domainname":   "talos.dev",
		},
	}

	assertRoundTrip(t, orig, func() (machineryConverter[mruntime.SysctlConfigV1Alpha1], error) {
		return SysctlConfigV1Alpha1FromMachinery(orig)
	})
}

func TestRoundTripKernelModuleConfig(t *testing.T) {
	orig := &mruntime.KernelModuleConfigV1Alpha1{
		MetaKind: mruntime.KernelModuleConfigKind, MetaAPIVersion: apiVersionV1Alpha1,
		MetaName:         "btrfs",
		ModuleParameters: []string{"foo=1", "bar=2"},
	}

	assertRoundTrip(t, orig, func() (machineryConverter[mruntime.KernelModuleConfigV1Alpha1], error) {
		return KernelModuleConfigV1Alpha1FromMachinery(orig)
	})
}

func TestRoundTripConfig(t *testing.T) {
	diskMatcher := &mv1alpha1.InstallDiskSizeMatcher{}
	if err := yaml.Unmarshal([]byte("'>= 100 GB'"), diskMatcher); err != nil {
		t.Fatalf("seeding disk size matcher: %v", err)
	}

	endpoint := &mv1alpha1.Endpoint{}
	if err := yaml.Unmarshal([]byte(`"https://10.0.0.1:6443"`), endpoint); err != nil {
		t.Fatalf("seeding endpoint: %v", err)
	}

	// The six fieldOverrides entries (internal/tools/mirrorgen/main.go):
	// building these through mirrorToMachineryUnstructured, the same helper
	// ToMachinery itself calls, guarantees the fixture is exactly the shape a
	// real round trip would produce, including every field the upstream
	// type requires with no omitempty.
	requests := mustUnstructured(t, "resources requests", corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("1"),
		corev1.ResourceMemory: resource.MustParse("2Gi"),
	})

	limits := mustUnstructured(t, "resources limits", corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("2"),
		corev1.ResourceMemory: resource.MustParse("4Gi"),
	})

	auditPolicy := mustUnstructured(t, "audit policy", auditv1.Policy{
		Rules: []auditv1.PolicyRule{{Level: auditv1.LevelMetadata}},
	})

	webhook := mustUnstructured(t, "authorizer webhook", apiserverv1.WebhookConfiguration{
		AuthorizedTTL:                            metav1.Duration{Duration: 5 * time.Minute},
		UnauthorizedTTL:                          metav1.Duration{Duration: 30 * time.Second},
		Timeout:                                  metav1.Duration{Duration: 30 * time.Second},
		SubjectAccessReviewVersion:               "v1",
		MatchConditionSubjectAccessReviewVersion: "v1",
		FailurePolicy:                            "NoOpinion",
		ConnectionInfo:                           apiserverv1.WebhookConnectionInfo{Type: "InClusterConfig"},
	})

	seccomp := mustUnstructured(t, "seccomp profile", specs.LinuxSeccomp{
		DefaultAction: specs.ActAllow,
		Architectures: []specs.Arch{specs.ArchX86_64},
	})

	// credentialProviderConfig exercises the sixth fieldOverrides entry.
	credentialProviderConfig := mustUnstructured(t, "credential provider", kubeletv1.CredentialProviderConfig{
		Providers: []kubeletv1.CredentialProvider{
			{
				Name:                 "ecr-credential-provider",
				MatchImages:          []string{"*.dkr.ecr.*.amazonaws.com"},
				DefaultCacheDuration: &metav1.Duration{Duration: 12 * time.Hour},
				APIVersion:           "credentialprovider.kubelet.k8s.io/v1",
			},
		},
	})

	orig := &mv1alpha1.Config{
		ConfigVersion: apiVersionV1Alpha1,
		ConfigDebug:   new(false),
		MachineConfig: &mv1alpha1.MachineConfig{
			MachineType: "controlplane",
			// MachineToken and MachineCA are deliberately left unset: both are on
			// mirrorgen's secretFields drop table, so the
			// mirror never carries them and a round trip that set them here would
			// not come back out. See roundtrip_test.go's package doc note, and
			// TestConfigRejectsSecretFields in api/v1alpha1 for the field-presence
			// side of that.
			MachineCertSANs: []string{"10.0.0.10", "203.0.113.10"},
			// MachineInstall, MachineKubelet and MachineFiles are deprecated in
			// favor of multi-document config kinds outside the phase 2 allow-list; they are still how a disk selector,
			// extraArgs and a file
			// mode reach v1alpha1.Config today, so the fixture exercises them here.
			MachineInstall: &mv1alpha1.InstallConfig{ //nolint:staticcheck // see comment above
				InstallDiskSelector: &mv1alpha1.InstallDiskSelector{
					Size:  diskMatcher,
					Model: "QEMU HARDDISK",
					Type:  mv1alpha1.InstallDiskType("ssd"),
				},
				InstallImage: "factory.talos.dev/metal-installer/abc123:latest",
				InstallWipe:  new(true),
			},
			MachineKubelet: &mv1alpha1.KubeletConfig{ //nolint:staticcheck // see comment above
				KubeletExtraArgs: mmeta.Args{ //nolint:staticcheck // see comment above
					"feature-gates": mmeta.NewArgValue("AllBeta=true", nil),
					"node-labels":   mmeta.NewArgValue("", []string{"foo=bar", "baz=qux"}),
				},
				KubeletCredentialProviderConfig: credentialProviderConfig,
			},
			MachineFiles: []*mv1alpha1.MachineFile{ //nolint:staticcheck // see comment above
				{
					FileContent:     "hello\n",
					FilePermissions: mv1alpha1.FileMode(0o644),
					FilePath:        "/etc/foo.conf",
					FileOp:          "create",
				},
			},
			// MachineSeccompProfiles exercises MachineSeccompProfile.MachineSeccompProfileValue,
			// one of the six fieldOverrides entries.
			MachineSeccompProfiles: []*mv1alpha1.MachineSeccompProfile{
				{
					MachineSeccompProfileName:  "audit.json",
					MachineSeccompProfileValue: seccomp,
				},
			},
		},
		ClusterConfig: &mv1alpha1.ClusterConfig{
			// ClusterName and ControlPlane are deprecated in favor of
			// KubeClusterConfig/KubeAPIServerConfig, outside the allow-list; same
			// reasoning as MachineInstall above.
			ClusterName: "test-cluster", //nolint:staticcheck // see comment above
			ControlPlane: &mv1alpha1.ControlPlaneConfig{ //nolint:staticcheck // see comment above
				Endpoint:           endpoint,
				LocalAPIServerPort: 6443, //nolint:staticcheck // see comment above
			},
			AdminKubeconfigConfig: &mv1alpha1.AdminKubeconfigConfig{
				AdminKubeconfigCertLifetime: 87600 * time.Hour,
			},
			// APIServerConfig is deprecated in favor of KubeAPIServerConfig, outside the
			// phase 2 allow-list; it is still how the remaining three fieldOverrides entries
			// (ResourcesConfig.Requests/Limits, APIServerConfig.AuditPolicyConfig and
			// AuthorizationConfigAuthorizerConfig.AuthorizerWebhook) reach v1alpha1.Config today.
			APIServerConfig: &mv1alpha1.APIServerConfig{ //nolint:staticcheck // see comment above
				ResourcesConfig: &mv1alpha1.ResourcesConfig{
					Requests: requests,
					Limits:   limits,
				},
				AuditPolicyConfig: auditPolicy, //nolint:staticcheck // see comment above
				//nolint:staticcheck // see comment above
				AuthorizationConfigConfig: mv1alpha1.AuthorizationConfigAuthorizerConfigList{
					{
						AuthorizerType:    "Webhook",
						AuthorizerName:    "example",
						AuthorizerWebhook: webhook,
					},
				},
			},
		},
	}

	assertRoundTrip(t, orig, func() (machineryConverter[mv1alpha1.Config], error) {
		return ConfigFromMachinery(orig)
	})
}

// Phase 3 (disk roles): the three volume document kinds. selectorFor builds a
// disk selector CEL expression the same way the approval flow would for a
// disk with none of WWID, serial or a by-id link -- architecture.md's
// documented fallback chain's last link. bus_path is always present, so
// this is the selector a virtio-blk disk actually gets matched by.
func selectorFor(expr string) mcel.Expression {
	return mcel.MustExpression(mcel.ParseBooleanExpression(expr, celenv.DiskLocator()))
}

func TestRoundTripVolumeConfig(t *testing.T) {
	orig := &mblock.VolumeConfigV1Alpha1{
		MetaKind: mblock.VolumeConfigKind, MetaAPIVersion: apiVersionV1Alpha1,
		MetaName: "EPHEMERAL",
		ProvisioningSpec: mblock.ProvisioningSpec{
			DiskSelectorSpec: mblock.DiskSelector{
				Match: selectorFor(`disk.bus_path == "/pci0000:00/0000:00:0a.0/virtio5/"`),
			},
			// A negative maxSize reserves free space rather than consuming it
			// (architecture.md); round-tripping one checks the sign survives.
			ProvisioningMaxSize: mblock.MustSize("-10GiB"),
		},
		FilesystemSpec: mblock.SystemVolumeFilesystemSpec{
			XFSSpec: &mblock.XFSSpec{MinAllocationGroupSizeConfig: mblock.MustByteSize("128MiB")},
		},
	}

	assertRoundTrip(t, orig, func() (machineryConverter[mblock.VolumeConfigV1Alpha1], error) {
		return VolumeConfigV1Alpha1FromMachinery(orig)
	})
}

func TestRoundTripUserVolumeConfig(t *testing.T) {
	volumeType := rblock.VolumeTypeDisk

	orig := &mblock.UserVolumeConfigV1Alpha1{
		MetaKind: mblock.UserVolumeConfigKind, MetaAPIVersion: apiVersionV1Alpha1,
		MetaName:   "local-data",
		VolumeType: &volumeType,
		ProvisioningSpec: mblock.ProvisioningSpec{
			DiskSelectorSpec: mblock.DiskSelector{
				Match: selectorFor(`disk.bus_path == "/pci0000:00/0000:00:0a.0/virtio5/" && !system_disk`),
			},
			ProvisioningMaxSize: mblock.MustSize("50GiB"),
		},
		FilesystemSpec: mblock.FilesystemSpec{
			FilesystemType: rblock.FilesystemTypeXFS,
		},
	}

	assertRoundTrip(t, orig, func() (machineryConverter[mblock.UserVolumeConfigV1Alpha1], error) {
		return UserVolumeConfigV1Alpha1FromMachinery(orig)
	})
}

func TestRoundTripRawVolumeConfig(t *testing.T) {
	orig := &mblock.RawVolumeConfigV1Alpha1{
		MetaKind: mblock.RawVolumeConfigKind, MetaAPIVersion: apiVersionV1Alpha1,
		MetaName: "ceph-osd",
		ProvisioningSpec: mblock.ProvisioningSpec{
			DiskSelectorSpec: mblock.DiskSelector{
				Match: selectorFor(`disk.transport == "nvme" && !system_disk`),
			},
		},
	}

	assertRoundTrip(t, orig, func() (machineryConverter[mblock.RawVolumeConfigV1Alpha1], error) {
		return RawVolumeConfigV1Alpha1FromMachinery(orig)
	})
}

func mustUnstructured[T any](t *testing.T, what string, v T) mmeta.Unstructured {
	t.Helper()

	u, err := mirrorToMachineryUnstructured(v)
	if err != nil {
		t.Fatalf("seeding %s: %v", what, err)
	}

	return u
}

// machineryConverter is a mirror type's ToMachinery method, named so the
// test functions below don't each repeat the anonymous interface.
type machineryConverter[M any] interface {
	ToMachinery() (*M, error)
}

// assertRoundTrip marshals orig to YAML, converts it to its mirror and back
// via fromMachinery, and checks the resulting YAML is byte-identical.
func assertRoundTrip[M any](t *testing.T, orig *M, fromMachinery func() (machineryConverter[M], error)) {
	t.Helper()

	wantYAML, err := yaml.Marshal(orig)
	if err != nil {
		t.Fatalf("marshaling original: %v", err)
	}

	mirror, err := fromMachinery()
	if err != nil {
		t.Fatalf("FromMachinery: %v", err)
	}

	back, err := mirror.ToMachinery()
	if err != nil {
		t.Fatalf("ToMachinery: %v", err)
	}

	gotYAML, err := yaml.Marshal(back)
	if err != nil {
		t.Fatalf("marshaling round-tripped value: %v", err)
	}

	if string(wantYAML) != string(gotYAML) {
		t.Fatalf("round trip not YAML-identical:\n--- want ---\n%s\n--- got ---\n%s", wantYAML, gotYAML)
	}
}

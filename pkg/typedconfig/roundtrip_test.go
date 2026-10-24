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

	mx509 "github.com/siderolabs/crypto/x509"
	mmeta "github.com/siderolabs/talos/pkg/machinery/config/types/meta"
	mruntime "github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	yaml "go.yaml.in/yaml/v4"
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

	boolTrue := true
	boolFalse := false

	orig := &mv1alpha1.Config{
		ConfigVersion: apiVersionV1Alpha1,
		ConfigDebug:   &boolFalse,
		MachineConfig: &mv1alpha1.MachineConfig{
			MachineType:     "controlplane",
			MachineToken:    "328hom.uqjzh6jnn2eie9oi",
			MachineCA:       &mx509.PEMEncodedCertificateAndKey{Crt: []byte("ca-cert-bytes"), Key: []byte("ca-key-bytes")},
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
				InstallWipe:  &boolTrue,
			},
			MachineKubelet: &mv1alpha1.KubeletConfig{ //nolint:staticcheck // see comment above
				KubeletExtraArgs: mmeta.Args{ //nolint:staticcheck // see comment above
					"feature-gates": mmeta.NewArgValue("AllBeta=true", nil),
					"node-labels":   mmeta.NewArgValue("", []string{"foo=bar", "baz=qux"}),
				},
			},
			MachineFiles: []*mv1alpha1.MachineFile{ //nolint:staticcheck // see comment above
				{
					FileContent:     "hello\n",
					FilePermissions: mv1alpha1.FileMode(0o644),
					FilePath:        "/etc/foo.conf",
					FileOp:          "create",
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
		},
	}

	assertRoundTrip(t, orig, func() (machineryConverter[mv1alpha1.Config], error) {
		return ConfigFromMachinery(orig)
	})
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

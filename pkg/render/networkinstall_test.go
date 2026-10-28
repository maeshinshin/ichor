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
	"errors"
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	contractV113 = "v1.13"
	devVDA       = "/dev/vda"

	docKubeNetwork  = "kind: KubeNetworkConfig"
	docFlannel      = "kind: KubeFlannelCNIConfig"
	docUnattended   = "kind: UnattendedInstallConfig"
	defaultPodNet   = "10.244.0.0/16"
	defaultSvcNet   = "10.96.0.0/12"
	customPodNet    = "10.200.0.0/16"
	customSvcNet    = "10.201.0.0/16"
	legacyCNINone   = "name: none"
	legacyInstall   = "install:"
	presetCiliumStr = "cilium"
)

type (
	disk     = ichorv1alpha1.TalosMachineDisk
	mutation = func(*ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine)
)

func renderFor(
	t *testing.T, contract string, mutate mutation,
) string {
	t.Helper()

	c := testCluster("true")
	c.Spec.VersionContract = contract
	m := testMachine("node-a", roleControlPlane, nil)

	if mutate != nil {
		mutate(c, m)
	}

	return mustRender(t, c, m, testBundle(t), nil)
}

func requireAll(t *testing.T, out string, want ...string) {
	t.Helper()

	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("rendered config lacks %q:\n%s", w, out)
		}
	}
}

func requireNone(t *testing.T, out string, unwanted ...string) {
	t.Helper()

	for _, w := range unwanted {
		if strings.Contains(out, w) {
			t.Errorf("rendered config must not contain %q:\n%s", w, out)
		}
	}
}

func TestSubnetsFollowTheContract(t *testing.T) {
	withNet := func(c *ichorv1alpha1.TalosCluster, _ *ichorv1alpha1.TalosMachine) {
		c.Spec.Network = &ichorv1alpha1.TalosClusterNetwork{
			PodSubnets: []string{customPodNet}, ServiceSubnets: []string{customSvcNet},
		}
	}

	t.Run("v1.14 renders the KubeNetworkConfig document", func(t *testing.T) {
		out := renderFor(t, versionContract, withNet)
		requireAll(t, out, docKubeNetwork, customPodNet, customSvcNet)
		requireNone(t, out, defaultPodNet, defaultSvcNet, "podSubnets: []")
	})

	t.Run("v1.14 without spec.network keeps Talos's defaults", func(t *testing.T) {
		requireAll(t, renderFor(t, versionContract, nil), docKubeNetwork, defaultPodNet, defaultSvcNet)
	})

	t.Run("v1.14 sets one range and keeps the other default", func(t *testing.T) {
		out := renderFor(t, versionContract, func(c *ichorv1alpha1.TalosCluster, _ *ichorv1alpha1.TalosMachine) {
			c.Spec.Network = &ichorv1alpha1.TalosClusterNetwork{PodSubnets: []string{customPodNet}}
		})
		requireAll(t, out, customPodNet, defaultSvcNet)
		requireNone(t, out, defaultPodNet)
	})

	t.Run("v1.13 renders cluster.network", func(t *testing.T) {
		out := renderFor(t, contractV113, withNet)
		requireAll(t, out, "podSubnets:", customPodNet, "serviceSubnets:", customSvcNet)
		requireNone(t, out, docKubeNetwork, defaultPodNet, defaultSvcNet)
	})

	t.Run("v1.13 without spec.network keeps Talos's defaults", func(t *testing.T) {
		requireAll(t, renderFor(t, contractV113, nil), defaultPodNet, defaultSvcNet)
	})

	t.Run("a worker gets the same subnets", func(t *testing.T) {
		out := renderFor(t, versionContract, func(c *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine) {
			withNet(c, m)
			m.Spec.Role = roleWorker
		})
		requireAll(t, out, docKubeNetwork, customPodNet)
	})
}

func TestTalosFlannelIsAlwaysDropped(t *testing.T) {
	for _, p := range []string{"", presetCiliumStr} {
		set := func(c *ichorv1alpha1.TalosCluster, _ *ichorv1alpha1.TalosMachine) { c.Spec.CNI.Preset = p }

		t.Run("v1.14 "+p+" drops the flannel document", func(t *testing.T) {
			out := renderFor(t, versionContract, set)
			requireNone(t, out, docFlannel)
			requireAll(t, out, docKubeNetwork)
		})

		t.Run("v1.13 "+p+" sets cni none", func(t *testing.T) {
			requireAll(t, renderFor(t, contractV113, set), legacyCNINone)
		})
	}

	t.Run("a worker has no flannel document", func(t *testing.T) {
		out := renderFor(t, versionContract, func(_ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine) {
			m.Spec.Role = roleWorker
		})
		requireNone(t, out, docFlannel)
	})
}

func TestRenderRefusesAPresetOtherThanCilium(t *testing.T) {
	for _, p := range []string{"flannel", "custom"} {
		c := testCluster("true")
		c.Spec.CNI.Preset = p

		_, err := BuildConfig(c, testMachine("node-a", roleControlPlane, nil), testBundle(t), nil, nil)
		if !errors.Is(err, ErrCNIUnsupported) {
			t.Errorf("preset %q: err = %v, want ErrCNIUnsupported", p, err)
		}
	}
}

func TestInstallTargetFollowsTheContract(t *testing.T) {
	install := func(d disk, image string) mutation {
		return func(_ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine) {
			m.Spec.Install = &ichorv1alpha1.TalosMachineInstall{Disk: d, Image: image}
		}
	}

	const image = "factory.talos.dev/metal-installer/abc:v1.14.3"

	t.Run("no spec.install renders no install configuration at v1.14", func(t *testing.T) {
		requireNone(t, renderFor(t, versionContract, nil), docUnattended)
	})

	for _, tc := range []struct {
		name string
		d    disk
		want string
	}{
		{"wwid", disk{WWID: "naa.5000c500a1b2c3d4"}, `disk.wwid == "naa.5000c500a1b2c3d4"`},
		{"serial", disk{Serial: "S3Z9NX0M"}, `disk.serial == "S3Z9NX0M"`},
		{
			"by-id", disk{ByIDPath: "/dev/disk/by-id/nvme-X1"},
			`"/dev/disk/by-id/nvme-X1" in disk.symlinks`,
		},
		{"bus path", disk{BusPath: "/pci0000:00/x"}, `disk.bus_path == "/pci0000:00/x"`},
		{"dev path", disk{DevPath: devVDA}, `disk.dev_path == "/dev/vda"`},
	} {
		t.Run("v1.14 "+tc.name, func(t *testing.T) {
			out := renderFor(t, versionContract, install(tc.d, image))
			requireAll(t, out, docUnattended, image)
			// The CEL expression is quoted by YAML, so compare with the inner quotes escaped or not.
			if !strings.Contains(out, tc.want) && !strings.Contains(out, strings.ReplaceAll(tc.want, `"`, `\"`)) {
				t.Errorf("want selector %s in:\n%s", tc.want, out)
			}

			requireNone(t, out, legacyInstall)
		})
	}

	t.Run("v1.14 without an image still renders", func(t *testing.T) {
		out := renderFor(t, versionContract, install(disk{DevPath: devVDA}, ""))
		requireAll(t, out, docUnattended)
	})

	t.Run("v1.13 wwid", func(t *testing.T) {
		out := renderFor(t, contractV113, install(disk{WWID: "naa.1"}, image))
		requireAll(t, out, legacyInstall, "diskSelector:", "wwid: naa.1", image)
		requireNone(t, out, docUnattended)
	})

	t.Run("v1.13 serial and bus path", func(t *testing.T) {
		requireAll(t, renderFor(t, contractV113, install(disk{Serial: "S1"}, "")), "serial: S1")
		out := renderFor(t, contractV113, install(disk{BusPath: "/pci0000:00/x"}, ""))
		requireAll(t, out, "busPath: /pci0000:00/x")
	})

	t.Run("v1.13 by-id and dev path are installDisk", func(t *testing.T) {
		requireAll(t, renderFor(t, contractV113, install(disk{ByIDPath: "/dev/disk/by-id/x"}, "")),
			"disk: /dev/disk/by-id/x")
		out := renderFor(t, contractV113, install(disk{DevPath: devVDA}, ""))
		requireAll(t, out, "disk: /dev/vda")
	})

	t.Run("an empty selector is an error, not a silent no-op", func(t *testing.T) {
		c := testCluster("true")
		m := testMachine("node-a", roleControlPlane, nil)
		m.Spec.Install = &ichorv1alpha1.TalosMachineInstall{}

		if _, _, err := Render(c, m, testBundle(t), nil, nil); err == nil {
			t.Fatal("want an error for an install with no disk")
		}
	})
}

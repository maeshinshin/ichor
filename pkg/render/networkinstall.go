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
	"fmt"
	"slices"

	"github.com/siderolabs/talos/pkg/machinery/cel"
	"github.com/siderolabs/talos/pkg/machinery/cel/celenv"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/container"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	"github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ErrCNIUnsupported is returned for a cluster whose preset is not cilium. Rendering
// one would strip Talos's own flannel document and take a running flannel cluster's network
// down, so no configuration is produced at all.
var ErrCNIUnsupported = errors.New(
	"render: spec.cni.preset must be cilium: flannel and custom were removed; delete and recreate the cluster")

// cniSelectionNone reports whether an unmanaged cluster's machine config selects no CNI, the only
// CNI-related thing an unmanaged render emits.
func cniSelectionNone(cluster *ichorv1alpha1.TalosCluster) bool {
	u := cluster.Spec.CNI.Unmanaged

	return u != nil && u.TalosCNI == "none"
}

// checkCNISupported refuses a cluster whose preset is not cilium. An unset preset is the CRD
// default, cilium.
func checkCNISupported(cluster *ichorv1alpha1.TalosCluster) error {
	if p := cluster.Spec.CNI.Preset; p != "" && p != "cilium" && p != ichorv1alpha1.TalosClusterCNIPresetUnmanaged {
		return fmt.Errorf("%w (preset %q)", ErrCNIUnsupported, p)
	}

	return nil
}

// injectCNI is the CNI half of the system layer. Cilium is the only CNI, so Talos's
// own is always removed: the KubeFlannelCNIConfig document the generator adds from the v1.14
// contract, and cluster.network.cni.name: none before it.
func injectCNI(cfg coreconfig.Provider, contract *coreconfig.VersionContract) (coreconfig.Provider, error) {
	if contract.MultidocKubernetesConfigSupported() {
		docs := slices.DeleteFunc(slices.Clone(cfg.Documents()), func(d config.Document) bool {
			return d.Kind() == k8s.KubeFlannelCNIConfig
		})

		out, err := container.New(docs...)
		if err != nil {
			return nil, fmt.Errorf("render: dropping the flannel document: %w", err)
		}

		return out, nil
	}

	//nolint:staticcheck // the legacy block is the only form a pre-1.14 contract accepts
	return mergeDocument(cfg, "cni", &mv1alpha1.Config{
		ConfigVersion: mv1alpha1.Version,
		ClusterConfig: &mv1alpha1.ClusterConfig{
			ClusterNetwork: &mv1alpha1.ClusterNetworkConfig{CNI: &mv1alpha1.CNIConfig{CNIName: "none"}},
		},
	})
}

// injectInstall renders spec.install: the UnattendedInstallConfig document from the
// v1.14 contract, machine.install before it. The two cannot coexist, so exactly one is produced.
func injectInstall(
	cfg coreconfig.Provider, contract *coreconfig.VersionContract, m *ichorv1alpha1.TalosMachine,
) (coreconfig.Provider, error) {
	if m.Spec.Install == nil {
		return cfg, nil
	}

	install := m.Spec.Install

	if contract.UnattendedInstallConfig() {
		match, err := diskExpression(install.Disk)
		if err != nil {
			return nil, err
		}

		doc := runtime.NewUnattendedInstallConfigV1Alpha1()
		doc.Installer.Image = install.Image
		doc.ProvisioningSpec.Wipe = new(install.Wipe)
		doc.ProvisioningSpec.DiskSelector.Match = match

		return mergeDocument(cfg, "install", doc)
	}

	legacy := &mv1alpha1.InstallConfig{InstallImage: install.Image} //nolint:staticcheck // see below
	if install.Wipe {
		legacy.InstallWipe = new(true) //nolint:staticcheck // see below
	}

	switch d := install.Disk; {
	case d.WWID != "":
		legacy.InstallDiskSelector = &mv1alpha1.InstallDiskSelector{WWID: d.WWID} //nolint:staticcheck // see below
	case d.Serial != "":
		legacy.InstallDiskSelector = &mv1alpha1.InstallDiskSelector{Serial: d.Serial} //nolint:staticcheck // see below
	case d.BusPath != "":
		legacy.InstallDiskSelector = &mv1alpha1.InstallDiskSelector{BusPath: d.BusPath} //nolint:staticcheck // see below
	case d.ByIDPath != "":
		// The legacy block has no symlink selector, but installDisk is a path and the symlink resolves.
		legacy.InstallDisk = d.ByIDPath
	default:
		legacy.InstallDisk = d.DevPath
	}

	//nolint:staticcheck // the legacy block is the only form a pre-1.14 contract accepts
	return mergeDocument(cfg, "install", &mv1alpha1.Config{
		ConfigVersion: mv1alpha1.Version,
		MachineConfig: &mv1alpha1.MachineConfig{MachineInstall: legacy},
	})
}

// diskExpression is the v1.14 disk selector for one TalosMachineDisk, checked against Talos's own
// disk environment so a wrong field name fails here, not on the node.
func diskExpression(d ichorv1alpha1.TalosMachineDisk) (cel.Expression, error) {
	var expr string

	switch {
	case d.WWID != "":
		expr = fmt.Sprintf("disk.wwid == %q", d.WWID)
	case d.Serial != "":
		expr = fmt.Sprintf("disk.serial == %q", d.Serial)
	case d.ByIDPath != "":
		expr = fmt.Sprintf("%q in disk.symlinks", d.ByIDPath)
	case d.BusPath != "":
		expr = fmt.Sprintf("disk.bus_path == %q", d.BusPath)
	case d.DevPath != "":
		expr = fmt.Sprintf("disk.dev_path == %q", d.DevPath)
	default:
		return cel.Expression{}, fmt.Errorf("render: install.disk selects nothing")
	}

	match, err := cel.ParseBooleanExpression(expr, celenv.DiskLocator())
	if err != nil {
		return cel.Expression{}, fmt.Errorf("render: install.disk selector: %w", err)
	}

	return match, nil
}

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

	"github.com/siderolabs/talos/pkg/machinery/cel"
	"github.com/siderolabs/talos/pkg/machinery/cel/celenv"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"github.com/siderolabs/talos/pkg/machinery/config/container"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	"github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// cniPresetFlannel is the one preset whose CNI Talos itself runs. Every other preset brings its
// own CNI, so the Talos-side flannel must not exist.
const cniPresetFlannel = "flannel"

// talosRunsFlannel reports whether the cluster's CNI is Talos's own flannel. An unset preset is
// flannel, the CRD default.
func talosRunsFlannel(cluster *ichorv1alpha1.TalosCluster) bool {
	p := cluster.Spec.CNI.Preset

	return p == "" || p == cniPresetFlannel
}

// injectCNI is the CNI half of the system layer. For a preset other than flannel it
// removes Talos's own CNI: the KubeFlannelCNIConfig document the generator adds from the v1.14
// contract, and cluster.network.cni.name: none before it. A custom preset's own documents are
// patches and still win over this.
func injectCNI(
	cfg coreconfig.Provider, contract *coreconfig.VersionContract, cluster *ichorv1alpha1.TalosCluster,
) (coreconfig.Provider, error) {
	if talosRunsFlannel(cluster) {
		return cfg, nil
	}

	if contract.MultidocKubernetesConfigSupported() {
		docs := make([]config.Document, 0, len(cfg.Documents()))

		for _, d := range cfg.Documents() {
			if d.Kind() != k8s.KubeFlannelCNIConfig {
				docs = append(docs, d)
			}
		}

		out, err := container.New(docs...)
		if err != nil {
			return nil, fmt.Errorf("render: dropping the flannel document: %w", err)
		}

		return out, nil
	}

	//nolint:staticcheck // the legacy block is the only form a pre-1.14 contract accepts
	return mergeDocument(cfg, &mv1alpha1.Config{
		ConfigVersion: mv1alpha1.Version,
		ClusterConfig: &mv1alpha1.ClusterConfig{
			ClusterNetwork: &mv1alpha1.ClusterNetworkConfig{CNI: &mv1alpha1.CNIConfig{CNIName: "none"}},
		},
	}, "cni")
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
		doc.ProvisioningSpec.Wipe = new(false)
		doc.ProvisioningSpec.DiskSelector.Match = match

		return mergeDocument(cfg, doc, "install")
	}

	legacy := &mv1alpha1.InstallConfig{InstallImage: install.Image} //nolint:staticcheck // see below

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
	return mergeDocument(cfg, &mv1alpha1.Config{
		ConfigVersion: mv1alpha1.Version,
		MachineConfig: &mv1alpha1.MachineConfig{MachineInstall: legacy},
	}, "install")
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

// mergeDocument strategic-merges one document into cfg, the way injectKubeProxy does.
func mergeDocument(cfg coreconfig.Provider, doc config.Document, what string) (coreconfig.Provider, error) {
	c, err := container.New(doc)
	if err != nil {
		return nil, fmt.Errorf("render: %s: %w", what, err)
	}

	out, err := configpatcher.Apply(configpatcher.WithConfig(cfg),
		[]configpatcher.Patch{configpatcher.NewStrategicMergePatch(c)})
	if err != nil {
		return nil, fmt.Errorf("render: applying %s: %w", what, err)
	}

	return out.Config()
}

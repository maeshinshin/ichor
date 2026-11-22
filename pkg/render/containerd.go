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

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/types/cri"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// criCustomizationDeviceOwnership names the CRICustomizationConfig for
// spec.containerd.deviceOwnershipFromSecurityContext; Talos merges customizations by name.
const criCustomizationDeviceOwnership = "ichor-device-ownership"

// deviceOwnershipFragment is the whole TOML Ichor writes: containerd 2.x, configuration version 3.
const deviceOwnershipFragment = `[plugins."io.containerd.cri.v1.runtime"]
  device_ownership_from_security_context = true
`

// DeviceOwnershipEnabled reports spec.containerd.deviceOwnershipFromSecurityContext.
func DeviceOwnershipEnabled(c *ichorv1alpha1.TalosCluster) bool {
	return c.Spec.Containerd != nil && c.Spec.Containerd.DeviceOwnershipFromSecurityContext
}

// ValidateContainerd checks spec.containerd; the admission webhook and Render both call it.
func ValidateContainerd(c *ichorv1alpha1.TalosCluster) error {
	if !DeviceOwnershipEnabled(c) {
		return nil
	}

	contract, err := coreconfig.ParseContractFromVersion(c.Spec.VersionContract)
	if err != nil {
		return err
	}

	if !contract.MultidocKubernetesConfigSupported() {
		return errors.New("spec.containerd.deviceOwnershipFromSecurityContext needs versionContract v1.14 or later")
	}

	return nil
}

// injectContainerd renders spec.containerd as a system-layer CRICustomizationConfig on every node;
// unset or false returns cfg untouched.
func injectContainerd(cfg coreconfig.Provider, cluster *ichorv1alpha1.TalosCluster) (coreconfig.Provider, error) {
	if !DeviceOwnershipEnabled(cluster) {
		return cfg, nil
	}

	if err := ValidateContainerd(cluster); err != nil {
		return nil, err
	}

	doc := cri.NewCRICustomizationConfigV1Alpha1(criCustomizationDeviceOwnership)
	doc.CustomizationContent = deviceOwnershipFragment

	return mergeDocument(cfg, "containerd device ownership", doc)
}

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
	"path/filepath"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	"github.com/siderolabs/talos/pkg/machinery/config/types/meta"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	apimeta "k8s.io/apimachinery/pkg/api/meta"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	// kubeletCAFlag is the kube-apiserver flag that makes it verify a kubelet's serving certificate.
	kubeletCAFlag = "kubelet-certificate-authority"

	serverTLSBootstrapField = "serverTLSBootstrap"
)

// KubeletServingEnabled reports spec.security.kubeletServingCertificates.enabled.
func KubeletServingEnabled(c *ichorv1alpha1.TalosCluster) bool {
	return c.Spec.Security != nil && c.Spec.Security.KubeletServingCertificates != nil &&
		c.Spec.Security.KubeletServingCertificates.Enabled
}

// KubeletServingVerified is true when the apiserver is to verify the kubelets: asked for, and the
// controller has latched KubeletServingVerified once every machine had a certificate.
func KubeletServingVerified(c *ichorv1alpha1.TalosCluster) bool {
	return KubeletServingEnabled(c) && c.Spec.Security.KubeletServingCertificates.VerifyFromAPIServer &&
		apimeta.IsStatusConditionTrue(c.Status.Conditions, ichorv1alpha1.ConditionKubeletServingVerified)
}

// validateKubeletServing is the part of ValidateSecurity for this option; the CRD says the same.
func validateKubeletServing(c *ichorv1alpha1.TalosCluster) error {
	k := c.Spec.Security.KubeletServingCertificates
	if k != nil && k.VerifyFromAPIServer && !k.Enabled {
		return errors.New("spec.security.kubeletServingCertificates.verifyFromAPIServer needs enabled")
	}

	return nil
}

// injectKubeletServing renders spec.security.kubeletServingCertificates: serverTLSBootstrap
// on every kubelet, in the spelling the version contract takes, and on control planes, once the
// controller has latched KubeletServingVerified, the apiserver's --kubelet-certificate-authority.
func injectKubeletServing(
	cfg coreconfig.Provider, contract *coreconfig.VersionContract, cluster *ichorv1alpha1.TalosCluster,
	machineType machine.Type,
) (coreconfig.Provider, error) {
	if !KubeletServingEnabled(cluster) {
		return cfg, nil
	}

	// The kubelet takes serverTLSBootstrap from its configuration file: the standalone
	// KubeletConfig document from Talos v1.14, machine.kubelet.extraConfig before.
	tls := meta.Unstructured{Object: map[string]any{serverTLSBootstrapField: true}}

	// The apiserver is pointed at the Kubernetes CA, the file Talos gives it as its client-ca-file
	// and gives kube-controller-manager as its signing certificate, so one CA signs the kubelets'
	// certificates and verifies them.
	var ca meta.Args

	if KubeletServingVerified(cluster) && machineType == machine.TypeControlPlane {
		ca = meta.Args{kubeletCAFlag: meta.NewArgValue(filepath.Join(constants.KubernetesAPIServerSecretsDir, "ca.crt"), nil)}
	}

	if contract.MultidocKubernetesConfigSupported() {
		kubelet := k8s.NewKubeletConfigV1Alpha1()
		kubelet.KubeletConfig = tls
		docs := []config.Document{kubelet}

		if ca != nil {
			apiserver := k8s.NewKubeAPIServerConfigV1Alpha1()
			apiserver.PodArgs = ca
			docs = append(docs, apiserver)
		}

		return mergeDocument(cfg, "kubelet serving certificates", docs...)
	}

	// One legacy document carries both: a container takes a single v1alpha1.Config.
	legacy := &mv1alpha1.Config{
		ConfigVersion: mv1alpha1.Version,
		MachineConfig: &mv1alpha1.MachineConfig{
			//nolint:staticcheck // the legacy blocks are the only form a pre-1.14 contract accepts
			MachineKubelet: &mv1alpha1.KubeletConfig{KubeletExtraConfig: tls},
		},
	}

	if ca != nil {
		//nolint:staticcheck // as above
		legacy.ClusterConfig = &mv1alpha1.ClusterConfig{
			APIServerConfig: &mv1alpha1.APIServerConfig{ExtraArgsConfig: ca},
		}
	}

	return mergeDocument(cfg, "kubelet serving certificates", legacy)
}

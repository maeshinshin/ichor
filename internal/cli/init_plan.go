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

package cli

import (
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// cniPatchLayer is the TalosConfigPatch layer every patch init builds for
// the CNI or the inline-manifests bootstrap carries: "cluster", the
// broadest layer, since neither applies to just one machine.
const cniPatchLayer = "cluster"

// supportedPreset is the only topology preset this phase implements
// (architecture.md's "Bootstrap" names `--preset standard`; the roadmap's
// other presets are future work). Anything else is a clear, immediate
// error rather than a silently wrong topology.
const supportedPreset = "standard"

// initPlanInput bundles the raw flag values initCmd's RunE collects, so
// buildInitPlan is driven by plain values a test can construct directly,
// with no cobra, filesystem or network involved.
type initPlanInput struct {
	Cluster   string
	Namespace string

	Preset    string
	CNIPreset string // ichorv1alpha1.TalosClusterCNI.Preset: "flannel" or "cilium"

	EndpointType    string
	EndpointAddress string

	TalosVersion      string
	KubernetesVersion string
	VersionContract   string

	ControlPlaneCount int32
	Schedulable       string // "true" or "false": no default
}

// secretsRefName is the Secret name buildInitPlan assigns to
// TalosCluster.Spec.SecretsRef; it lives in the controller's own namespace
// once created, not necessarily Namespace above, which is just
// where this TalosCluster itself is created.
func secretsRefName(cluster string) string {
	return cluster + "-secrets"
}

// buildInitPlan implements Bootstrap step 2, "settle the shape of the
// cluster": it validates in and returns the TalosCluster object init will
// render against and create. It writes a warning (never an error) to warn
// when the endpoint type is not "vip", per architecture.md's "Design":
// "(a VIP is recommended even for one node, because changing the endpoint
// later means re-issuing certificates)" -- recommended, not required, so
// this never rejects a valid-but-not-recommended choice.
//
// Disk roles for the first node (also named in step 2) are not set here:
// TalosMachineSpec carries no disk field yet (the allow-list another unit
// is extending does not cover volumes), and pkg/render's own base config
// does not set machine.install either (its ValidateAsClient runtime mode
// explicitly sets RequiresInstall to false, "Ichor does not render
// disk/install configuration yet"). The node this command targets must
// already know its own install disk -- e.g. a `talos.install.disk=`
// kernel argument on the ISO/Image Factory image it booted -- since
// nothing on the Ichor side supplies one yet.
func buildInitPlan(in initPlanInput, warn io.Writer) (*ichorv1alpha1.TalosCluster, error) {
	if in.Preset != supportedPreset {
		return nil, fmt.Errorf("init: --preset %q is not supported yet; only %q is", in.Preset, supportedPreset)
	}

	switch in.CNIPreset {
	case "flannel", "cilium":
	default:
		return nil, fmt.Errorf("init: --cni %q is invalid: must be %q or %q", in.CNIPreset, "flannel", "cilium")
	}

	switch in.EndpointType {
	case "vip", "loadBalancer", "dns", "node":
	default:
		return nil, fmt.Errorf("init: --endpoint-type %q is invalid: must be one of vip, loadBalancer, dns, node", in.EndpointType)
	}

	if in.EndpointType != "vip" {
		_, _ = fmt.Fprintf(warn,
			"warning: --endpoint-type %q is not \"vip\". A VIP is recommended even for a single node: "+
				"changing the endpoint later means re-issuing every certificate in the cluster.\n",
			in.EndpointType)
	}

	if in.EndpointAddress == "" {
		return nil, fmt.Errorf("init: --endpoint-address is required")
	}

	switch in.ControlPlaneCount {
	case 1, 3, 5:
	default:
		return nil, fmt.Errorf("init: --control-plane-count %d is invalid: must be 1, 3 or 5", in.ControlPlaneCount)
	}

	switch in.Schedulable {
	case "true", "false":
	default:
		return nil, fmt.Errorf("init: --schedulable %q is invalid: must be %q or %q", in.Schedulable, "true", "false")
	}

	return &ichorv1alpha1.TalosCluster{
		Name: in.Cluster, Namespace: in.Namespace,
		Spec: ichorv1alpha1.TalosClusterSpec{
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: in.EndpointType, Address: in.EndpointAddress},
			TalosVersion:      in.TalosVersion,
			KubernetesVersion: in.KubernetesVersion,
			VersionContract:   in.VersionContract,
			SecretsRef:        corev1.LocalObjectReference{Name: secretsRefName(in.Cluster)},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: in.ControlPlaneCount, Schedulable: in.Schedulable},
			CNI:               ichorv1alpha1.TalosClusterCNI{Preset: in.CNIPreset, KubeProxy: "enabled"},
		},
	}, nil
}

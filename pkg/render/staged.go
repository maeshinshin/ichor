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
	"strings"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/stdpatches"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

// StagedKubernetes is the system-layer input of a Kubernetes upgrade in progress (ADR 0087). The
// apiserver always follows spec.kubernetesVersion; this pins the controller-manager, scheduler,
// kubelet and kube-proxy below it, to the version a node still runs, while the apiservers and then
// the other control-plane components are upgraded first (Kubernetes' version-skew policy: nothing
// may be newer than the API server). An empty field follows the spec. Only the Kubernetes upgrade
// executor sets it.
type StagedKubernetes struct {
	ControllerManagerVersion string
	SchedulerVersion         string
	KubeletVersion           string
	KubeProxyVersion         string
}

// injectStagedVersions pins the controller-manager, scheduler, kubelet and kube-proxy images. It
// runs below the patch layers, so a patch that sets an image explicitly still wins. The three
// control-plane components have no document on a worker.
func injectStagedVersions(
	cfg coreconfig.Provider, contract *coreconfig.VersionContract, staged *StagedKubernetes,
) (coreconfig.Provider, error) {
	if staged == nil {
		return cfg, nil
	}

	var patches []configpatcher.Patch

	add := func(b []byte, err error) error {
		if err != nil {
			return err
		}

		p, err := configpatcher.LoadPatch(b)
		if err != nil {
			return err
		}

		patches = append(patches, p)

		return nil
	}

	if v := staged.ControllerManagerVersion; v != "" && cfg.Machine().Type() != machine.TypeWorker {
		img := image(constants.KubernetesControllerManagerImage, v)
		if err := add(stdpatches.WithKubeControllerManagerImage(contract, img)); err != nil {
			return nil, fmt.Errorf("render: pinning the controller-manager image: %w", err)
		}
	}

	if v := staged.SchedulerVersion; v != "" && cfg.Machine().Type() != machine.TypeWorker {
		if err := add(stdpatches.WithKubeSchedulerImage(contract, image(constants.KubernetesSchedulerImage, v))); err != nil {
			return nil, fmt.Errorf("render: pinning the scheduler image: %w", err)
		}
	}

	if v := staged.KubeletVersion; v != "" {
		if err := add(stdpatches.WithKubeletImage(contract, image(constants.KubeletImage, v))); err != nil {
			return nil, fmt.Errorf("render: pinning the kubelet image: %w", err)
		}
	}

	if v := staged.KubeProxyVersion; v != "" && cfg.Machine().Type() != machine.TypeWorker {
		if err := add(stdpatches.WithKubeProxyImage(contract, image(constants.KubeProxyImage, v))); err != nil {
			return nil, fmt.Errorf("render: pinning the kube-proxy image: %w", err)
		}
	}

	if len(patches) == 0 {
		return cfg, nil
	}

	out, err := configpatcher.Apply(configpatcher.WithConfig(cfg), patches)
	if err != nil {
		return nil, fmt.Errorf("render: applying the pinned images: %w", err)
	}

	return out.Config()
}

// image is repository:vX.Y.Z for a version written with or without the leading v.
func image(repository, version string) string {
	return repository + ":v" + strings.TrimPrefix(version, "v")
}

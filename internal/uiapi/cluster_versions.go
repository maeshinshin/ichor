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

package uiapi

import (
	"strings"

	"connectrpc.com/connect"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/release"
)

// The names of the components a cluster is compared on. They are the wire names too.
const (
	componentTalos    = "talos"
	componentContract = "version-contract"
	componentK8s      = "kubernetes"
	componentCilium   = "cilium"
)

// releaseSetToPB is the release set this Ichor build supports.
func releaseSetToPB() *pb.ReleaseSet {
	return &pb.ReleaseSet{
		TalosVersion:       release.Pinned.Talos,
		VersionContract:    release.Pinned.VersionContract,
		KubernetesVersion:  release.Pinned.Kubernetes,
		CiliumChartVersion: release.Pinned.Cilium,
	}
}

// ciliumVersion is the installed Cilium release as far as the status shows it, and how it compares
// with the embedded chart. The status says Behind as soon as the controller sees an older release
// (status.cni.availableChartVersion); it is Unknown until a Cilium release is observed.
func ciliumVersion(c *ichorv1alpha1.TalosCluster) (string, release.State) {
	cni := c.Status.CNI
	if cni == nil {
		return "", release.Unknown
	}

	installed := strings.TrimPrefix(cni.DetectedVersion, "v")

	if cni.AvailableChartVersion != "" {
		return installed, release.Behind
	}

	if cni.Detected == "cilium" && installed != "" {
		return installed, release.Compare("v"+installed, "v"+release.Pinned.Cilium)
	}

	return "", release.Unknown
}

// componentVersionsToPB compares each component of the cluster with the release set.
func componentVersionsToPB(c *ichorv1alpha1.TalosCluster) []*pb.ComponentVersion {
	cilium, ciliumState := ciliumVersion(c)

	return []*pb.ComponentVersion{
		{Name: componentTalos, Current: c.Spec.TalosVersion, Target: release.Pinned.Talos,
			State: string(release.Compare(c.Spec.TalosVersion, release.Pinned.Talos))},
		{Name: componentContract, Current: c.Spec.VersionContract, Target: release.Pinned.VersionContract,
			State: string(release.Compare(c.Spec.VersionContract, release.Pinned.VersionContract))},
		{Name: componentK8s, Current: c.Spec.KubernetesVersion, Target: release.Pinned.Kubernetes,
			State: string(release.Compare(c.Spec.KubernetesVersion, release.Pinned.Kubernetes))},
		{Name: componentCilium, Current: cilium, Target: release.Pinned.Cilium, State: string(ciliumState)},
	}
}

// checkReleaseTarget refuses a Talos or Kubernetes target that is not the release set's version. An empty target is
// what the spec already says, and is not checked here.
func checkReleaseTarget(kind, target string) error {
	if target == "" {
		return nil
	}

	var err error

	switch kind {
	case ichorv1alpha1.TalosOperationKindTalos:
		err = release.CheckTarget("Talos", target, release.Pinned.Talos)
	case ichorv1alpha1.TalosOperationKindKubernetes:
		err = release.CheckTarget("Kubernetes", target, release.Pinned.Kubernetes)
	}

	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	return nil
}

// runningOf is what a rollout needs to know about the cluster (release.Steps).
func runningOf(c *ichorv1alpha1.TalosCluster, configPending bool) release.Running {
	_, ciliumState := ciliumVersion(c)

	return release.Running{
		Talos:           c.Spec.TalosVersion,
		VersionContract: c.Spec.VersionContract,
		Kubernetes:      c.Spec.KubernetesVersion,
		CiliumBehind:    ciliumState == release.Behind,
		ConfigPending:   configPending,
	}
}

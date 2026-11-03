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

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/release"
)

// The names of the components a cluster reports its versions for (ADR 0113). They are the wire names too.
const (
	componentTalos    = "talos"
	componentContract = "version-contract"
	componentK8s      = "kubernetes"
	componentCilium   = "cilium"
)

// releaseSetToPB is the set of versions a new install gets from this Ichor build (ADR 0115).
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

// componentVersionsToPB reports what each component of the cluster runs (ADR 0115). Talos, the
// contract and Kubernetes have no state: an upgrade is not measured against the release set, so
// they carry the running version and the set's version as the default target only. Cilium has a
// state: behind when the embedded chart is newer than the installed release.
func componentVersionsToPB(c *ichorv1alpha1.TalosCluster) []*pb.ComponentVersion {
	cilium, ciliumState := ciliumVersion(c)

	return []*pb.ComponentVersion{
		{Name: componentTalos, Current: c.Spec.TalosVersion, Target: release.Pinned.Talos},
		{Name: componentContract, Current: c.Spec.VersionContract, Target: release.Pinned.VersionContract},
		{Name: componentK8s, Current: c.Spec.KubernetesVersion, Target: release.Pinned.Kubernetes},
		{Name: componentCilium, Current: cilium, Target: release.Pinned.Cilium, State: string(ciliumState)},
	}
}

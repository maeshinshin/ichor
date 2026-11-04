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

package controller

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"

	"golang.org/x/mod/semver"
	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/imagesig"
	"github.com/maeshinshin/ichor/internal/releasecheck"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/release"
)

// The Cilium chart upgrade (ADR 0099, decisions 11 and 14). Ichor embeds one chart per release.
// CNIHandoverReconciler compares it with the chart the Helm release was deployed from and says
// so in status.cni.availableChartVersion and the CiliumUpgradeAvailable condition; the user
// starts the upgrade as a cni-kind TalosOperation with spec.cni.action upgrade, which runs the
// Cilium Helm Job (it upgrades the release to the embedded chart with the current effective
// values) and verifies the result.

const (
	reasonCiliumUpgradeAvailable = "UpgradeAvailable"
	reasonCiliumUpToDate         = "UpToDate"
)

// isCNIUpgrade reports whether op is a Cilium chart upgrade rather than a kube-proxy switch.
func isCNIUpgrade(op *ichorv1alpha1.TalosOperation) bool {
	return op.Spec.Kind == ichorv1alpha1.TalosOperationKindCNI && op.Spec.CNI != nil &&
		op.Spec.CNI.Action == ichorv1alpha1.TalosOperationCNIActionUpgrade
}

// checkChartStep refuses an upgrade from deployed to target that Cilium does not support: a
// downgrade, or more than one minor version at a time (ADR 0099, decision 14).
func checkChartStep(deployed, target string) error {
	from, to := "v"+deployed, "v"+target
	if !semver.IsValid(from) || !semver.IsValid(to) {
		return fmt.Errorf("cannot compare Cilium chart versions %q and %q", deployed, target)
	}

	if semver.Compare(to, from) < 0 {
		return fmt.Errorf("the Cilium release is at chart %s, newer than the %s this Ichor embeds; Ichor does not downgrade Cilium",
			deployed, target)
	}

	if semver.Major(from) != semver.Major(to) || minorOf(to)-minorOf(from) > 1 {
		return fmt.Errorf("the Cilium release is at chart %s and this Ichor embeds %s: Cilium supports upgrading one minor version "+
			"at a time, so this step is refused; upgrade Ichor one release at a time (each moves Cilium by at most one minor), "+
			"or run the intermediate Ichor release first", deployed, target)
	}

	return nil
}

func minorOf(v string) int {
	n, _ := strconv.Atoi(semver.MajorMinor(v)[len(semver.Major(v))+1:]) // semver.MajorMinor is "vX.Y"

	return n
}

// syncUpgradeAvailable sets status.cni.availableChartVersion and CiliumUpgradeAvailable from the
// deployed release's chart version. It writes only when something changes. A cluster with no
// deployed release record is left alone.
func (r *CNIHandoverReconciler) syncUpgradeAvailable(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) error {
	// A patch's images stop applying once the embedded chart reaches it (ADR 0115, amendment 2026-11-03):
	// the chart carries them now, so the status no longer needs them.
	if cluster.Status.CNI != nil && cluster.Status.CNI.ImageOverride != nil && cilium.ImageOverrideActive(cluster) == nil {
		orig := cluster.DeepCopy()
		cluster.Status.CNI.ImageOverride = nil

		return r.Status().Patch(ctx, cluster, client.MergeFrom(orig))
	}

	rel, err := readCiliumRelease(ctx, r.reader(), r.Namespace)
	if err != nil || rel.Status != helmStatusDeployed || rel.ChartVersion == "" {
		return err
	}

	available, cond := "", metav1.Condition{
		Type: ichorv1alpha1.ConditionCiliumUpgradeAvailable, Status: metav1.ConditionFalse, Reason: reasonCiliumUpToDate,
		Message: "the Cilium release is at the chart " + cilium.ChartVersion + " this Ichor embeds",
	}

	if rel.ChartVersion != cilium.ChartVersion {
		available = cilium.ChartVersion
		cond.Status, cond.Reason = metav1.ConditionTrue, reasonCiliumUpgradeAvailable
		cond.Message = fmt.Sprintf("Cilium chart %s is available (installed %s): run `ichor cni upgrade`", available, rel.ChartVersion)

		if err := checkChartStep(rel.ChartVersion, available); err != nil {
			cond.Message = fmt.Sprintf("Cilium chart %s is embedded (installed %s), but it cannot be applied in one step: %v",
				available, rel.ChartVersion, err)
		}
	}

	have := ""
	if cluster.Status.CNI != nil {
		have = cluster.Status.CNI.AvailableChartVersion
	}

	if old := apimeta.FindStatusCondition(cluster.Status.Conditions, cond.Type); have == available && old != nil &&
		old.Status == cond.Status && old.Message == cond.Message {
		return nil
	}

	if cluster.Status.CNI == nil {
		cluster.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{}
	}

	cluster.Status.CNI.AvailableChartVersion = available
	apimeta.SetStatusCondition(&cluster.Status.Conditions, cond)

	return r.Status().Update(ctx, cluster)
}

// cniUpgradeOperationClusters maps a cni-kind operation to its cluster, so the end of an upgrade
// clears the available version without waiting for another event.
func cniUpgradeOperationClusters(_ context.Context, obj client.Object) []reconcile.Request {
	op, ok := obj.(*ichorv1alpha1.TalosOperation)
	if !ok || op.Spec.Kind != ichorv1alpha1.TalosOperationKindCNI {
		return nil
	}

	var req reconcile.Request

	req.Namespace, req.Name = op.Namespace, op.Spec.ClusterRef.Name

	return []reconcile.Request{req}
}

// machineless reports whether op's executor touches no machine (an Ichor self-upgrade, a Cilium
// chart upgrade), so the operation carries no machine list.
func machineless(op *ichorv1alpha1.TalosOperation) bool {
	return op.Spec.Kind == ichorv1alpha1.TalosOperationKindIchor || isCNIUpgrade(op)
}

// upgradeChart is the executor of a cni upgrade operation: it runs the Cilium Helm Job (the
// backbone holds the cluster operation lock), then verifies that the release is deployed at the
// target chart and the agents have rolled out. A failed Job is an error, which pauses the
// operation with the Job's reason; Helm's atomic upgrade has already rolled back.
func (a *cniKubeProxyAdvancer) upgradeChart(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, op *ichorv1alpha1.TalosOperation,
) (bool, error) {
	// A patch (ADR 0115, amendment 2026-11-03) runs this chart with the patch's verified images. The
	// Job gets them from a copy of the cluster, so the spec is untouched; the status records them once
	// the rollout has finished, and every later Job keeps them.
	var override *ichorv1alpha1.TalosCiliumImageOverride

	job := cluster

	if op.Spec.CNI.ImageVersion != "" {
		var err error

		if override, err = a.patchOverride(ctx, cluster, op.Spec.CNI.ImageVersion); err != nil {
			return false, err
		}

		job = cluster.DeepCopy()
		if job.Status.CNI == nil {
			job.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{}
		}

		job.Status.CNI.ImageOverride = override
	}

	done, err := a.upgradeChartRelease(ctx, job, op, override != nil)
	if err != nil || !done {
		return false, err
	}

	if override == nil {
		return true, nil
	}

	return true, a.recordImageOverride(ctx, cluster, override)
}

// upgradeChartRelease runs the Helm Job that moves the release to the embedded chart, with the cluster's
// current Cilium values (which carry a patch's images when job has one), and waits for the rollout. With
// patch set, a deployed release counts as done only when its values are the ones the Job would apply.
func (a *cniKubeProxyAdvancer) upgradeChartRelease(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, op *ichorv1alpha1.TalosOperation, patch bool,
) (bool, error) {
	target := op.Spec.CNI.ChartVersion
	if target != cilium.ChartVersion {
		return false, fmt.Errorf("the operation upgrades to Cilium chart %s but this controller embeds %s (Ichor was changed "+
			"after the operation was created); delete the operation and run `ichor cni upgrade` again", target, cilium.ChartVersion)
	}

	rel, err := readCiliumRelease(ctx, a.Reader, a.ControllerNamespace)
	if err != nil {
		return false, err
	}

	if rel.Status == "" {
		return false, errors.New("there is no Helm release of Cilium yet: wait for the hand-over to finish")
	}

	// The upgrade reproduces the mode Cilium runs now, not a spec edit no switch has applied.
	mode, err := currentKubeProxyMode(ctx, a.Reader, cluster)
	if err != nil {
		return false, err
	}

	if rel.Status == helmStatusDeployed && rel.ChartVersion == target {
		nodes, err := clusterNodeCount(ctx, a.Reader)
		if err != nil {
			return false, err
		}

		hash, err := CNIValuesHash(cluster, mode, nodes)
		if err != nil {
			return false, err
		}

		if !patch || rel.ValuesHash == hash {
			return a.ciliumRolledOut(ctx, op)
		}
	}

	if rel.ChartVersion != "" {
		if err := checkChartStep(rel.ChartVersion, target); err != nil {
			return false, err
		}
	}

	// The retry request is part of the name: a failed Job stays for a day, and a retry must run a new one.
	salt := "/upgrade/" + string(op.UID)
	if op.Status.LastRetry != nil {
		salt += "/" + strconv.FormatInt(op.Status.LastRetry.Unix(), 10)
	}

	if done, err := a.runCiliumJob(ctx, cluster, mode, op, salt,
		"upgrading the Cilium release to chart "+target,
		fmt.Sprintf("the Cilium Helm Job upgrading to chart %s is running", target)); err != nil || !done {
		return false, err
	}

	if rel.Status != helmStatusDeployed || rel.ChartVersion != target {
		return false, waitOn(op, reasonCiliumNotSwitched, fmt.Sprintf(
			"the Cilium Helm Job finished but the release is %q at chart %q, not deployed at %s yet", rel.Status, rel.ChartVersion, target))
	}

	return a.ciliumRolledOut(ctx, op)
}

// patchOverride is the verified image set of a Cilium patch the operation names (ADR 0115, amendment
// 2026-11-03). The patch must be newer than the embedded chart, compatible with the cluster's Kubernetes,
// and every image it runs must verify against the Cilium release workflow of its tag; otherwise nothing runs.
func (a *cniKubeProxyAdvancer) patchOverride(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, version string,
) (*ichorv1alpha1.TalosCiliumImageOverride, error) {
	if a.Images == nil {
		return nil, errors.New("this controller has no image verifier, so it runs no Cilium patch")
	}

	if !semver.IsValid(version) || semver.Compare("v"+cilium.ChartVersion, version) >= 0 {
		return nil, fmt.Errorf("the Cilium patch %s is not newer than the chart %s this Ichor embeds", version, cilium.ChartVersion)
	}

	if err := release.CiliumKubernetesError(version, cluster.Spec.KubernetesVersion); err != nil {
		return nil, fmt.Errorf("the Cilium patch %s does not run the cluster's Kubernetes: %w", version, err)
	}

	base := path.Dir(releasecheck.DefaultCiliumRepository)
	repos := map[string]string{
		cilium.ImageAgent:       releasecheck.DefaultCiliumRepository,
		cilium.ImageOperator:    base + "/operator-generic",
		cilium.ImageHubbleRelay: base + "/hubble-relay",
	}

	images := make(map[string]string, len(repos))

	for key, repo := range repos {
		digest, err := a.Images.Digest(ctx, repo, version)
		if err != nil {
			return nil, fmt.Errorf("resolving the %s image of Cilium %s: %w", key, version, err)
		}

		if err := a.Images.Verify(ctx, imagesig.Image{Repository: repo, Digest: digest}, imagesig.CiliumIdentity(version)); err != nil {
			return nil, fmt.Errorf("the %s image of Cilium %s is not signed by its release workflow, so it is not run: %w", key, version, err)
		}

		images[key] = repo + ":" + version + "@" + digest
	}

	return &ichorv1alpha1.TalosCiliumImageOverride{Version: version, Images: images}, nil
}

// recordImageOverride writes the patch the rollout ran to status.cni.imageOverride (controller-written
// status; the spec is unchanged).
func (a *cniKubeProxyAdvancer) recordImageOverride(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, override *ichorv1alpha1.TalosCiliumImageOverride,
) error {
	if cluster.Status.CNI != nil && equality.Semantic.DeepEqual(cluster.Status.CNI.ImageOverride, override) {
		return nil
	}

	orig := cluster.DeepCopy()
	if cluster.Status.CNI == nil {
		cluster.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{}
	}

	cluster.Status.CNI.ImageOverride = override

	if err := a.Client.Status().Patch(ctx, cluster, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("recording the Cilium patch %s in the status: %w", override.Version, err)
	}

	return nil
}

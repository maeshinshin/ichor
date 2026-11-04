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
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
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
	reasonCiliumPatchDropped     = "CiliumPatchDropped"
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
	if err := r.dropPatchImages(ctx, cluster); err != nil {
		return err
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

// dropPatchImages clears status.cni.imageOverride, the Cilium patch an earlier build recorded (ADR 0115,
// amendment 2026-11-04 (3)). Ichor renders only the images of the chart it embeds now, so the next Helm Job
// returns the release to them. When the patch is newer than the embedded chart that is a step back within the
// minor, and the cluster's Events say so, because nothing else would: the Job runs on its own once the values
// hash moves.
func (r *CNIHandoverReconciler) dropPatchImages(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) error {
	if cluster.Status.CNI == nil || cluster.Status.CNI.ImageOverride == nil {
		return nil
	}

	o := cluster.Status.CNI.ImageOverride
	orig := cluster.DeepCopy()
	cluster.Status.CNI.ImageOverride = nil

	if err := r.Status().Patch(ctx, cluster, client.MergeFrom(orig)); err != nil {
		return err
	}

	if r.Recorder != nil && semver.Compare(o.Version, "v"+cilium.ChartVersion) > 0 {
		r.Recorder.Eventf(cluster, corev1.EventTypeWarning, reasonCiliumPatchDropped,
			"Cilium %s ran on this cluster from an earlier Ichor. This Ichor embeds chart %s and runs only its images, "+
				"so the Cilium Helm Job returns the release to them. Upgrade Ichor to a release that embeds Cilium %s or newer to keep that version.",
			o.Version, cilium.ChartVersion, strings.TrimPrefix(o.Version, "v"))
	}

	return nil
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
	// An operation of an earlier build may name a Cilium patch: Ichor runs only the chart it embeds now
	// (ADR 0115, amendment 2026-11-04 (3)), so such an operation is refused rather than run as the chart.
	if op.Spec.CNI.ImageVersion != "" {
		return false, fmt.Errorf("the operation names the Cilium patch %s, which Ichor no longer runs: Cilium moves only to the chart "+
			"this Ichor embeds (%s); delete the operation and run `ichor cni upgrade` again", op.Spec.CNI.ImageVersion, cilium.ChartVersion)
	}

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
		return a.ciliumRolledOut(ctx, op)
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

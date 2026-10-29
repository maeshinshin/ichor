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
	"fmt"
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// cniDetectInterval is how often the detected CNI is re-read; a CNI does not change often.
const cniDetectInterval = 10 * time.Minute

const (
	cniFlannel = "flannel"
	cniCalico  = "calico"
	cniOther   = "other"
	cniNone    = "none"

	reasonCNINotCilium = "CNINotCilium"

	cniNamespace = "kube-system"
)

// otherCNIDaemonSets are kube-system DaemonSets of CNIs Ichor has no special case for.
var otherCNIDaemonSets = []string{"weave-net", "kube-router", "antrea-agent", "aws-node", "canal", "kindnet", "ovnkube-node"}

// CNIDetectReconciler sets status.cni.detected and detectedVersion from the
// DaemonSets in kube-system, and the NetworkPoliciesApplied condition that tells the UI and CLI
// Ichor's CiliumNetworkPolicies are skipped. It only reads the workload cluster and writes only
// the TalosCluster's status, for every preset.
type CNIDetectReconciler struct {
	client.Client

	// APIReader lists the DaemonSets live; falls back to Client when nil.
	APIReader client.Reader
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list

// Reconcile re-reads the CNI and records it, then comes back in cniDetectInterval. The
// NetworkPoliciesApplied condition is False when the CNI is not Cilium and absent when it is
// (whether the policies are really applied also depends on the CRD and the chart value, which a
// True here could not promise).
func (r *CNIDetectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cluster ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var reader client.Reader = r.Client
	if r.APIReader != nil {
		reader = r.APIReader
	}

	var list appsv1.DaemonSetList
	if err := reader.List(ctx, &list, client.InNamespace(cniNamespace)); err != nil {
		return ctrl.Result{}, err
	}

	detected, version := DetectCNI(list.Items)
	orig := cluster.DeepCopy()

	if cluster.Status.CNI == nil {
		cluster.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{}
	}

	cluster.Status.CNI.Detected = detected
	cluster.Status.CNI.DetectedVersion = version

	if detected == ciliumName {
		apimeta.RemoveStatusCondition(&cluster.Status.Conditions, ichorv1alpha1.ConditionNetworkPoliciesApplied)
	} else {
		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: ichorv1alpha1.ConditionNetworkPoliciesApplied, Status: metav1.ConditionFalse, Reason: reasonCNINotCilium,
			Message: fmt.Sprintf("Ichor's network policies are not applied: the CNI is %s", detected),
		})
	}

	res := ctrl.Result{RequeueAfter: cniDetectInterval}

	if apiequality.Semantic.DeepEqual(orig.Status, cluster.Status) {
		return res, nil
	}

	return res, r.Status().Patch(ctx, &cluster, client.MergeFrom(orig))
}

// DetectCNI names the CNI the kube-system DaemonSets say is running, and the image tag of its
// DaemonSet. One family gives cilium, flannel, calico or (a CNI Ichor knows by name only) other;
// none gives none. Two families (a migration in progress, or canal) give other with no version.
func DetectCNI(sets []appsv1.DaemonSet) (detected, version string) {
	found := map[string]*appsv1.DaemonSet{}

	for i := range sets {
		name := sets[i].Name

		switch {
		case sets[i].Namespace != "" && sets[i].Namespace != cniNamespace:
		case name == ciliumName:
			found[ciliumName] = &sets[i]
		case strings.Contains(name, cniFlannel):
			found[cniFlannel] = &sets[i]
		case name == "calico-node":
			found[cniCalico] = &sets[i]
		case slices.Contains(otherCNIDaemonSets, name):
			found[cniOther] = &sets[i]
		}
	}

	switch len(found) {
	case 0:
		return cniNone, ""
	case 1:
		for kind, ds := range found {
			return kind, daemonSetImageTag(ds)
		}
	}

	return cniOther, ""
}

// daemonSetImageTag is the tag of the DaemonSet's first container image (a digest suffix is dropped).
func daemonSetImageTag(ds *appsv1.DaemonSet) string {
	cs := ds.Spec.Template.Spec.Containers
	if len(cs) == 0 {
		return ""
	}

	img, _, _ := strings.Cut(cs[0].Image, "@")

	i := strings.LastIndex(img, ":")
	if i < 0 || strings.Contains(img[i:], "/") {
		return ""
	}

	return img[i+1:]
}

// detectedCNI is status.cni.detected, empty before the first detection.
func detectedCNI(cluster *ichorv1alpha1.TalosCluster) string {
	if cluster == nil || cluster.Status.CNI == nil {
		return ""
	}

	return cluster.Status.CNI.Detected
}

// SetupWithManager sets up the controller with the Manager. Status writes (including its own)
// do not re-trigger it; the requeue does.
func (r *CNIDetectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.TalosCluster{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("cnidetect").
		Complete(r)
}

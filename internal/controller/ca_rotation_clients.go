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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/maeshinshin/ichor/internal/oidctrust"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// After the old CAs are dropped, CoreDNS, Cilium and kube-proxy keep verifying the API server against
// the CA they loaded at start (they do not recover by themselves), and ichor-ui
// re-reads it slowly. A rotation therefore restarts them once. It is a write to
// those workloads, in scope the way the restore's one-time pod restart is: Ichor does not operate the
// CNI, it recovers from its own action.

// +kubebuilder:rbac:groups=apps,resources=daemonsets,resourceNames=kube-proxy;cilium,verbs=get;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,resourceNames=coredns;cilium-operator,verbs=get;patch
// ichor-ui is found by label (kustomize prefixes its name), which needs list; patch then cannot be pinned to a name.
// +kubebuilder:rbac:groups=apps,namespace=ichor-system,resources=deployments,verbs=get;list;patch

const (
	reasonCARotationRestarting = "RestartingClients"

	// caRestartMarker is set on a restarted workload's pod template to the operation that restarted it:
	// a controller that dies after the patch finds it and does not restart the workload twice.
	caRestartMarker = "ichor.maesh.dev/ca-rotation"

	restartedAtAnnotation = "kubectl.kubernetes.io/restartedAt"

	caClientPoll = 10 * time.Second

	kindDeployment = "Deployment"
	kindDaemonSet  = "DaemonSet"
	corednsName    = "coredns"

	ciliumOperatorName = "cilium-operator"
)

// caClientRestartTimeout is how long the restarted workloads get to become available again.
// A variable so tests need not wait.
var caClientRestartTimeout = 5 * time.Minute

// caClient is one workload that is restarted.
type caClient struct {
	kind, namespace, name string
}

func (a caClient) String() string { return a.kind + " " + a.namespace + "/" + a.name }

// restartTargets is the restart order: the CNI first (the cilium agents, then cilium-operator), then
// kube-proxy, CoreDNS, ichor-ui. With the old CA an agent cannot reach the API, goes not Ready and
// Cilium taints the Nodes node.cilium.io/agent-not-ready:NoSchedule, so a CoreDNS restarted before it
// stays Pending and the sequence never gets to the CNI (CNI-first amendment). A cluster
// whose preset is unmanaged has a CNI Ichor does not know: it restarts none (the completion note says so).
func (c *caRun) restartTargets(ctx context.Context) ([]caClient, error) {
	var targets []caClient

	if c.cluster.Spec.CNI.Preset != ichorv1alpha1.TalosClusterCNIPresetUnmanaged {
		targets = append(targets, caClient{kindDaemonSet, kubeSystemNamespace, ciliumName},
			caClient{kindDeployment, kubeSystemNamespace, ciliumOperatorName})
	}

	targets = append(targets, caClient{kindDaemonSet, kubeSystemNamespace, kubeProxyName},
		caClient{kindDeployment, kubeSystemNamespace, corednsName})

	// ichor-ui, found by the label every install path sets; none installed is fine.
	list, err := c.r.KubeClient.AppsV1().Deployments(c.r.ControllerNamespace).List(ctx,
		metav1.ListOptions{LabelSelector: oidctrust.UIComponentLabel + "=" + oidctrust.UIComponentValue})
	if err != nil {
		return nil, fmt.Errorf("finding the ichor-ui Deployment: %w", err)
	}

	for _, d := range list.Items {
		targets = append(targets, caClient{kindDeployment, c.r.ControllerNamespace, d.Name})
	}

	return targets, nil
}

// restartClients restarts, once and one at a time (the CNI, kube-proxy, CoreDNS, ichor-ui), the in-cluster
// clients known to keep the old CA, and waits (bounded) for each before the next. The caller runs it only
// after the replaced bundle is verified and every Node is Ready. done is false while the rotation must wait; a workload that is not back after the timeout
// is named in the returned note and does not hold the rotation.
func (c *caRun) restartClients(ctx context.Context) (res ctrl.Result, note string, done bool, err error) {
	if c.r.KubeClient == nil {
		return ctrl.Result{}, "", true, nil
	}

	token := string(c.op.UID)
	var pending []string

	targets, err := c.restartTargets(ctx)
	if err != nil {
		return ctrl.Result{RequeueAfter: caClientPoll}, "", false, err
	}

	// One at a time, in order: a workload is restarted only once the one before it is available again,
	// so DNS and the proxy are never down together. On a wait, the later ones are not touched.
	for i, t := range targets {
		ready, rerr := c.restartOne(ctx, t, token)
		if rerr == nil && ready {
			continue
		}

		if rerr != nil {
			pending = append(pending, fmt.Sprintf("%s (%v)", t, rerr))
		} else {
			pending = append(pending, t.String())
		}

		for _, later := range targets[i+1:] {
			pending = append(pending, later.String()+" (not restarted yet)")
		}

		break
	}

	if len(pending) == 0 {
		return ctrl.Result{}, "", true, nil
	}

	cond := apimeta.FindStatusCondition(c.op.Status.Conditions, conditionCARotation)
	if cond == nil || cond.Reason != reasonCARotationRestarting || cond.Status != metav1.ConditionTrue {
		// Start the clock: a removed condition gets a fresh transition time.
		apimeta.RemoveStatusCondition(&c.op.Status.Conditions, conditionCARotation)
	} else if time.Since(cond.LastTransitionTime.Time) > caClientRestartTimeout {
		return ctrl.Result{}, "; not back after the restart: " + strings.Join(pending, ", "), true, nil
	}

	err = c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress, metav1.ConditionTrue, reasonCARotationRestarting,
		"the old CAs are dropped; waiting for the clients restarted to load the new one: "+strings.Join(pending, ", "))

	return ctrl.Result{RequeueAfter: caClientPoll}, "", false, err
}

// restartOne patches t's pod template unless it already carries token, and reports whether the rollout
// is available. A t that does not exist counts as ready: there is nothing to wait for.
func (c *caRun) restartOne(ctx context.Context, t caClient, token string) (ready bool, err error) {
	kc := c.r.KubeClient

	var (
		template  *metav1.ObjectMeta
		available bool
		patch     func([]byte) error
	)

	switch t.kind {
	case kindDeployment:
		d, gerr := kc.AppsV1().Deployments(t.namespace).Get(ctx, t.name, metav1.GetOptions{})
		if gerr != nil {
			return apierrors.IsNotFound(gerr), client.IgnoreNotFound(gerr)
		}

		template, available = &d.Spec.Template.ObjectMeta, deploymentAvailable(d)
		patch = func(b []byte) (perr error) {
			_, perr = kc.AppsV1().Deployments(t.namespace).Patch(ctx, t.name, types.StrategicMergePatchType, b, metav1.PatchOptions{})

			return perr
		}
	default:
		d, gerr := kc.AppsV1().DaemonSets(t.namespace).Get(ctx, t.name, metav1.GetOptions{})
		if gerr != nil {
			return apierrors.IsNotFound(gerr), client.IgnoreNotFound(gerr)
		}

		template, available = &d.Spec.Template.ObjectMeta, daemonSetAvailable(d)
		patch = func(b []byte) (perr error) {
			_, perr = kc.AppsV1().DaemonSets(t.namespace).Patch(ctx, t.name, types.StrategicMergePatchType, b, metav1.PatchOptions{})

			return perr
		}
	}

	if template.Annotations[caRestartMarker] == token {
		return available, nil
	}

	// The patch just made the rollout; whatever status was read describes the previous one.
	return false, patch(restartPatch(caRestartMarker, token))
}

// restartPatch is the strategic merge patch that rolls a workload once: restartedAt and the operation's
// marker on its pod template.
func restartPatch(marker, token string) []byte {
	body, _ := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{
		"annotations": map[string]string{
			restartedAtAnnotation: time.Now().UTC().Format(time.RFC3339),
			marker:                token,
		},
	}}}})

	return body
}

func deploymentAvailable(d *appsv1.Deployment) bool {
	want := int32(1)
	if d.Spec.Replicas != nil {
		want = *d.Spec.Replicas
	}

	s := d.Status

	return s.ObservedGeneration >= d.Generation && s.UpdatedReplicas == want && s.Replicas == want && s.AvailableReplicas == want
}

func daemonSetAvailable(d *appsv1.DaemonSet) bool {
	s := d.Status

	return s.ObservedGeneration >= d.Generation && s.UpdatedNumberScheduled == s.DesiredNumberScheduled &&
		s.NumberAvailable == s.DesiredNumberScheduled
}

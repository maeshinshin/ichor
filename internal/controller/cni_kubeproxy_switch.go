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

// This file implements the TalosOperationKindCNI AdvanceMachineFunc (ADR 0066): the switch of
// spec.cni.kubeProxy between enabled and replaced, in the one safe order -- new side up and
// verified on every node, then the old side down -- on top of the operation backbone (ADR 0026),
// so it has the cluster lock, pause-on-failure and a status-held state machine for free.
//
// Ichor changes the Talos side (cluster.proxy.disabled, through status.cni.kubeProxy, which
// pkg/render turns into configuration) and deletes what Talos leaves behind. It never writes the
// CNI's own configuration: that lives in a user-owned patch or, after the hand-over, in Git
// (ADR 0024), so the CNI owner changes it and this executor verifies the result.
package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/render"
)

const (
	// conditionAwaitingCNIChange is set on the TalosOperation while the CNI's owner has to change
	// the CNI's configuration (a human or Git; Ichor cannot). conditionVerifyingNewSide is set
	// while Ichor is waiting for something that needs no one: a rollout, a DaemonSet. Both are the
	// timeout clocks: True since LastTransitionTime, False once satisfied.
	conditionAwaitingCNIChange = "AwaitingCNIChange"
	conditionVerifyingNewSide  = "VerifyingNewSide"

	reasonCiliumNotSwitched = "CiliumNotSwitched"
	reasonNewSideNotReady   = "NewSideNotReady"
	reasonSideVerified      = "Verified"

	// kubeProxyVerifyTimeout bounds each automatic wait (a rollout, the DaemonSet appearing).
	kubeProxyVerifyTimeout = 30 * time.Minute

	// cniChangeTimeout bounds the wait for the CNI's owner: a pull request may sit in review.
	cniChangeTimeout = 24 * time.Hour

	ciliumConfigName = "cilium-config"
	ciliumName       = "cilium"

	// ciliumKPRKey is the cilium-config key holding the agent's kube-proxy-replacement mode.
	ciliumKPRKey = "kube-proxy-replacement"
)

// +kubebuilder:rbac:groups="",resources=configmaps,resourceNames=cilium-config,verbs=get
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters/status,verbs=get;update;patch

// KubeProxyAdmin removes what Talos leaves behind when kube-proxy is switched off (ADR 0066). A
// seam so tests never reach a cluster.
type KubeProxyAdmin interface {
	// RemoveKubeProxy deletes the kube-proxy DaemonSet, ServiceAccount, ClusterRoleBinding and
	// ConfigMaps. Idempotent: absent objects are not an error.
	RemoveKubeProxy(ctx context.Context, talos TalosClient) error
}

// cniKubeProxyAdvancer is the cni-kind AdvanceMachineFunc. cmd/main.go selects it for
// TalosOperationKindCNI only.
type cniKubeProxyAdvancer struct {
	client.Client

	// Reader is an uncached reader for ConfigMaps and DaemonSets (mgr.GetAPIReader()): the checks
	// gate a disruptive step, and caching them would add a cluster-wide ConfigMap informer.
	Reader client.Reader

	ControllerNamespace string

	TalosClient func(
		ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
	) (TalosClient, error)

	Admin KubeProxyAdmin
}

// NewCNIKubeProxyAdvancer returns the cni-kind AdvanceMachineFunc.
func NewCNIKubeProxyAdvancer(
	c client.Client, reader client.Reader, controllerNamespace string,
	talosClient func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) (TalosClient, error),
	admin KubeProxyAdmin,
) AdvanceMachineFunc {
	a := &cniKubeProxyAdvancer{Client: c, Reader: reader, ControllerNamespace: controllerNamespace, TalosClient: talosClient, Admin: admin}

	return a.Advance
}

// Advance performs one machine's step of a kube-proxy switch toward spec.cni.kubeProxy. Per call
// everything is re-derived from live state, so a restarted controller resumes where it was:
//
//  1. Replacing only: the new side (Cilium) must already serve Services on every node, or the
//     call waits. Nothing on any node has changed at that point.
//  2. The Talos side is switched (status.cni.kubeProxy) and this machine's rendered configuration
//     is applied without a reboot; the next call sees the node carrying it.
//  3. Once every other machine of the operation is done, the cluster-wide tail runs: replacing
//     deletes the kube-proxy objects; enabling verifies kube-proxy runs everywhere and then waits
//     for Cilium to step aside.
func (a *cniKubeProxyAdvancer) Advance(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, op *ichorv1alpha1.TalosOperation,
) (bool, error) {
	target := cni.KubeProxyMode(cluster.Spec.CNI.KubeProxy)
	if err := target.Validate(); err != nil {
		return false, err
	}

	preset, err := a.verifiablePreset(cluster)
	if err != nil {
		return false, err
	}

	if cluster.Status.CNI == nil || cluster.Status.CNI.KubeProxy != string(target) {
		if target == cni.KubeProxyReplaced {
			ready, err := a.requireCilium(ctx, preset, cluster, op, cni.KubeProxyReplaced)
			if err != nil || !ready {
				return false, err
			}
		}

		if err := a.setTalosSide(ctx, cluster, target); err != nil {
			return false, err
		}
	}

	bundle, err := loadBundle(ctx, a.Client, a.ControllerNamespace, cluster)
	if err != nil {
		return false, fmt.Errorf("loading secrets bundle to switch %q: %w", m.Name, err)
	}

	var patches ichorv1alpha1.TalosConfigPatchList
	if err := a.List(ctx, &patches, client.InNamespace(m.Namespace)); err != nil {
		return false, err
	}

	rendered, hash, _, err := (&TalosMachineReconciler{Client: a.Client, ControllerNamespace: a.ControllerNamespace}).
		renderMachine(ctx, cluster, m, bundle, patches.Items)
	if err != nil {
		return false, fmt.Errorf("rendering %q's configuration: %w", m.Name, err)
	}

	dialCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	talos, err := a.TalosClient(dialCtx, cluster, m, bundle)
	if err != nil {
		return false, fmt.Errorf("dialing %q: %w", m.Name, err)
	}
	defer closeTalosClient(talos)

	current, err := talos.CurrentConfig(dialCtx)
	if err != nil {
		return false, fmt.Errorf("reading %q's configuration: %w", m.Name, err)
	}

	if hashConfig(current) != hash {
		return false, a.apply(ctx, talos, m, current, rendered)
	}

	others, err := a.othersDone(ctx, cluster, m, op, target)
	if err != nil {
		return false, err
	}

	if !others {
		return true, nil
	}

	if target == cni.KubeProxyReplaced {
		return a.removeKubeProxy(ctx, cluster, m, bundle, talos)
	}

	return a.enableTail(ctx, preset, cluster, op)
}

// verifiablePreset returns the preset whose live kube-proxy state Ichor can read. Only Cilium is
// readable today; a switch it cannot verify is refused rather than done on faith.
func (a *cniKubeProxyAdvancer) verifiablePreset(cluster *ichorv1alpha1.TalosCluster) (cni.Preset, error) {
	preset, ok := cniPresetFor(cluster.Spec.CNI.Preset)
	if !ok || cluster.Spec.CNI.Preset != "cilium" || !preset.SupportsKubeProxyReplacement() {
		return nil, fmt.Errorf("the %q CNI preset is not one whose live kube-proxy state Ichor can read, so it "+
			"cannot verify a kube-proxy switch; refusing to run one", cluster.Spec.CNI.Preset)
	}

	return preset, nil
}

// setTalosSide records the Talos-side mode on status. A merge patch, so a concurrent status write
// by another controller is not a conflict that would pause the operation.
func (a *cniKubeProxyAdvancer) setTalosSide(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, target cni.KubeProxyMode,
) error {
	base := cluster.DeepCopy()
	cluster.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{KubeProxy: string(target)}

	if err := a.Status().Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("recording the Talos-side kube-proxy mode: %w", err)
	}

	return nil
}

// apply sends the rendered configuration without a reboot, after checking that the only thing it
// changes is kube-proxy's mode: a pending unrelated change is never swept into a switch.
func (a *cniKubeProxyAdvancer) apply(
	ctx context.Context, talos TalosClient, m *ichorv1alpha1.TalosMachine, current, rendered []byte,
) error {
	paths, err := ChangedPaths(current, rendered)
	if err != nil {
		return fmt.Errorf("comparing %q's configuration: %w", m.Name, err)
	}

	for _, p := range paths {
		if !strings.HasPrefix(p, "KubeProxyConfig") && !strings.HasPrefix(p, "cluster.proxy") {
			return fmt.Errorf("%q also has an unrelated pending configuration change at %q that a kube-proxy "+
				"switch will not apply; converge it first", m.Name, p)
		}
	}

	// Each call has its own deadline: sharing the 15s dial bound would starve the apply.
	dryRunCtx, cancelDryRun := context.WithTimeout(ctx, etcdDialTimeout)
	_, err = talos.DryRunApply(dryRunCtx, rendered)

	cancelDryRun()

	if err != nil {
		return fmt.Errorf("dry-run apply to %q failed: %w", m.Name, err)
	}

	applyCtx, cancelApply := context.WithTimeout(ctx, upgradeCallTimeout)
	defer cancelApply()

	if err := talos.ApplyNoReboot(applyCtx, rendered); err != nil {
		return fmt.Errorf("applying the kube-proxy mode to %q: %w", m.Name, err)
	}

	return nil
}

// othersDone reports whether every other machine of the operation has finished. It is not a
// control-plane count (nothing is gated on how many there are), but it does decide when the
// cluster-wide tail may run, so it reads through the uncached Reader: a machine approved a moment
// ago must not be missed, or kube-proxy could be removed before that machine was switched. When
// replacing, a held control plane is an error: it was not switched, so its Talos would re-apply the
// DaemonSet the tail deletes.
func (a *cniKubeProxyAdvancer) othersDone(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, self *ichorv1alpha1.TalosMachine,
	op *ichorv1alpha1.TalosOperation, target cni.KubeProxyMode,
) (bool, error) {
	list, err := deleteguard.ApprovedMachines(ctx, a.Reader, cluster.Namespace, cluster.Name, deleteguard.MachineFilter{})
	if err != nil {
		return false, err
	}

	done := true

	for i := range list {
		o := &list[i]
		if o.Name == self.Name {
			continue
		}

		if o.Annotations[ichorv1alpha1.TalosMachineUpgradeHoldAnnotation] != "" {
			if target == cni.KubeProxyReplaced && o.Spec.Role == roleControlPlane {
				return false, fmt.Errorf("control plane %q is excluded by its upgrade-hold annotation, so it still renders "+
					"kube-proxy and its Talos would re-apply what this switch removes; release the hold and create the "+
					"operation again", o.Name)
			}

			continue
		}

		if op == nil || !machineCompleted(op, o.Name) {
			done = false
		}
	}

	return done, nil
}

func machineCompleted(op *ichorv1alpha1.TalosOperation, name string) bool {
	for _, rec := range op.Status.Machines {
		if rec.Name == name && rec.Phase == ichorv1alpha1.TalosOperationMachinePhaseCompleted {
			return true
		}
	}

	return false
}

// removeKubeProxy is the replacing tail: Talos stopped managing the objects but does not delete
// them (ADR 0066), so Ichor does, and reports done once the DaemonSet is gone.
//
// Only a control plane's Talos API issues an admin kubeconfig, and a cluster with workers can finish
// on one, so the removal goes through a control plane's client when m is not one.
func (a *cniKubeProxyAdvancer) removeKubeProxy(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle, talos TalosClient,
) (bool, error) {
	if m.Spec.Role != roleControlPlane {
		cps, err := deleteguard.ApprovedMachines(ctx, a.Reader, cluster.Namespace, cluster.Name,
			deleteguard.MachineFilter{Role: roleControlPlane})
		if err != nil {
			return false, err
		}

		if len(cps) == 0 {
			return false, errors.New("no approved control plane to fetch an admin kubeconfig from")
		}

		dialCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
		defer cancel()

		cp, err := a.TalosClient(dialCtx, cluster, &cps[0], bundle)
		if err != nil {
			return false, fmt.Errorf("dialing control plane %q to remove kube-proxy: %w", cps[0].Name, err)
		}
		defer closeTalosClient(cp)

		talos = cp
	}

	if err := a.Admin.RemoveKubeProxy(ctx, talos); err != nil {
		return false, fmt.Errorf("removing kube-proxy: %w", err)
	}

	var ds appsv1.DaemonSet

	err := a.Reader.Get(ctx, types.NamespacedName{Namespace: kubeSystemNamespace, Name: kubeProxyName}, &ds)
	if apierrors.IsNotFound(err) {
		return true, nil
	}

	return false, err
}

// enableTail is the enabling tail: kube-proxy runs on every node first (it is the new side), and
// only then is Cilium asked to step aside.
func (a *cniKubeProxyAdvancer) enableTail(
	ctx context.Context, preset cni.Preset, cluster *ichorv1alpha1.TalosCluster, op *ichorv1alpha1.TalosOperation,
) (bool, error) {
	var ds appsv1.DaemonSet

	err := a.Reader.Get(ctx, types.NamespacedName{Namespace: kubeSystemNamespace, Name: kubeProxyName}, &ds)

	reason := ""

	switch {
	case apierrors.IsNotFound(err):
		reason = "the kube-proxy DaemonSet has not been created yet"
	case err != nil:
		return false, err
	case ds.Status.DesiredNumberScheduled == 0 || ds.Status.UpdatedNumberScheduled < ds.Status.DesiredNumberScheduled ||
		ds.Status.NumberReady < ds.Status.DesiredNumberScheduled:
		reason = fmt.Sprintf("kube-proxy is rolling out: %d/%d ready", ds.Status.NumberReady, ds.Status.DesiredNumberScheduled)
	}

	if reason != "" {
		return false, waitOn(op, conditionVerifyingNewSide, reasonNewSideNotReady, reason, kubeProxyVerifyTimeout)
	}

	clearWait(op, conditionVerifyingNewSide)

	return a.requireCilium(ctx, preset, cluster, op, cni.KubeProxyEnabled)
}

// requireCilium reports whether Cilium's live kube-proxy replacement matches want and its agents
// have rolled out. When they do not it waits, under the owner's clock if the CNI's configuration
// has to change and under the verification clock if only a rollout is pending.
func (a *cniKubeProxyAdvancer) requireCilium(
	ctx context.Context, preset cni.Preset, cluster *ichorv1alpha1.TalosCluster, op *ichorv1alpha1.TalosOperation,
	want cni.KubeProxyMode,
) (bool, error) {
	var cm corev1.ConfigMap

	err := a.Reader.Get(ctx, types.NamespacedName{Namespace: kubeSystemNamespace, Name: ciliumConfigName}, &cm)
	if apierrors.IsNotFound(err) {
		return false, waitOn(op, conditionVerifyingNewSide, reasonNewSideNotReady,
			"Cilium's cilium-config ConfigMap does not exist", kubeProxyVerifyTimeout)
	}

	if err != nil {
		return false, err
	}

	live := cni.LiveConfig{KubeProxyReplacement: ciliumReplaces(cm.Data[ciliumKPRKey])}
	if err := preset.CheckConsistency(ctx, live, want); err != nil {
		clearWait(op, conditionVerifyingNewSide)

		return false, waitOn(op, conditionAwaitingCNIChange, reasonCiliumNotSwitched,
			ciliumChangeInstructions(cluster, want, err), cniChangeTimeout)
	}

	clearWait(op, conditionAwaitingCNIChange)

	var ds appsv1.DaemonSet
	if err := a.Reader.Get(ctx, types.NamespacedName{Namespace: kubeSystemNamespace, Name: ciliumName}, &ds); err != nil {
		return false, fmt.Errorf("reading the cilium DaemonSet: %w", err)
	}

	if ds.Status.ObservedGeneration < ds.Generation || ds.Status.DesiredNumberScheduled == 0 ||
		ds.Status.UpdatedNumberScheduled < ds.Status.DesiredNumberScheduled ||
		ds.Status.NumberReady < ds.Status.DesiredNumberScheduled {
		return false, waitOn(op, conditionVerifyingNewSide, reasonNewSideNotReady,
			fmt.Sprintf("the Cilium agents are rolling out: %d/%d updated and ready",
				min(ds.Status.UpdatedNumberScheduled, ds.Status.NumberReady), ds.Status.DesiredNumberScheduled),
			kubeProxyVerifyTimeout)
	}

	clearWait(op, conditionVerifyingNewSide)

	return true, nil
}

// ciliumReplaces reads cilium-config's kube-proxy-replacement: "true" (and the pre-1.14 "strict")
// mean Cilium serves Services; "false", "disabled" and anything else do not.
func ciliumReplaces(value string) bool {
	return value == "true" || value == "strict"
}

// ciliumChangeInstructions says what the CNI's owner has to change and where, depending on who
// holds the CNI's configuration.
func ciliumChangeInstructions(cluster *ichorv1alpha1.TalosCluster, want cni.KubeProxyMode, mismatch error) string {
	values := "kubeProxyReplacement: false"
	if want == cni.KubeProxyReplaced {
		values = "kubeProxyReplacement: true, with k8sServiceHost: localhost and the KubePrism port as k8sServicePort"
	}

	where := "in the cni entry of cluster.inlineManifests in your cluster-layer TalosConfigPatch"
	if apimeta.IsStatusConditionTrue(cluster.Status.Conditions, render.ConditionCNIHandover) {
		where = "in the Cilium Application's Helm values in Git (Argo CD owns them since the hand-over)"
	}

	return fmt.Sprintf("%v. Ichor does not write the CNI's configuration; set %s %s", mismatch, values, where)
}

// waitOn starts or continues the clock for condType and fails once it exceeds timeout. op may be
// nil (a caller with no operation): there is then no clock and the wait is unbounded.
func waitOn(op *ichorv1alpha1.TalosOperation, condType, reason, message string, timeout time.Duration) error {
	if op == nil {
		return nil
	}

	if cond := apimeta.FindStatusCondition(op.Status.Conditions, condType); cond != nil &&
		cond.Status == metav1.ConditionTrue && time.Since(cond.LastTransitionTime.Time) > timeout {
		return fmt.Errorf("timed out after %s waiting for the new kube-proxy side: %s", timeout, message)
	}

	apimeta.SetStatusCondition(&op.Status.Conditions, metav1.Condition{
		Type: condType, Status: metav1.ConditionTrue, Reason: reason, Message: message,
	})

	return nil
}

// clearWait ends the clock for condType.
func clearWait(op *ichorv1alpha1.TalosOperation, condType string) {
	if op == nil {
		return
	}

	if cond := apimeta.FindStatusCondition(op.Status.Conditions, condType); cond != nil && cond.Status != metav1.ConditionFalse {
		apimeta.SetStatusCondition(&op.Status.Conditions, metav1.Condition{
			Type: condType, Status: metav1.ConditionFalse, Reason: reasonSideVerified, Message: "satisfied",
		})
	}
}

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

// This file implements the TalosOperationKindCNI AdvanceMachineFunc: the switch of
// spec.cni.kubeProxy between enabled and replaced, in the one safe order -- new side up and
// verified on every node, then the old side down -- on top of the operation backbone,
// so it has the cluster lock, pause-on-failure and a status-held state machine for free.
//
// Ichor changes the Talos side (cluster.proxy.disabled, through status.cni.kubeProxy, which
// pkg/render turns into configuration) and deletes what Talos leaves behind. It never writes the
// CNI's configuration directly: Ichor owns the Cilium release, so the executor runs
// the Cilium Helm Job with the target mode and verifies the result (cilium-config, the rollout).
package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/fieldmeta"
	"github.com/maeshinshin/ichor/pkg/render"
)

const (
	// conditionVerifyingNewSide is set on the TalosOperation while Ichor waits for something that
	// needs no one: the Cilium Helm Job, a rollout, a DaemonSet. It is the timeout clock: True
	// since LastTransitionTime, False once satisfied.
	conditionVerifyingNewSide = "VerifyingNewSide"

	reasonCiliumNotSwitched = "CiliumNotSwitched"
	reasonNewSideNotReady   = "NewSideNotReady"
	reasonSideVerified      = "Verified"

	// kubeProxyVerifyTimeout bounds each automatic wait (a rollout, the DaemonSet appearing).
	kubeProxyVerifyTimeout = 30 * time.Minute

	// kubeProxyProbeTimeout bounds one connection to the kubernetes Service after kube-proxy's removal.
	kubeProxyProbeTimeout = 5 * time.Second

	ciliumConfigName = "cilium-config"
	ciliumName       = "cilium"

	// ciliumKPRKey is the cilium-config key holding the agent's kube-proxy-replacement mode.
	ciliumKPRKey = "kube-proxy-replacement"
)

// +kubebuilder:rbac:groups="",resources=configmaps,resourceNames=cilium-config,verbs=get
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters/status,verbs=get;update;patch

// KubeProxyAdmin removes what Talos leaves behind when kube-proxy is switched off. A
// seam so tests never reach a cluster.
type KubeProxyAdmin interface {
	// RemoveKubeProxy deletes the kube-proxy DaemonSet, ServiceAccount, ClusterRoleBinding and
	// ConfigMaps. Idempotent: absent objects are not an error.
	RemoveKubeProxy(ctx context.Context, talos TalosClient) error

	// RestoreKubeProxy server-side-applies the kube-proxy objects of the control plane's own rendered
	// bootstrap manifests (empty until Talos rendered them). Idempotent.
	RestoreKubeProxy(ctx context.Context, talos TalosClient) error
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

	// Images pins the Cilium Job's installer image to a verified digest; nil refuses to start any Job.
	Images InstallerImages

	// ServiceProbe checks, after kube-proxy was removed, that the kubernetes Service still answers;
	// nil means probeKubernetesService.
	ServiceProbe func(ctx context.Context) error
}

// NewCNIKubeProxyAdvancer returns the cni-kind AdvanceMachineFunc.
func NewCNIKubeProxyAdvancer(
	c client.Client, reader client.Reader, controllerNamespace string,
	talosClient func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) (TalosClient, error),
	admin KubeProxyAdmin, images InstallerImages,
) AdvanceMachineFunc {
	a := &cniKubeProxyAdvancer{Client: c, Reader: reader, ControllerNamespace: controllerNamespace, TalosClient: talosClient, Admin: admin, Images: images}

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
	if cluster.Spec.CNI.Preset == ichorv1alpha1.TalosClusterCNIPresetUnmanaged {
		return false, errors.New(ichorv1alpha1.CNINotManagedMessage)
	}

	if isCNIUpgrade(op) {
		return a.upgradeChart(ctx, cluster, op)
	}

	target := cni.KubeProxyMode(cluster.Spec.CNI.KubeProxy)
	if err := target.Validate(); err != nil {
		return false, err
	}

	if err := verifiableCNI(cluster); err != nil {
		return false, err
	}

	if cluster.Status.CNI == nil || cluster.Status.CNI.KubeProxy != string(target) {
		if target == cni.KubeProxyReplaced {
			ready, err := a.requireCilium(ctx, cluster, op, cni.KubeProxyReplaced)
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

	if render.Hash(current) != hash {
		return false, a.apply(ctx, talos, cluster, m, current, rendered)
	}

	others, err := a.othersDone(ctx, cluster, m, op, target)
	if err != nil {
		return false, err
	}

	if !others {
		return true, nil
	}

	if target == cni.KubeProxyReplaced {
		return a.removeKubeProxy(ctx, cluster, m, op, bundle, talos)
	}

	return a.enableTail(ctx, cluster, m, op, bundle, talos)
}

// verifiableCNI refuses a cluster whose live kube-proxy state Ichor cannot read. Only Cilium is
// readable; a switch it cannot verify is refused rather than done on faith.
func verifiableCNI(cluster *ichorv1alpha1.TalosCluster) error {
	if cluster.Spec.CNI.Preset != ciliumName {
		return fmt.Errorf("the %q CNI preset is not one whose live kube-proxy state Ichor can read, so it "+
			"cannot verify a kube-proxy switch; refusing to run one", cluster.Spec.CNI.Preset)
	}

	return nil
}

// setTalosSide records the Talos-side mode on status. A merge patch, so a concurrent status write
// by another controller is not a conflict that would pause the operation.
func (a *cniKubeProxyAdvancer) setTalosSide(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, target cni.KubeProxyMode,
) error {
	base := cluster.DeepCopy()
	cniStatus(cluster).KubeProxy = string(target)

	if err := a.Status().Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("recording the Talos-side kube-proxy mode: %w", err)
	}

	return nil
}

// apply sends the rendered configuration without a reboot, after checking that it changes nothing
// but kube-proxy's mode and paths fieldmeta classifies as safe (applied live, no reboot, no
// API-server restart: they ride along). An unrelated reboot-requiring, restarting, install-time
// or unclassified change is never swept into a switch.
func (a *cniKubeProxyAdvancer) apply(
	ctx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	current, rendered []byte,
) error {
	paths, err := ChangedPaths(current, rendered)
	if err != nil {
		return fmt.Errorf("comparing %q's configuration: %w", m.Name, err)
	}

	contract, err := coreconfig.ParseContractFromVersion(cluster.Spec.VersionContract)
	if err != nil {
		return fmt.Errorf("invalid versionContract %q: %w", cluster.Spec.VersionContract, err)
	}

	for _, p := range paths {
		if strings.HasPrefix(p, "KubeProxyConfig") || strings.HasPrefix(p, "cluster.proxy") {
			continue
		}

		if impact := fieldmeta.Assess([]string{p}, contract); impact.RebootRequired || len(impact.InstallTimeOnly) > 0 ||
			len(impact.APIServerRestartPaths) > 0 || len(impact.Unsupported) > 0 {
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
	return slices.ContainsFunc(op.Status.Machines, func(rec ichorv1alpha1.TalosOperationMachineStatus) bool {
		return rec.Name == name && rec.Phase == ichorv1alpha1.TalosOperationMachinePhaseCompleted
	})
}

// removeKubeProxy is the replacing tail: Talos stopped managing the objects but does not delete
// them, so Ichor does, and reports done once the DaemonSet is gone.
//
// Only a control plane's Talos API issues an admin kubeconfig, and a cluster with workers can finish
// on one, so the removal goes through a control plane's client when m is not one.
func (a *cniKubeProxyAdvancer) removeKubeProxy(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, op *ichorv1alpha1.TalosOperation,
	bundle *secrets.Bundle, talos TalosClient,
) (bool, error) {
	talos, closeCP, err := a.controlPlaneClient(ctx, cluster, m, bundle, talos)
	if err != nil {
		return false, err
	}
	defer closeCP()

	if err := a.Admin.RemoveKubeProxy(ctx, talos); err != nil {
		return false, fmt.Errorf("removing kube-proxy: %w", err)
	}

	var ds appsv1.DaemonSet

	err = a.Reader.Get(ctx, types.NamespacedName{Namespace: kubeSystemNamespace, Name: kubeProxyName}, &ds)
	if err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}

	if err != nil {
		// The DaemonSet is gone; the CNI must now serve the kubernetes Service without kube-proxy. The
		// rules kube-proxy programmed stay on the nodes until they reboot.
		probe := a.ServiceProbe
		if probe == nil {
			probe = probeKubernetesService
		}

		if perr := probe(ctx); perr != nil {
			return false, waitOn(op, reasonNewSideNotReady,
				fmt.Sprintf("kube-proxy is removed but the kubernetes Service does not answer from the controller (%v); "+
					"its old iptables/nftables rules stay on each node until it reboots", perr))
		}

		clearWait(op)

		return true, nil
	}

	return false, nil
}

// probeKubernetesService opens a TCP connection to the kubernetes Service's cluster IP, the address the
// controller's own environment names. Outside a cluster (no such variables) there is nothing to probe.
func probeKubernetesService(ctx context.Context) error {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, kubeProxyProbeTimeout)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}

	return conn.Close()
}

// controlPlaneClient returns talos when m is a control plane, else a client to an approved control
// plane (only a control plane's Talos API issues an admin kubeconfig) and the func closing it.
func (a *cniKubeProxyAdvancer) controlPlaneClient(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle, talos TalosClient,
) (TalosClient, func(), error) {
	if m.Spec.Role == roleControlPlane {
		return talos, func() {}, nil
	}

	cps, err := deleteguard.ApprovedMachines(ctx, a.Reader, cluster.Namespace, cluster.Name,
		deleteguard.MachineFilter{Role: roleControlPlane})
	if err != nil {
		return nil, nil, err
	}

	if len(cps) == 0 {
		return nil, nil, errors.New("no approved control plane to fetch an admin kubeconfig from")
	}

	dialCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	cp, err := a.TalosClient(dialCtx, cluster, &cps[0], bundle)
	if err != nil {
		return nil, nil, fmt.Errorf("dialing control plane %q for its admin kubeconfig: %w", cps[0].Name, err)
	}

	return cp, func() { closeTalosClient(cp) }, nil
}

// enableTail is the enabling tail: kube-proxy runs on every node first (it is the new side), and
// only then is Cilium asked to step aside.
//
// Talos creates the kube-proxy objects only while they are absent from its bootstrap-manifests
// inventory, which still lists them after removeKubeProxy deleted them, so it never recreates them. While the
// DaemonSet is missing, Ichor therefore restores them itself from
// Talos's own rendered manifest set; the call is idempotent and happens only while it is missing.
func (a *cniKubeProxyAdvancer) enableTail(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	op *ichorv1alpha1.TalosOperation, bundle *secrets.Bundle, talos TalosClient,
) (bool, error) {
	var ds appsv1.DaemonSet

	err := a.Reader.Get(ctx, types.NamespacedName{Namespace: kubeSystemNamespace, Name: kubeProxyName}, &ds)

	reason := ""

	switch {
	case apierrors.IsNotFound(err):
		cp, closeCP, cerr := a.controlPlaneClient(ctx, cluster, m, bundle, talos)
		if cerr != nil {
			return false, cerr
		}
		defer closeCP()

		if rerr := a.Admin.RestoreKubeProxy(ctx, cp); rerr != nil {
			return false, fmt.Errorf("restoring kube-proxy: %w", rerr)
		}

		reason = "the kube-proxy DaemonSet has not been created yet"
	case err != nil:
		return false, err
	case ds.Status.DesiredNumberScheduled == 0 || ds.Status.UpdatedNumberScheduled < ds.Status.DesiredNumberScheduled ||
		ds.Status.NumberReady < ds.Status.DesiredNumberScheduled:
		reason = fmt.Sprintf("kube-proxy is rolling out: %d/%d ready", ds.Status.NumberReady, ds.Status.DesiredNumberScheduled)
	}

	if reason != "" {
		return false, waitOn(op, reasonNewSideNotReady, reason)
	}

	clearWait(op)

	return a.requireCilium(ctx, cluster, op, cni.KubeProxyEnabled)
}

// requireCilium makes Cilium serve (or stop serving) Services as want says and reports whether it
// does: Ichor owns the release, so it runs the Cilium Helm Job for the cluster's
// effective values for the mode want (the only place the spec's mode is applied: the hand-over
// reconciler keeps the mode Cilium runs now, so a spec edit alone changes nothing; kubeProxyReplacement
// and k8sService* derive from the mode), then verifies cilium-config and the agents' rollout. A failed Job is
// an error, which pauses the operation with the Job's reason. Every wait is on the verification clock.
//
// The Job is this operation's own run (runCiliumJob): one that ended before the operation was created
// is replaced, not taken as done.
func (a *cniKubeProxyAdvancer) requireCilium(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, op *ichorv1alpha1.TalosOperation,
	want cni.KubeProxyMode,
) (bool, error) {
	if done, err := a.runCiliumJob(ctx, cluster, want, op, "",
		fmt.Sprintf("applying kube-proxy mode %q to the Cilium release", want),
		fmt.Sprintf("the Cilium Helm Job applying kube-proxy mode %q is running", want)); err != nil || !done {
		return false, err
	}

	var cm corev1.ConfigMap

	err := a.Reader.Get(ctx, types.NamespacedName{Namespace: kubeSystemNamespace, Name: ciliumConfigName}, &cm)
	if apierrors.IsNotFound(err) {
		return false, waitOn(op, reasonNewSideNotReady,
			"Cilium's cilium-config ConfigMap does not exist")
	}

	if err != nil {
		return false, err
	}

	live := cni.LiveConfig{KubeProxyReplacement: ciliumReplaces(cm.Data[ciliumKPRKey])}
	if err := cilium.New().CheckConsistency(ctx, live, want); err != nil {
		return false, waitOn(op, reasonCiliumNotSwitched,
			fmt.Sprintf("the Cilium Helm Job finished but cilium-config does not match yet: %v", err))
	}

	return a.ciliumRolledOut(ctx, op)
}

// runCiliumJob advances the Cilium Helm Job (salted as AdvanceCNIHelmJob says) and reports whether
// it has completed; while it runs the operation waits on the verification clock with running as
// the message, and a failure is wrapped with doing.
func (a *cniKubeProxyAdvancer) runCiliumJob(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode, op *ichorv1alpha1.TalosOperation,
	salt, doing, running string,
) (bool, error) {
	inst, err := installerSource(ctx, a.Reader)
	if err != nil {
		return false, err
	}

	nodes, err := clusterNodeCount(ctx, a.Reader)
	if err != nil {
		return false, err
	}

	done, err := AdvanceCNIHelmJob(ctx, a.Client, a.ControllerNamespace, cluster, mode, nodes, inst, a.Images,
		cniValuesCheck(a.Client, a.Reader, a.ControllerNamespace, cluster, mode), salt, op.CreationTimestamp.Time)
	if err != nil {
		return false, fmt.Errorf("%s: %w", doing, err)
	}

	if !done {
		return false, waitOn(op, reasonCiliumNotSwitched, running)
	}

	return true, nil
}

// ciliumRolledOut reports whether every Cilium agent runs the current DaemonSet generation and is
// ready; until then it waits on the verification clock.
func (a *cniKubeProxyAdvancer) ciliumRolledOut(ctx context.Context, op *ichorv1alpha1.TalosOperation) (bool, error) {
	var ds appsv1.DaemonSet
	if err := a.Reader.Get(ctx, types.NamespacedName{Namespace: kubeSystemNamespace, Name: ciliumName}, &ds); err != nil {
		return false, fmt.Errorf("reading the cilium DaemonSet: %w", err)
	}

	if !daemonSetRolledOut(&ds) {
		return false, waitOn(op, reasonNewSideNotReady,
			fmt.Sprintf("the Cilium agents are rolling out: %d/%d updated and ready",
				min(ds.Status.UpdatedNumberScheduled, ds.Status.NumberReady), ds.Status.DesiredNumberScheduled))
	}

	clearWait(op)

	return true, nil
}

// daemonSetRolledOut reports whether ds runs its current generation, scheduled somewhere, with
// every pod updated and ready.
func daemonSetRolledOut(ds *appsv1.DaemonSet) bool {
	want := ds.Status.DesiredNumberScheduled

	return ds.Status.ObservedGeneration >= ds.Generation && want > 0 &&
		ds.Status.UpdatedNumberScheduled >= want && ds.Status.NumberReady >= want
}

// ciliumReplaces reads cilium-config's kube-proxy-replacement: "true" (and the pre-1.14 "strict")
// mean Cilium serves Services; "false", "disabled" and anything else do not.
func ciliumReplaces(value string) bool {
	return value == "true" || value == "strict"
}

// waitOn starts or continues the verification clock and fails once it exceeds timeout. op may be
// nil (a caller with no operation): there is then no clock and the wait is unbounded.
func waitOn(op *ichorv1alpha1.TalosOperation, reason, message string) error {
	if op == nil {
		return nil
	}

	if cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionVerifyingNewSide); cond != nil &&
		cond.Status == metav1.ConditionTrue && time.Since(cond.LastTransitionTime.Time) > kubeProxyVerifyTimeout {
		return fmt.Errorf("timed out after %s waiting for the new kube-proxy side: %s", kubeProxyVerifyTimeout, message)
	}

	apimeta.SetStatusCondition(&op.Status.Conditions, metav1.Condition{
		Type: conditionVerifyingNewSide, Status: metav1.ConditionTrue, Reason: reason, Message: message,
	})

	return nil
}

// clearWait ends the verification clock.
func clearWait(op *ichorv1alpha1.TalosOperation) {
	if op == nil {
		return
	}

	if cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionVerifyingNewSide); cond != nil && cond.Status != metav1.ConditionFalse {
		apimeta.SetStatusCondition(&op.Status.Conditions, metav1.Condition{
			Type: conditionVerifyingNewSide, Status: metav1.ConditionFalse, Reason: reasonSideVerified, Message: "satisfied",
		})
	}
}

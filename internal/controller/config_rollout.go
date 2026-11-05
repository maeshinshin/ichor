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

// This file implements the TalosOperationKindConfig AdvanceMachineFunc: the per-node
// step of docs/design/architecture.md's "Configuration reconcile" step 4 for a change whose field
// paths pkg/fieldmeta says need a reboot. TalosMachineReconciler only reports such a change
// (refuseReboot); this applies it, and only when someone started the operation. The lock, the
// status-held state machine, pause-on-failure, maxUnavailable, the hold annotation, the ordering
// label and one-control-plane-at-a-time all come from TalosOperationReconciler.
//
// Per node: readiness and (control plane) etcd membership checks -> dry run -> storage-evacuation
// hook -> cordon and drain (Ichor's own: Talos's Reboot does not) -> staged apply (written to disk,
// nothing restarts) -> graceful Reboot -> wait for the boot ID to change -> verify the node runs the rendered
// configuration, is Ready and (control plane) is back in etcd -> uncordon.
package controller

import (
	"context"
	"fmt"
	"slices"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/storage"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
)

const (
	// conditionConfigRollout is the TalosMachine marker the executor uses as its resume point and
	// verification clock: True from the moment the reboot was requested until the node is verified
	// on the rendered configuration. Message carries the boot ID read just before the reboot, the
	// only way to tell "has not rebooted yet" from "rebooted".
	conditionConfigRollout = "ConfigRollout"

	reasonRolloutRebooting = "RebootRequested"
	reasonRolloutConverged = "Converged"
	reasonRolloutFailed    = "Failed"

	// reasonRolloutRestarting marks (Status True) a control plane whose kube-apiserver the applied
	// configuration is restarting; reasonRolloutAPINotReady (False) that it did not come back in time.
	reasonRolloutRestarting  = "APIServerRestarting"
	reasonRolloutAPINotReady = "APIServerNotReady"

	// apiServerReadyTimeout bounds the wait for a restarted kube-apiserver to answer /readyz (15-45 s
	// on Talos 1.14.3); apiServerRestartGrace is how long a pod that never changed is waited for.
	apiServerReadyTimeout = 3 * time.Minute
	apiServerRestartGrace = time.Minute

	// configRolloutVerifyTimeout bounds the wait for a node to come back on the new configuration,
	// Ready and in etcd. Past it the step fails and the operation pauses.
	configRolloutVerifyTimeout = 20 * time.Minute

	// rebootCallTimeout bounds the Reboot and the staged Apply RPCs, each given its own deadline
	// rather than what is left of the dial's.
	rebootCallTimeout = 2 * time.Minute
)

// configRolloutAdvancer is the config-kind AdvanceMachineFunc.
type configRolloutAdvancer struct {
	client.Client

	ControllerNamespace string

	TalosClient func(
		ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
	) (TalosClient, error)

	// Evacuator is the storage hook, nil when no storage system is configured (as in talosUpgradeAdvancer).
	Evacuator StorageEvacuator

	// Kube cordons and drains the node before its reboot and uncordons it afterwards (drain.go),
	// and reads its readiness live: Talos's Reboot does not drain, and on a single
	// control plane the cached Node would stay Ready while the API server is down. nil skips both.
	Kube kubernetes.Interface
}

// NewConfigRolloutAdvancer returns the config-kind AdvanceMachineFunc.
func NewConfigRolloutAdvancer(
	c client.Client, controllerNamespace string,
	talosClient func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) (TalosClient, error),
	evacuator StorageEvacuator, kube kubernetes.Interface,
) AdvanceMachineFunc {
	a := &configRolloutAdvancer{
		Client: c, ControllerNamespace: controllerNamespace, TalosClient: talosClient, Evacuator: evacuator, Kube: kube,
	}

	return a.Advance
}

// Advance performs one machine's step. Every decision is re-derived from the node's own
// configuration and boot ID and from the ConfigRollout condition, never from memory, so a restarted
// controller resumes where the node says it was.
func (a *configRolloutAdvancer) Advance(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *ichorv1alpha1.TalosOperation,
) (bool, error) {
	restarting := false
	if c := apimeta.FindStatusCondition(m.Status.Conditions, conditionConfigRollout); c != nil {
		restarting = c.Status == metav1.ConditionTrue && c.Reason == reasonRolloutRestarting
	}

	rebooting := apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionConfigRollout) && !restarting

	// While this control plane's own API server restarts, the controller may be talking to it: any
	// failure to read or write the cluster is waiting (bounded), not a reason to pause the operation.
	early := func(err error) (bool, error) {
		if restarting {
			return a.restartWaiting(ctx, m, err.Error())
		}

		return false, err
	}

	bundle, err := loadBundle(ctx, a.Client, a.ControllerNamespace, cluster)
	if err != nil {
		return early(fmt.Errorf("loading secrets bundle to roll out %q: %w", m.Name, err))
	}

	var patches ichorv1alpha1.TalosConfigPatchList
	if err := a.List(ctx, &patches, client.InNamespace(m.Namespace)); err != nil {
		return early(err)
	}

	rendered, hash, _, err := (&TalosMachineReconciler{Client: a.Client, ControllerNamespace: a.ControllerNamespace}).
		renderMachine(ctx, cluster, m, bundle, patches.Items)
	if err != nil {
		return early(fmt.Errorf("rendering %q's configuration: %w", m.Name, err))
	}

	dialCtx, cancel := dialBound(ctx)
	defer cancel()

	talos, err := a.TalosClient(dialCtx, cluster, m, bundle)
	if err != nil {
		return a.unreachable(ctx, cluster, m, rebooting, restarting, fmt.Errorf("dialing %q: %w", m.Name, err))
	}
	defer closeTalosClient(talos)

	current, err := talos.CurrentConfig(dialCtx)
	if err != nil {
		return a.unreachable(ctx, cluster, m, rebooting, restarting, fmt.Errorf("reading %q's configuration: %w", m.Name, err))
	}

	switch {
	case restarting:
		return a.afterRestart(ctx, dialCtx, talos, cluster, m, current, rendered, hash)
	case rebooting:
		return a.afterReboot(ctx, dialCtx, talos, cluster, m, current, hash)
	case render.Hash(current) == hash:
		// A retried wait: the configuration is on the node but its API server did not
		// answer in time last time; wait again with a fresh clock instead of calling it done.
		if c := apimeta.FindStatusCondition(m.Status.Conditions, conditionConfigRollout); c != nil &&
			c.Status == metav1.ConditionFalse && c.Reason == reasonRolloutAPINotReady {
			return false, a.markRestarting(ctx, m, "")
		}

		return true, nil // nothing pending on this node: no reboot, no hook
	default:
		return false, a.begin(ctx, talos, cluster, m, current, rendered)
	}
}

// unreachable decides what a failed dial or read means: while the reboot is in flight the node is
// expected to be away, bounded by configRolloutVerifyTimeout; otherwise it is a failure.
func (a *configRolloutAdvancer) unreachable(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, rebooting, restarting bool, err error,
) (bool, error) {
	switch {
	case restarting:
		return a.restartWaiting(ctx, m, err.Error())
	case rebooting:
		return false, a.stillWaiting(ctx, cluster, m, err.Error())
	}

	return false, err
}

// markRestarting records that this control plane's kube-apiserver is being restarted by the
// rendered configuration (written before the apply, so a controller killed in between resumes into
// applying again rather than skipping the wait). baseline is the kube-apiserver container ID read
// just before; empty skips the "has restarted" check and waits only for /readyz.
func (a *configRolloutAdvancer) markRestarting(ctx context.Context, m *ichorv1alpha1.TalosMachine, baseline string) error {
	return a.setRollout(ctx, m, metav1.ConditionTrue, reasonRolloutRestarting, baseline)
}

// setRollout writes the ConfigRollout marker on m.
func (a *configRolloutAdvancer) setRollout(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, status metav1.ConditionStatus, reason, message string,
) error {
	return setMachineCondition(ctx, a.Client, a.Client, m, metav1.Condition{
		Type: conditionConfigRollout, Status: status, Reason: reason, Message: message,
	})
}

// restartWaiting returns nil while the restart is inside its window (apiServerReadyTimeout) and,
// past it, pauses the operation with a reason that says the API server did not come back.
func (a *configRolloutAdvancer) restartWaiting(ctx context.Context, m *ichorv1alpha1.TalosMachine, why string) (bool, error) {
	cond := apimeta.FindStatusCondition(m.Status.Conditions, conditionConfigRollout)
	if cond == nil || time.Since(cond.LastTransitionTime.Time) < apiServerReadyTimeout {
		return false, nil
	}

	err := fmt.Errorf("kube-apiserver on %q did not answer /readyz within %s of the configuration being applied (%s); "+
		"the operation is paused so the next control plane is not restarted too: fix or wait for it, then retry the step",
		m.Name, apiServerReadyTimeout, why)

	if updErr := a.setRollout(ctx, m, metav1.ConditionFalse, reasonRolloutAPINotReady, err.Error()); updErr != nil {
		return false, fmt.Errorf("%w (and recording it failed: %v)", err, updErr)
	}

	return false, err
}

// applyAPIServerRestart applies a change that needs no reboot but restarts this control plane's
// kube-apiserver: no drain, no evacuation, no reboot. The step finishes in
// afterRestart, when the API server answers /readyz again.
func (a *configRolloutAdvancer) applyAPIServerRestart(
	ctx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, rendered []byte,
) error {
	instCtx, cancelInst := dialBound(ctx)
	baseline, err := talos.APIServerInstance(instCtx)

	cancelInst()

	if err != nil {
		return fmt.Errorf("reading %q's kube-apiserver before restarting it: %w", m.Name, err)
	}

	if err := a.markRestarting(ctx, m, baseline); err != nil {
		return fmt.Errorf("recording the API server restart of %q: %w", m.Name, err)
	}

	applyCtx, cancelApply := context.WithTimeout(ctx, rebootCallTimeout)
	defer cancelApply()

	if err := talos.ApplyNoReboot(applyCtx, rendered); err != nil {
		return a.fail(ctx, cluster, m, fmt.Errorf("applying the rendered configuration to %q: %w", m.Name, err))
	}

	return nil
}

// afterRestart runs once the restart was requested. A node whose configuration is not yet the
// rendered one means the controller stopped between recording the marker and applying: apply
// again. Otherwise it waits for the kube-apiserver container to have changed (or a grace period to
// pass, for a change that turned out not to alter the pod) and for /readyz to answer.
func (a *configRolloutAdvancer) afterRestart(
	ctx, dialCtx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	current, rendered []byte, hash string,
) (bool, error) {
	if render.Hash(current) != hash {
		applyCtx, cancelApply := context.WithTimeout(ctx, rebootCallTimeout)
		defer cancelApply()

		if err := talos.ApplyNoReboot(applyCtx, rendered); err != nil {
			return false, a.fail(ctx, cluster, m, fmt.Errorf("applying the rendered configuration to %q: %w", m.Name, err))
		}

		return false, nil
	}

	cond := apimeta.FindStatusCondition(m.Status.Conditions, conditionConfigRollout)

	instance, err := talos.APIServerInstance(dialCtx)
	if err != nil {
		return a.restartWaiting(ctx, m, "reading the kube-apiserver container: "+err.Error())
	}

	restarted := cond.Message == "" || instance != cond.Message
	if !restarted && time.Since(cond.LastTransitionTime.Time) < apiServerRestartGrace {
		return false, nil
	}

	readyCtx, cancelReady := dialBound(ctx)
	defer cancelReady()

	if err := talos.APIServerReady(readyCtx); err != nil {
		return a.restartWaiting(ctx, m, err.Error())
	}

	if err := a.setRollout(ctx, m, metav1.ConditionFalse, reasonRolloutConverged,
		"running the rendered configuration; kube-apiserver is ready"); err != nil {
		return false, err
	}

	return true, nil
}

// stillWaiting returns nil while the rollout of m is inside its verification window and an error
// (so the operation pauses) once it is past it.
func (a *configRolloutAdvancer) stillWaiting(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, why string,
) error {
	cond := apimeta.FindStatusCondition(m.Status.Conditions, conditionConfigRollout)
	if cond == nil || time.Since(cond.LastTransitionTime.Time) < configRolloutVerifyTimeout {
		return nil
	}

	return a.fail(ctx, cluster, m, fmt.Errorf("%q did not come back on the new configuration within %s: %s",
		m.Name, configRolloutVerifyTimeout, why))
}

// fail records the failure on the marker, ends evacuation (the node must not stay unschedulable
// because a rollout paused) and returns err.
func (a *configRolloutAdvancer) fail(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, err error,
) error {
	if endErr := endEvacuationOf(ctx, a.Client, a.Evacuator, nil, cluster, m); endErr != nil {
		return fmt.Errorf("%w (and re-enabling scheduling failed: %v)", err, endErr)
	}

	if uncErr := a.uncordon(ctx, nil, m); uncErr != nil {
		err = fmt.Errorf("%w (and uncordoning failed: %v)", err, uncErr)
	}

	if updErr := a.setRollout(ctx, m, metav1.ConditionFalse, reasonRolloutFailed, err.Error()); updErr != nil {
		return fmt.Errorf("%w (and recording it failed: %v)", err, updErr)
	}

	return err
}

// dialBound is a fresh context for one short Talos call, created at the point of use: a context
// made before a long evacuation or drain would already have expired.
func dialBound(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, etcdDialTimeout)
}

// begin runs the pre-checks for one node and, when a reboot really is needed, stages the rendered
// configuration and reboots. A pending change that needs no reboot is applied without one.
func (a *configRolloutAdvancer) begin(
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

	// A kube-proxy mode changed in spec ahead of the switch is the cni operation's to apply, with its
	// safety checks; rebooting a node onto it here would skip them.
	if kubeProxySwitchPending(cluster) && slices.ContainsFunc(paths, isKubeProxyPath) {
		return fmt.Errorf("%q has a pending kube-proxy change (spec.cni.kubeProxy is %s); run `ichor cni kube-proxy %s` "+
			"first -- a config rollout would reboot the node onto it without the switch's checks",
			m.Name, cluster.Spec.CNI.KubeProxy, cluster.Spec.CNI.KubeProxy)
	}

	impact := assessChange(ctx, talos, m.Spec.Hostname, paths, contract)

	switch {
	case len(impact.Unsupported) > 0:
		return fmt.Errorf("%q needs a newer Talos version than the pinned contract %s: %v",
			m.Name, cluster.Spec.VersionContract, impact.Unsupported)
	case len(impact.InstallTimeOnly) > 0:
		return fmt.Errorf("%q needs a reset and reinstall, not a reboot (install-time-only fields: %v)",
			m.Name, impact.InstallTimeOnly)
	}

	dryRunCtx, cancelDryRun := dialBound(ctx)
	_, err = talos.DryRunApply(dryRunCtx, rendered)

	cancelDryRun()

	if err != nil {
		return fmt.Errorf("dry-run apply to %q failed: %w", m.Name, err)
	}

	if !impact.RebootRequired {
		if len(impact.APIServerRestartPaths) > 0 && m.Spec.Role == roleControlPlane {
			return a.applyAPIServerRestart(ctx, talos, cluster, m, rendered)
		}

		applyCtx, cancelApply := context.WithTimeout(ctx, rebootCallTimeout)
		defer cancelApply()

		if err := talos.ApplyNoReboot(applyCtx, rendered); err != nil {
			return fmt.Errorf("applying the rendered configuration to %q: %w", m.Name, err)
		}

		return nil
	}

	node, err := resolveNodeName(ctx, a.Client, talos, m)
	if err != nil {
		return err
	}

	ready, err := nodeReadyByName(ctx, a.Kube, a.Client, node)
	if err != nil {
		return fmt.Errorf("checking %q's node readiness: %w", m.Name, err)
	}

	if !ready {
		return fmt.Errorf("node %q is not Ready; refusing to reboot it", node)
	}

	if m.Spec.Role == roleControlPlane {
		if err := etcdAllMembersPresent(ctx, a.Client, talos, cluster, m, "rebooting"); err != nil {
			return err
		}
	}

	progress, err := evacuateMachine(ctx, a.Client, a.Evacuator, cluster, m, node, "rebooting")
	if err != nil {
		return err
	}

	switch progress {
	case storage.EvacuationWaiting:
		return nil
	case storage.EvacuationPaused:
		return fmt.Errorf("evacuating %q timed out with storage replicas still remaining", node)
	}

	if a.Kube != nil {
		drained, err := drainForReboot(ctx, a.Kube, node)
		if err != nil {
			if uncErr := a.uncordon(ctx, talos, m); uncErr != nil {
				err = fmt.Errorf("%w (and uncordoning failed: %v)", err, uncErr)
			}

			return err
		}

		if !drained {
			return nil // evictions are still going (a PodDisruptionBudget holds some back): resume next reconcile
		}
	}

	if err := a.stageAndReboot(ctx, talos, cluster, m, rendered); err != nil {
		// Nothing was rebooted (a failed Reboot call already went through fail), so do not leave the node cordoned.
		_ = a.uncordon(ctx, talos, m)

		return err
	}

	return nil
}

// stageAndReboot writes the configuration to disk, records the pre-reboot boot ID on the marker
// and only then asks for the reboot, so a controller killed in between resumes into "waiting for
// the boot ID to change" instead of rebooting the node a second time.
func (a *configRolloutAdvancer) stageAndReboot(
	ctx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	rendered []byte,
) error {
	// A fresh bound: begin may have spent minutes evacuating and draining since Advance dialed.
	bootCtx, cancelBoot := dialBound(ctx)
	bootID, err := talos.BootID(bootCtx)

	cancelBoot()

	if err != nil {
		return fmt.Errorf("reading %q's boot ID before rebooting: %w", m.Name, err)
	}

	stageCtx, cancelStage := context.WithTimeout(ctx, rebootCallTimeout)
	err = talos.ApplyStaged(stageCtx, rendered)

	cancelStage()

	if err != nil {
		return fmt.Errorf("staging the rendered configuration on %q: %w", m.Name, err)
	}

	if err := a.setRollout(ctx, m, metav1.ConditionTrue, reasonRolloutRebooting, bootID); err != nil {
		return fmt.Errorf("recording the reboot of %q: %w", m.Name, err)
	}

	rebootCtx, cancelReboot := context.WithTimeout(ctx, rebootCallTimeout)
	defer cancelReboot()

	if err := talos.Reboot(rebootCtx); err != nil {
		return a.fail(ctx, cluster, m, fmt.Errorf("reboot API call to %q failed: %w", m.Name, err))
	}

	return nil
}

// etcdAllMembersPresent is the quorum guard of every action that takes a control plane's etcd
// member away for a while: talos's etcd must list every approved control
// plane. A reboot takes this member down, and with another one already missing a three-member
// cluster would lose quorum; Talos is not asked to do this for Reboot as it is for Upgrade, so the check is Ichor's.
// doing completes the refusal ("rebooting", "stopping").
func etcdAllMembersPresent(
	ctx context.Context, reader client.Reader, talos TalosClient, cluster *ichorv1alpha1.TalosCluster,
	m *ichorv1alpha1.TalosMachine, doing string,
) error {
	listCtx, cancelList := dialBound(ctx)
	members, err := talos.EtcdMemberList(listCtx)

	cancelList()

	if err != nil {
		return fmt.Errorf("listing %q's etcd members: %w", m.Name, err)
	}

	planes, err := deleteguard.ApprovedMachines(ctx, reader, cluster.Namespace, cluster.Name, deleteguard.MachineFilter{})
	if err != nil {
		return err
	}

	planeCount := 0

	for i := range planes {
		cp := &planes[i]
		if cp.Spec.Role != roleControlPlane {
			continue
		}

		planeCount++

		// A control plane without spec.hostname is counted below instead: its name is only known
		// to its own node, and dialing every sibling here would make a reboot depend on all of them.
		if cp.Spec.Hostname != "" && !etcdMemberPresent(members, cp.Spec.Hostname) {
			return fmt.Errorf("etcd member %q is missing; %s %q could lose quorum", cp.Spec.Hostname, doing, m.Name)
		}
	}

	if len(members) < planeCount {
		return fmt.Errorf("etcd lists %d members for %d approved control planes; %s %q could lose quorum",
			len(members), planeCount, doing, m.Name)
	}

	return nil
}

// afterReboot runs once the reboot was requested: it waits for the boot ID to change, then fails
// if the node does not run the rendered configuration, or finishes.
func (a *configRolloutAdvancer) afterReboot(
	ctx, dialCtx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	current []byte, hash string,
) (bool, error) {
	cond := apimeta.FindStatusCondition(m.Status.Conditions, conditionConfigRollout)

	bootID, err := talos.BootID(dialCtx)
	if err != nil {
		return false, a.stillWaiting(ctx, cluster, m, err.Error())
	}

	if cond != nil && cond.Message == bootID {
		return false, a.stillWaiting(ctx, cluster, m, "the node has not rebooted")
	}

	if render.Hash(current) != hash {
		return false, a.fail(ctx, cluster, m, fmt.Errorf(
			"machine %q rebooted but is not running the rendered configuration: the staged configuration was not activated", m.Name))
	}

	return a.finish(ctx, dialCtx, talos, cluster, m)
}

// finish verifies readiness and, for a control plane, etcd membership; once both pass it ends
// evacuation and clears the marker.
func (a *configRolloutAdvancer) finish(
	ctx, dialCtx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) (bool, error) {
	node, err := resolveNodeName(ctx, a.Client, talos, m)
	if err != nil {
		return false, a.stillWaiting(ctx, cluster, m, err.Error())
	}

	ready, err := nodeReadyByName(ctx, a.Kube, a.Client, node)
	if err != nil {
		// A single control plane has no API server while it reboots; that is waiting, not failing.
		return false, a.stillWaiting(ctx, cluster, m, "checking the node's readiness: "+err.Error())
	}

	if !ready {
		return false, a.stillWaiting(ctx, cluster, m, "the node is not Ready")
	}

	if m.Spec.Role == roleControlPlane {
		members, err := talos.EtcdMemberList(dialCtx)
		if err != nil {
			return false, a.stillWaiting(ctx, cluster, m, "listing etcd members: "+err.Error())
		}

		if !etcdMemberPresent(members, node) {
			return false, a.stillWaiting(ctx, cluster, m, "the node is not back in etcd")
		}
	}

	if err := a.uncordon(ctx, talos, m); err != nil {
		return false, a.stillWaiting(ctx, cluster, m, "uncordoning the node: "+err.Error())
	}

	if err := endEvacuationOf(ctx, a.Client, a.Evacuator, talos, cluster, m); err != nil {
		return false, err
	}

	if err := a.setRollout(ctx, m, metav1.ConditionFalse, reasonRolloutConverged, "running the rendered configuration"); err != nil {
		return false, err
	}

	return true, nil
}

// uncordon lifts the cordon drainForReboot made, if any (a no-op without Kube).
func (a *configRolloutAdvancer) uncordon(ctx context.Context, talos TalosClient, m *ichorv1alpha1.TalosMachine) error {
	if a.Kube == nil {
		return nil
	}

	node, err := resolveNodeName(ctx, a.Client, talos, m)
	if err != nil {
		return err
	}

	return uncordonAfterReboot(ctx, a.Kube, node)
}

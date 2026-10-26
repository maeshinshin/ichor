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
// hook -> staged apply (written to disk, nothing restarts) -> graceful Reboot (Talos cordons and
// drains first) -> wait for the boot ID to change -> verify the node runs the rendered
// configuration, is Ready and (control plane) is back in etcd.
package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/fieldmeta"
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
}

// NewConfigRolloutAdvancer returns the config-kind AdvanceMachineFunc.
func NewConfigRolloutAdvancer(
	c client.Client, controllerNamespace string,
	talosClient func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) (TalosClient, error),
	evacuator StorageEvacuator,
) AdvanceMachineFunc {
	a := &configRolloutAdvancer{Client: c, ControllerNamespace: controllerNamespace, TalosClient: talosClient, Evacuator: evacuator}

	return a.Advance
}

// Advance performs one machine's step. Every decision is re-derived from the node's own
// configuration and boot ID and from the ConfigRollout condition, never from memory, so a restarted
// controller resumes where the node says it was.
func (a *configRolloutAdvancer) Advance(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *ichorv1alpha1.TalosOperation,
) (bool, error) {
	bundle, err := loadBundle(ctx, a.Client, a.ControllerNamespace, cluster)
	if err != nil {
		return false, fmt.Errorf("loading secrets bundle to roll out %q: %w", m.Name, err)
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

	rebooting := apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionConfigRollout)

	dialCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	talos, err := a.TalosClient(dialCtx, cluster, m, bundle)
	if err != nil {
		return a.unreachable(ctx, cluster, m, rebooting, fmt.Errorf("dialing %q: %w", m.Name, err))
	}
	defer closeTalosClient(talos)

	current, err := talos.CurrentConfig(dialCtx)
	if err != nil {
		return a.unreachable(ctx, cluster, m, rebooting, fmt.Errorf("reading %q's configuration: %w", m.Name, err))
	}

	switch {
	case rebooting:
		return a.afterReboot(ctx, dialCtx, talos, cluster, m, current, hash)
	case hashConfig(current) == hash:
		return true, nil // nothing pending on this node: no reboot, no hook
	default:
		return false, a.begin(ctx, dialCtx, talos, cluster, m, current, rendered)
	}
}

// unreachable decides what a failed dial or read means: while the reboot is in flight the node is
// expected to be away, bounded by configRolloutVerifyTimeout; otherwise it is a failure.
func (a *configRolloutAdvancer) unreachable(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, rebooting bool, err error,
) (bool, error) {
	if !rebooting {
		return false, err
	}

	return false, a.stillWaiting(ctx, cluster, m, err.Error())
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
	if endErr := a.endEvacuation(ctx, nil, cluster, m); endErr != nil {
		return fmt.Errorf("%w (and re-enabling scheduling failed: %v)", err, endErr)
	}

	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: conditionConfigRollout, Status: metav1.ConditionFalse, Reason: reasonRolloutFailed, Message: err.Error(),
	})

	if updErr := a.Status().Update(ctx, m); updErr != nil {
		return fmt.Errorf("%w (and recording it failed: %v)", err, updErr)
	}

	return err
}

// begin runs the pre-checks for one node and, when a reboot really is needed, stages the rendered
// configuration and reboots. A pending change that needs no reboot is applied without one.
func (a *configRolloutAdvancer) begin(
	ctx, dialCtx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
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

	impact := fieldmeta.Assess(paths, contract)

	switch {
	case len(impact.Unsupported) > 0:
		return fmt.Errorf("%q needs a newer Talos version than the pinned contract %s: %v",
			m.Name, cluster.Spec.VersionContract, impact.Unsupported)
	case len(impact.InstallTimeOnly) > 0:
		return fmt.Errorf("%q needs a reset and reinstall, not a reboot (install-time-only fields: %v)",
			m.Name, impact.InstallTimeOnly)
	}

	dryRunCtx, cancelDryRun := context.WithTimeout(ctx, etcdDialTimeout)
	_, err = talos.DryRunApply(dryRunCtx, rendered)

	cancelDryRun()

	if err != nil {
		return fmt.Errorf("dry-run apply to %q failed: %w", m.Name, err)
	}

	if !impact.RebootRequired {
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

	ready, err := a.nodeReady(ctx, node)
	if err != nil {
		return fmt.Errorf("checking %q's node readiness: %w", m.Name, err)
	}

	if !ready {
		return fmt.Errorf("node %q is not Ready; refusing to reboot it", node)
	}

	if m.Spec.Role == roleControlPlane {
		if err := a.checkEtcd(ctx, dialCtx, talos, cluster, m); err != nil {
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

	return a.stageAndReboot(ctx, dialCtx, talos, cluster, m, rendered)
}

// stageAndReboot writes the configuration to disk, records the pre-reboot boot ID on the marker
// and only then asks for the reboot, so a controller killed in between resumes into "waiting for
// the boot ID to change" instead of rebooting the node a second time.
func (a *configRolloutAdvancer) stageAndReboot(
	ctx, dialCtx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	rendered []byte,
) error {
	bootID, err := talos.BootID(dialCtx)
	if err != nil {
		return fmt.Errorf("reading %q's boot ID before rebooting: %w", m.Name, err)
	}

	stageCtx, cancelStage := context.WithTimeout(ctx, rebootCallTimeout)
	err = talos.ApplyStaged(stageCtx, rendered)

	cancelStage()

	if err != nil {
		return fmt.Errorf("staging the rendered configuration on %q: %w", m.Name, err)
	}

	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: conditionConfigRollout, Status: metav1.ConditionTrue, Reason: reasonRolloutRebooting, Message: bootID,
	})

	if err := a.Status().Update(ctx, m); err != nil {
		return fmt.Errorf("recording the reboot of %q: %w", m.Name, err)
	}

	rebootCtx, cancelReboot := context.WithTimeout(ctx, rebootCallTimeout)
	defer cancelReboot()

	if err := talos.Reboot(rebootCtx); err != nil {
		return a.fail(ctx, cluster, m, fmt.Errorf("reboot API call to %q failed: %w", m.Name, err))
	}

	return nil
}

// checkEtcd refuses to reboot a control plane unless this node's etcd lists every approved
// control plane as a member: a reboot takes this member down, and with another one already
// missing a three-member cluster would lose quorum. Talos is not asked to do this for Reboot as
// it is for Upgrade, so the check is Ichor's.
func (a *configRolloutAdvancer) checkEtcd(
	ctx, dialCtx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) error {
	members, err := talos.EtcdMemberList(dialCtx)
	if err != nil {
		return fmt.Errorf("listing %q's etcd members: %w", m.Name, err)
	}

	planes, err := deleteguard.ApprovedMachines(ctx, a.Client, cluster.Namespace, cluster.Name, deleteguard.MachineFilter{})
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
			return fmt.Errorf("etcd member %q is missing; rebooting %q could lose quorum", cp.Spec.Hostname, m.Name)
		}
	}

	if len(members) < planeCount {
		return fmt.Errorf("etcd lists %d members for %d approved control planes; rebooting %q could lose quorum",
			len(members), planeCount, m.Name)
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

	if hashConfig(current) != hash {
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

	ready, err := a.nodeReady(ctx, node)
	if err != nil {
		return false, fmt.Errorf("checking %q's node readiness: %w", m.Name, err)
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

	if err := a.endEvacuation(ctx, talos, cluster, m); err != nil {
		return false, err
	}

	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: conditionConfigRollout, Status: metav1.ConditionFalse, Reason: reasonRolloutConverged,
		Message: "running the rendered configuration",
	})

	if err := a.Status().Update(ctx, m); err != nil {
		return false, err
	}

	return true, nil
}

// endEvacuation re-enables scheduling after an evacuation, resolving the node's name like the
// rest of the rollout (talos may be nil when the node cannot be dialed).
func (a *configRolloutAdvancer) endEvacuation(
	ctx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) error {
	if a.Evacuator == nil || !apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionStorageEvacuation) {
		return nil
	}

	node, err := resolveNodeName(ctx, a.Client, talos, m)
	if err != nil {
		return err
	}

	return endMachineEvacuation(ctx, a.Client, a.Evacuator, cluster, m, node)
}

// nodeReady reports whether hostname's Kubernetes Node is Ready.
func (a *configRolloutAdvancer) nodeReady(ctx context.Context, hostname string) (bool, error) {
	var node corev1.Node
	if err := a.Get(ctx, types.NamespacedName{Name: hostname}, &node); err != nil {
		return false, err
	}

	return nodeIsReady(&node), nil
}

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

// This file implements the TalosOperationKindTalos AdvanceMachineFunc docs/design/architecture.md's "Upgrades"
// describes
// for one node: evacuation hook, Upgrade API, wait for reboot, verify
// version/readiness/etcd membership, fail (never succeed) on an A/B-slot
// rollback. TalosOperationReconciler itself supplies everything else --
// the lock, the status-held state machine, pause-on-failure, maxUnavailable,
// the hold annotation and ordering label, one-control-plane-at-a-time --
// none of which is rebuilt here.
package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/storage"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// imageFactoryHost and defaultInstallerSchematic build an installer image
// reference the same way github.com/siderolabs/talos/pkg/images.
// NewInstallerImage does (factory + "/" + platform + "-installer/" +
// schematic + ":" + tag, verified by reading that package at v1.14.3) --
// reimplemented as one format string rather than taking a dependency on
// it: that package lives in the top-level github.com/siderolabs/talos
// module, not the pkg/machinery submodule this project already depends on,
// and pulling in the whole monorepo module for one string format is
// exactly the "wrong module" problem already declined for
// go-kubernetes's upgrade package. defaultInstallerSchematic is that same
// package's own DefaultInstallerImageSchematic constant, copied verbatim.
const (
	imageFactoryHost          = "factory.talos.dev"
	defaultInstallerSchematic = "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"
	installerPlatform         = "metal"
)

// conditionTalosUpgrade and conditionStorageEvacuation are TalosMachine
// status conditions talosUpgradeAdvancer uses purely as its own
// resumability markers -- mirroring the existing fire-and-forget-RPC
// idempotency pattern removal.go's conditionRemoval/reasonResetRequested
// already uses for resetNode, rather than inventing a second mechanism.
// Neither is part of TalosOperationStatus: both live on the TalosMachine Advance
// already receives, so a killed and restarted controller resumes from live
// state, never from memory.
const (
	// conditionTalosUpgrade is True from the moment the Upgrade API call is
	// acknowledged until the node is confirmed back and converged on the
	// target version. Message carries the boot ID recorded immediately
	// before the call, the only way to tell "hasn't rebooted yet" apart
	// from "rebooted and fell back to the same old version" (both report
	// an unchanged Version()).
	conditionTalosUpgrade = "TalosUpgrade"
)

const (
	reasonUpgradeRequested = "UpgradeRequested"
	reasonUpgradeConverged = "Converged"
)

// talosUpgradeAdvancer is the Talos-kind AdvanceMachineFunc. cmd/main.go
// selects it for TalosOperationKindTalos only; TalosOperationReconciler
// itself never branches on spec.kind.
type talosUpgradeAdvancer struct {
	client.Client

	// ControllerNamespace is loadBundle's namespace -- the same field,
	// with the same meaning, as TalosMachineReconciler.ControllerNamespace.
	ControllerNamespace string

	// TalosClient opens the Talos API boundary for a machine, the same
	// factory type TalosMachineReconciler.TalosClient and removal.go
	// already use.
	TalosClient func(
		ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
	) (TalosClient, error)

	// Evacuator drives pkg/storage's EvacuationHook/EvacuationStatus for a
	// machine's node, or reports nothing needs evacuating. nil skips
	// evacuation entirely -- a cluster with no storage system configured
	// has nothing for this to coordinate.
	Evacuator StorageEvacuator
}

// NewTalosUpgradeAdvancer returns the Talos-kind AdvanceMachineFunc,
// wired with the real Talos client dialer and storage evacuator cmd/main.go
// constructs.
func NewTalosUpgradeAdvancer(
	c client.Client, controllerNamespace string,
	talosClient func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) (TalosClient, error),
	evacuator StorageEvacuator,
) AdvanceMachineFunc {
	a := &talosUpgradeAdvancer{
		Client: c, ControllerNamespace: controllerNamespace, TalosClient: talosClient, Evacuator: evacuator,
	}

	return a.Advance
}

// Advance implements AdvanceMachineFunc for one machine in a Talos version
// upgrade. Every decision below is re-derived from live state on every
// call -- the node's own reported version and boot ID, m's own status
// conditions -- never from in-memory state, so a killed and restarted
// controller resumes exactly where the live node and m.status already say
// it was.
func (a *talosUpgradeAdvancer) Advance(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *ichorv1alpha1.TalosOperation,
) (bool, error) {
	if m.Spec.Hostname == "" {
		return false, fmt.Errorf("machine %q has no hostname recorded; cannot verify its Kubernetes node or etcd membership", m.Name)
	}

	target := cluster.Spec.TalosVersion

	bundle, err := loadBundle(ctx, a.Client, a.ControllerNamespace, cluster)
	if err != nil {
		return false, fmt.Errorf("loading secrets bundle to upgrade %q: %w", m.Name, err)
	}

	// rebooting means the Upgrade API call already succeeded on an earlier
	// Advance call (possibly a previous process's): the node is expected
	// to be briefly unreachable while it drains, stops services and
	// reboots, so a dial or RPC failure below is treated as "still
	// settling", not a hard failure, only in this state.
	rebooting := apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionTalosUpgrade)

	dialCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	talos, err := a.TalosClient(dialCtx, cluster, m, bundle)
	if err != nil {
		if rebooting {
			return false, nil
		}

		return false, fmt.Errorf("dialing %q: %w", m.Name, err)
	}
	defer closeTalosClient(talos)

	currentVersion, err := talos.Version(dialCtx)
	if err != nil {
		if rebooting {
			return false, nil
		}

		return false, fmt.Errorf("reading %q's Talos version: %w", m.Name, err)
	}

	switch {
	case rebooting:
		return a.afterReboot(ctx, dialCtx, talos, cluster, m, currentVersion, target)
	case currentVersion != target:
		return a.beginUpgrade(ctx, dialCtx, talos, cluster, m, currentVersion, target)
	default:
		return a.finish(ctx, dialCtx, talos, cluster, m)
	}
}

// beginUpgrade runs the pre-checks docs/design/architecture.md's
// "Upgrades" lists, evacuates storage if any is configured, then calls the
// Upgrade API and records the boot ID current before the call, so a later
// Advance can tell a reboot apart from "hasn't happened yet".
//
// etcd health is not re-checked here: Talos's own Upgrade handler already
// validates it for a control-plane node before proceeding, under its own
// etcd-held mutex, as long as force and preserve both stay false
// (v1alpha1_server.go's Server.Upgrade, confirmed against Talos 1.14.3's
// source) -- the same reasoning architecture.md already gives for not
// re-implementing cordon/drain applies here: Ichor would only duplicate a
// check Talos already makes more precisely.
func (a *talosUpgradeAdvancer) beginUpgrade(
	ctx, dialCtx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	currentVersion, target string,
) (bool, error) {
	if err := upgrade.TalosPath(currentVersion, target); err != nil {
		return false, fmt.Errorf("talos upgrade path %s -> %s is not supported: %w", currentVersion, target, err)
	}

	if err := upgrade.KubernetesPairing(cluster.Spec.KubernetesVersion, target); err != nil {
		return false, fmt.Errorf("kubernetes %s is not supported with talos %s: %w", cluster.Spec.KubernetesVersion, target, err)
	}

	ready, err := a.nodeReady(ctx, m.Spec.Hostname)
	if err != nil {
		return false, fmt.Errorf("checking %q's node readiness: %w", m.Name, err)
	}

	if !ready {
		return false, fmt.Errorf("node %q is not Ready; refusing to start an upgrade", m.Spec.Hostname)
	}

	progress, err := a.evacuate(ctx, cluster, m)
	if err != nil {
		return false, err
	}

	switch progress {
	case storage.EvacuationWaiting:
		return false, nil
	case storage.EvacuationPaused:
		return false, fmt.Errorf("evacuating %q timed out with storage replicas still remaining", m.Spec.Hostname)
	}

	bootID, err := talos.BootID(dialCtx)
	if err != nil {
		return false, fmt.Errorf("reading %q's boot ID before upgrading: %w", m.Name, err)
	}

	if err := talos.Upgrade(dialCtx, installerImage(cluster)); err != nil {
		return false, fmt.Errorf("upgrade API call to %q failed: %w", m.Name, err)
	}

	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: conditionTalosUpgrade, Status: metav1.ConditionTrue, Reason: reasonUpgradeRequested,
		Message: bootID,
	})

	if err := a.Status().Update(ctx, m); err != nil {
		return false, fmt.Errorf("recording the upgrade request for %q: %w", m.Name, err)
	}

	return false, nil
}

// afterReboot runs once the Upgrade API call already succeeded on an
// earlier Advance call: it waits for the boot ID to actually change (a
// node that hasn't rebooted yet reports the same one BootID did right
// before the Upgrade call), then either fails outright -- a changed boot ID
// but a version that still isn't target means the node fell back to its
// previous A/B slot, which architecture.md's "Upgrades" counts as a
// failure, never a success -- or verifies and finishes.
func (a *talosUpgradeAdvancer) afterReboot(
	ctx, dialCtx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	currentVersion, target string,
) (bool, error) {
	cond := apimeta.FindStatusCondition(m.Status.Conditions, conditionTalosUpgrade)

	bootID, err := talos.BootID(dialCtx)
	if err != nil {
		return false, nil // transient: treat like any other hiccup while rebooting
	}

	if cond != nil && cond.Message == bootID {
		return false, nil // hasn't rebooted yet
	}

	if currentVersion != target {
		if endErr := a.endEvacuation(ctx, cluster, m); endErr != nil {
			return false, endErr
		}

		return false, fmt.Errorf(
			"machine %q rebooted after an upgrade to %s but is now running %s: it fell back to its previous A/B slot",
			m.Name, target, currentVersion)
	}

	return a.finish(ctx, dialCtx, talos, cluster, m)
}

// finish verifies readiness and (for a control plane) etcd membership,
// and once both pass, ends evacuation and clears conditionTalosUpgrade --
// the one place both the already-converged fast path (Advance's default
// case) and afterReboot's success path land.
func (a *talosUpgradeAdvancer) finish(
	ctx, dialCtx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) (bool, error) {
	ready, err := a.nodeReady(ctx, m.Spec.Hostname)
	if err != nil {
		return false, fmt.Errorf("checking %q's node readiness: %w", m.Name, err)
	}

	if !ready {
		return false, nil // expected right after a reboot: keep waiting
	}

	if m.Spec.Role == roleControlPlane {
		members, err := talos.EtcdMemberList(dialCtx)
		if err != nil {
			return false, fmt.Errorf("listing %q's etcd members: %w", m.Name, err)
		}

		if !etcdMemberPresent(members, m.Spec.Hostname) {
			return false, nil
		}
	}

	if err := a.endEvacuation(ctx, cluster, m); err != nil {
		return false, err
	}

	if apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionTalosUpgrade) {
		apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
			Type: conditionTalosUpgrade, Status: metav1.ConditionFalse, Reason: reasonUpgradeConverged,
			Message: fmt.Sprintf("running %s", cluster.Spec.TalosVersion),
		})

		if err := a.Status().Update(ctx, m); err != nil {
			return false, err
		}
	}

	return true, nil
}

// nodeReady reports whether hostname's Kubernetes Node is Ready.
func (a *talosUpgradeAdvancer) nodeReady(ctx context.Context, hostname string) (bool, error) {
	var node corev1.Node
	if err := a.Get(ctx, types.NamespacedName{Name: hostname}, &node); err != nil {
		return false, err
	}

	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue, nil
		}
	}

	return false, nil
}

// evacuate drives one storage.EvacuationStatus step for m's node through
// the shared evacuateMachine (evacuation.go), which removal.go calls too.
// On EvacuationDone it deliberately does NOT call End yet: the node is
// unschedulable until the upgrade's drain-and-reboot is actually confirmed
// to have worked, by finish's own call to endEvacuation, never before
// (architecture.md, "Storage": "...then drain; afterwards, re-enable
// scheduling").
func (a *talosUpgradeAdvancer) evacuate(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) (storage.EvacuationProgress, error) {
	return evacuateMachine(ctx, a.Client, a.Evacuator, cluster, m, "upgrading")
}

// endEvacuation undoes a pending evacuation (re-enables scheduling); see
// endMachineEvacuation.
func (a *talosUpgradeAdvancer) endEvacuation(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) error {
	return endMachineEvacuation(ctx, a.Client, a.Evacuator, cluster, m)
}

// installerImage builds cluster's target installer image reference:
// factory.talos.dev/metal-installer/<schematic>:<talosVersion>, Image
// Factory's own documented URL shape.
func installerImage(cluster *ichorv1alpha1.TalosCluster) string {
	schematic := defaultInstallerSchematic
	if cluster.Spec.Schematic != nil && cluster.Spec.Schematic.ID != "" {
		schematic = cluster.Spec.Schematic.ID
	}

	return fmt.Sprintf("%s/%s-installer/%s:%s", imageFactoryHost, installerPlatform, schematic, cluster.Spec.TalosVersion)
}

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
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/secureboot"
	"github.com/maeshinshin/ichor/pkg/storage"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// The installer image reference is built by pkg/secureboot (the shape github.com/siderolabs/talos/pkg/images.
// NewInstallerImage builds; reimplemented because that package lives in the top-level talos
// module, not the pkg/machinery submodule).
const (
	// conditionTalosUpgrade is a TalosMachine resumability marker (like removal.go's
	// conditionRemoval), True from the moment the Upgrade API call is acknowledged until the node
	// is confirmed on the target version. Message carries the boot ID recorded just before the
	// call, the only way to tell "hasn't rebooted yet" from "rebooted and fell back to the same
	// old version".
	conditionTalosUpgrade  = "TalosUpgrade"
	reasonUpgradeRequested = "UpgradeRequested"
	reasonUpgradeConverged = "Converged"

	// upgradeCallTimeout bounds the Upgrade RPC itself, which blocks while the node pulls and
	// installs the installer image.
	upgradeCallTimeout = 10 * time.Minute

	// talosUpgradeVerifyTimeout bounds how long a node may take to reboot onto the target after its
	// Upgrade call was acknowledged. It is the clock of conditionTalosUpgrade, the same window
	// kubernetesVerifyTimeout gives a Kubernetes upgrade, and it bounds the lock renewal for that step.
	talosUpgradeVerifyTimeout = 20 * time.Minute
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

	settling := func(err error) error {
		if rebooting {
			return nil
		}

		return err
	}

	talos, err := a.TalosClient(dialCtx, cluster, m, bundle)
	if err != nil {
		return false, settling(fmt.Errorf("dialing %q: %w", m.Name, err))
	}
	defer closeTalosClient(talos)

	if err := confirmMachine(dialCtx, talos, m); err != nil {
		return false, settling(err)
	}

	currentVersion, err := talos.Version(dialCtx)
	if err != nil {
		return false, settling(fmt.Errorf("reading %q's Talos version: %w", m.Name, err))
	}

	currentSchematic, err := talos.Schematic(dialCtx)
	if err != nil {
		return false, settling(fmt.Errorf("reading %q's schematic: %w", m.Name, err))
	}

	// A node that did not boot an Image Factory asset is running the stock installer's schematic.
	if currentSchematic == "" {
		currentSchematic = upgrade.DefaultSchematic
	}

	// A machine needs the upgrade when its running version OR its running schematic differs
	// from the target: a schematic-only change reinstalls the same release
	// with different extensions.
	upToDate := currentVersion == target && currentSchematic == targetSchematic(cluster)

	switch {
	case rebooting:
		return a.afterReboot(ctx, dialCtx, talos, cluster, m, upToDate, currentVersion, target)
	case !upToDate:
		return a.beginUpgrade(ctx, talos, cluster, m, currentVersion, target)
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
	ctx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	currentVersion, target string,
) (bool, error) {
	if err := upgrade.TalosPath(currentVersion, target); err != nil {
		return false, fmt.Errorf("talos upgrade path %s -> %s is not supported: %w", currentVersion, target, err)
	}

	if err := upgrade.KubernetesPairing(cluster.Spec.KubernetesVersion, target); err != nil {
		return false, fmt.Errorf("kubernetes %s is not supported with talos %s: %w", cluster.Spec.KubernetesVersion, target, err)
	}

	node, err := resolveNodeName(ctx, a.Client, talos, m)
	if err != nil {
		return false, err
	}

	ready, err := nodeReadyByName(ctx, nil, a.Client, node)
	if err != nil {
		return false, fmt.Errorf("checking %q's node readiness: %w", m.Name, err)
	}

	if !ready {
		return false, fmt.Errorf("node %q is not Ready; refusing to start an upgrade", node)
	}

	progress, err := evacuateMachine(ctx, a.Client, a.Evacuator, cluster, m, node, "upgrading")
	if err != nil {
		return false, err
	}

	switch progress {
	case storage.EvacuationWaiting:
		return false, nil
	case storage.EvacuationPaused:
		return false, fmt.Errorf("evacuating %q timed out with storage replicas still remaining", node)
	}

	// Fresh bound: evacuation may have taken longer than dialCtx lives.
	bootCtx, cancelBoot := dialBound(ctx)
	bootID, err := talos.BootID(bootCtx)

	cancelBoot()

	if err != nil {
		return false, fmt.Errorf("reading %q's boot ID before upgrading: %w", m.Name, err)
	}

	// The Upgrade RPC returns only after the node has pulled the installer image and
	// written it, which takes minutes -- far past the 15s dial bound dialCtx carries
	// (observed on a real node: DeadlineExceeded, operation paused, node untouched).
	upgradeCtx, cancelUpgrade := context.WithTimeout(ctx, upgradeCallTimeout)
	defer cancelUpgrade()

	if err := talos.Upgrade(upgradeCtx, installerImage(cluster, a.secureBootInstaller(ctx, talos, cluster, m))); err != nil {
		return false, fmt.Errorf("upgrade API call to %q failed: %w", m.Name, err)
	}

	if err := setMachineCondition(ctx, a.Client, a.Client, m, metav1.Condition{
		Type: conditionTalosUpgrade, Status: metav1.ConditionTrue, Reason: reasonUpgradeRequested,
		Message: bootID,
	}); err != nil {
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
	upToDate bool, currentVersion, target string,
) (bool, error) {
	cond := apimeta.FindStatusCondition(m.Status.Conditions, conditionTalosUpgrade)

	bootID, err := talos.BootID(dialCtx)
	if err != nil {
		return false, nil // transient: treat like any other hiccup while rebooting
	}

	if cond != nil && cond.Message == bootID {
		return false, nil // hasn't rebooted yet
	}

	if !upToDate {
		if endErr := endEvacuationOf(ctx, a.Client, a.Evacuator, talos, cluster, m); endErr != nil {
			return false, endErr
		}

		return false, fmt.Errorf(
			"node %q rebooted after an upgrade to %s but is now running %s: it fell back to its previous A/B slot",
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
	node, err := resolveNodeName(ctx, a.Client, talos, m)
	if err != nil {
		if apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionTalosUpgrade) {
			return false, nil // right after the reboot the node may not report its name yet
		}

		return false, err
	}

	ready, err := nodeReadyByName(ctx, nil, a.Client, node)
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

		if !etcdMemberPresent(members, node) {
			return false, nil
		}
	}

	if err := endEvacuationOf(ctx, a.Client, a.Evacuator, talos, cluster, m); err != nil {
		return false, err
	}

	if apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionTalosUpgrade) {
		if err := setMachineCondition(ctx, a.Client, a.Client, m, metav1.Condition{
			Type: conditionTalosUpgrade, Status: metav1.ConditionFalse, Reason: reasonUpgradeConverged,
			Message: fmt.Sprintf("running %s (schematic %s)", cluster.Spec.TalosVersion, targetSchematic(cluster)),
		}); err != nil {
			return false, err
		}
	}

	return true, nil
}

// installerImage builds cluster's target installer image reference:
// factory.talos.dev/metal-installer/<schematic>:<talosVersion>, Image
// Factory's own documented URL shape.
//
// secureBoot selects the signed UKI variant, metal-installer-secureboot.
func installerImage(cluster *ichorv1alpha1.TalosCluster, secureBoot bool) string {
	return secureboot.ClusterInstallerImage(cluster, secureBoot)
}

// secureBootInstaller decides the installer variant for m (secureboot.UseSecureBootInstaller): from
// what the node says now, else from what was last recorded of it.
func (a *talosUpgradeAdvancer) secureBootInstaller(
	ctx context.Context, talos TalosClient, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) bool {
	callCtx, cancel := dialBound(ctx)
	defer cancel()

	node := m.Status.Security
	if ss, err := talos.SecurityState(callCtx); err == nil {
		node = ss.status()
	}

	return secureboot.UseSecureBootInstaller(cluster.Spec.Security, node)
}

// targetSchematic is the Image Factory schematic cluster's nodes should run: spec.schematic.id, or
// the stock installer's when unset.
func targetSchematic(cluster *ichorv1alpha1.TalosCluster) string {
	if cluster.Spec.Schematic != nil && cluster.Spec.Schematic.ID != "" {
		return cluster.Spec.Schematic.ID
	}

	return upgrade.DefaultSchematic
}

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
	"cmp"
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Re-provisioning: an approved machine whose node fell back to maintenance mode (its disk was wiped
// or replaced) is sent its rendered configuration again by provision() in talosmachine_provision.go,
// on its own. The checks are the first provisioning's: the identity must match the approval, the
// SecureBoot requirement, the install disk and, for a control plane, the cluster lock. A control
// plane's etcd member, which the wiped disk can no longer answer for, is removed through a surviving
// control plane before the configuration is sent (ADR 0076, amendment of 2026-11-05).

const (
	reasonReprovisionBlocked = "ReprovisionBlocked"
	reasonStaleEtcdMember    = "StaleEtcdMember"

	// reprovisionProbeTimeout bounds the maintenance probe of a machine that had converged. It is
	// made every round the node does not answer, and a node that is simply down never answers it.
	reprovisionProbeTimeout = 5 * time.Second
)

// reprovisioned records that the configuration was stored again on a machine that had converged
// before: it is followed to running like a first provisioning, so what it had converged to is
// forgotten (which is also what stops a second send while the node is still in maintenance mode).
func (r *TalosMachineReconciler) reprovisioned(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, hash string,
) (ctrl.Result, error) {
	m.Status.AppliedConfigHash, m.Status.ObservedInputsHash = "", ""
	m.Status.RebootPaths, m.Status.APIServerRestartPaths = nil, nil

	message := "Ichor re-installed this node: it fell back to maintenance mode with its recorded identity (its disk was wiped " +
		"or replaced), so its configuration " + hash[:12] + " was sent again (this only means stored); waiting for install, reboot and join"
	r.event(m, corev1.EventTypeWarning, "NodeReinstalled", message)

	return r.following(ctx, m, reasonConfigStored, message)
}

// removeStaleEtcdMember removes the etcd member of the wiped control plane m, when etcd still lists it,
// and returns a reason and message when the configuration must not be sent: the member could not be
// looked up or removed. A node that joins under the name of a listed member does not replace it.
//
// Removing it is safe because the wiped node has proven it cannot be that member any more: it answers
// the maintenance API with the approved identity, so its etcd data is gone (a stronger finding than
// the unreachable member ADR 0106 removes). The member is removed through another approved control
// plane, the same call a dead-member removal makes, and etcd refuses it without a quorum; the caller
// holds the cluster lock. ponytail: only the first other control plane is asked.
func (r *TalosMachineReconciler) removeStaleEtcdMember(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) (reason, message string) {
	blocked := func(why string) (string, string) {
		return reasonReprovisionBlocked, "nothing was sent: " + why
	}

	name := cmp.Or(m.Spec.Hostname, m.Status.NodeName)
	if name == "" {
		return blocked("this control plane's etcd member cannot be looked up because neither spec.hostname nor a node name is recorded; set spec.hostname")
	}

	bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, cluster)
	if err != nil {
		return blocked("the secrets bundle is unavailable, so etcd cannot be asked whether the member still exists")
	}

	survivor, err := r.findEtcdSurvivor(ctx, m)
	if err != nil {
		return blocked(fmt.Sprintf("%v. If this was the cluster's only control plane its etcd data is gone: recover the cluster "+
			"from a backup with `ichor restore --recover-cluster`", err))
	}

	ctx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	sc, err := r.TalosClient(ctx, cluster, survivor, bundle)
	if err != nil {
		return blocked(fmt.Sprintf("dialling the control plane %q to read etcd failed", survivor.Name))
	}
	defer closeTalosClient(sc)

	members, err := sc.EtcdMemberList(ctx)
	if err != nil {
		return blocked(fmt.Sprintf("listing the etcd members from %q failed: %s", survivor.Name, shortError(err)))
	}

	id, found := etcdMemberID(members, name)
	if !found {
		return "", ""
	}

	if err := sc.EtcdRemoveMemberByID(ctx, id); err != nil {
		return reasonStaleEtcdMember, fmt.Sprintf("etcd still lists a member named %q for this control plane and removing it "+
			"through %q failed (%s), so nothing was sent; remove the machine with `ichor node remove %s`, which removes its etcd "+
			"member too, and approve it again", name, survivor.Name, shortError(err), m.Name)
	}

	r.event(m, corev1.EventTypeWarning, "StaleEtcdMemberRemoved", fmt.Sprintf("the node was wiped, so its etcd member %q was removed "+
		"through control plane %q before its configuration was sent again", name, survivor.Name))

	return "", ""
}

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

	apimeta "k8s.io/apimachinery/pkg/api/meta"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// deadEtcdMember decides whether the etcd member of the control plane m, which is being removed below
// the declared count, is gone. Replacing a dead control plane needs it removed first, since the
// replacement's etcd cannot join while the dead member is still listed, and "approve a replacement
// first" (the add-then-remove rule) cannot finish. Read through a healthy control plane, never through
// m: a healthy sibling must list the member, and m itself must not answer its etcd status or must see
// no leader. A member that answers with a leader is healthy, and the refusal stands. why says what was
// found either way; dead is false whenever it could not be told (no healthy sibling, no name).
func (r *TalosMachineReconciler) deadEtcdMember(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) (why string, dead bool) {
	if r.TalosClient == nil {
		return "no Talos API connectivity is configured, so etcd could not be asked", false
	}

	bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, cluster)
	if err != nil {
		return "the secrets bundle could not be loaded, so etcd could not be asked", false
	}

	nodeName, err := r.removalNodeName(ctx, cluster, m)
	if err != nil || nodeName == "" {
		return "its etcd member name is unknown (set spec.hostname)", false
	}

	survivor, err := r.findEtcdSurvivor(ctx, m)
	if err != nil {
		return "no other control plane to read etcd through", false
	}

	sCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	sc, err := r.TalosClient(sCtx, cluster, survivor, bundle)
	if err != nil {
		return fmt.Sprintf("control plane %q could not be dialed to read etcd", survivor.Name), false
	}
	defer closeTalosClient(sc)

	members, err := sc.EtcdMemberList(sCtx)
	if err != nil {
		return fmt.Sprintf("control plane %q could not list the etcd members", survivor.Name), false
	}

	if !etcdMemberPresent(members, nodeName) {
		// Gone from etcd already: either this removal took it out on an earlier round (it recorded
		// that it was proceeding for a dead member), or the name does not match and nothing is known.
		if c := apimeta.FindStatusCondition(m.Status.Conditions, conditionRemoval); c != nil && wentOnForDeadMember(c.Reason) {
			return "already removed from etcd by this removal", true
		}

		return fmt.Sprintf("not listed by etcd (read through %q), so there is nothing to prove it is dead", survivor.Name), false
	}

	mCtx, cancelM := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancelM()

	self, err := r.TalosClient(mCtx, cluster, m, bundle)
	if err != nil {
		return fmt.Sprintf("unreachable (%s)", shortError(err)), true
	}
	defer closeTalosClient(self)

	st, err := self.EtcdStatus(mCtx)
	if err != nil {
		return fmt.Sprintf("unreachable (%s)", shortError(err)), true
	}

	if st.LeaderID == 0 {
		return "reporting no leader", true
	}

	return fmt.Sprintf("healthy (member %d sees leader %d)", st.MemberID, st.LeaderID), false
}

// wentOnForDeadMember reports whether reason, the Removal condition's, shows that an earlier round of this
// removal passed the count check for a dead member and carried on: the later steps replace the reason
// DeadMemberRemoval, and a round that resumes after the member left etcd must still know why it proceeds.
// (A reset that Talos accepted is handled before the count check and needs no entry here.)
func wentOnForDeadMember(reason string) bool {
	return reason == reasonDeadMemberRemoval || reason == reasonResetFailed
}

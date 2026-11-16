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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
)

const ownNodeLockNode = "node-lock-self"

// expectLeaseFree fails unless another operation can take the cluster lease now, which is what the reconcile that
// stopped on an unreadable own machine must leave possible. It gives the lease back.
func expectLeaseFree(lock *clusterlock.Lock) {
	GinkgoHelper()

	const stranger = controllerIdentity + "/op/" + tmNamespace + "/another"

	Expect(lock.Acquire(ctx, stranger)).To(Succeed(), "the stopped operation still holds the cluster lease")
	Expect(lock.Release(ctx, stranger)).To(Succeed())
}

// When the controller cannot tell which machine it runs on, the operation stops. It must not keep the cluster lock
// while it waits (the retry would renew it), and it must say why on the operation, and go on by itself when the
// machine can be told.
var _ = Describe("a TalosOperation that cannot tell which machine runs the controller", func() {
	It("frees the cluster lock for another operation, says why, and resumes when the machine can be told (rollout)", func() {
		cluster := newOpTestCluster()
		cp := newOpTestMachine(cluster.Name, "controlplane", "oul-cp1") // no spec.hostname and no Node at its address
		op := newOpTestOperationOfKind(cluster.Name, ichorv1alpha1.TalosOperationKindKubernetes)

		r, fake := newOpTestReconciler(nil)
		r.NodeName = ownNodeLockNode

		res, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(operationRequeueInterval), "the operation retries by itself")
		Expect(fake.calls).To(BeEmpty())

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).To(BeEmpty(), "a failure reason would make the pause permanent")
		expectPausedBecause(got, reasonOperationOwnNodeUnread)
		Expect(pausedMessage(got)).To(ContainSubstring("cannot tell which machine runs this controller"))

		expectLeaseFree(opTestLock(cluster))

		setHostname(cp, ownNodeLockNode)
		got = reconcileFetch(r, op)
		Expect(fake.calls).To(Equal([]string{cp.Name}))
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionOperationPaused)).To(BeNil())
	})

	It("frees the cluster lock for another operation and says why when a shutdown stops (shutdownPlan)", func() {
		f := newShutdownFixture()
		f.r.NodeName = ownNodeLockNode // every fake answers with no node name, and there is no Node

		op := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)
		res, err := reconcileOperation(f.r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(operationRequeueInterval))
		Expect(f.log).To(BeEmpty(), "nothing is stopped while the controller's machine is unknown")

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		expectPausedBecause(got, reasonOperationOwnNodeUnread)

		expectLeaseFree(opTestLock(f.cluster))

		f.fakes[f.workers[0].Name].nodeName = ownNodeLockNode
		got = f.reconcile(op)
		Expect(f.log).To(Equal([]string{f.workers[1].Name}))
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionOperationPaused)).To(BeNil())
	})
})

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// approveAt stamps m's approvalDecision.at, as the webhook-bypassing writer in
// these tests (a GitOps apply, or a race admission could not close) would.
func approveAt(m *ichorv1alpha1.TalosMachine, at time.Time) {
	got := fetchMachine(m)
	got.Spec.ApprovalDecision = &ichorv1alpha1.TalosMachineApprovalDecision{At: metav1.NewTime(at)}
	Expect(k8sClient.Update(ctx, got)).To(Succeed())
}

// reconcileTwice runs the finalizer-adding first pass and then the real one.
func reconcileTwice(r *TalosMachineReconciler, m *ichorv1alpha1.TalosMachine) {
	for range 2 {
		reconcileOK(r, m)
	}
}

var _ = Describe("TalosMachineReconciler control-plane approval cap", func() {
	var (
		cluster *ichorv1alpha1.TalosCluster
		base    time.Time
	)

	reconcilerFor := func() (*TalosMachineReconciler, *fakeTalosClient) {
		fake := &fakeTalosClient{}
		return &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(fake),
		}, fake
	}

	BeforeEach(func() {
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster = newTestClusterWithCount(1, secret.Name)
		base = time.Now().Add(-time.Hour).Truncate(time.Second)
	})

	It("holds only the newest approval beyond count+1, applies nothing to it, and releases it when a sibling goes", func() {
		older := newTestControlPlane(cluster.Name, "1")
		middle := newTestControlPlane(cluster.Name, "2")
		newest := newTestControlPlane(cluster.Name, "3")
		approveAt(older, base)
		approveAt(middle, base.Add(time.Minute))
		approveAt(newest, base.Add(2*time.Minute))

		r, fake := reconcilerFor()
		reconcileTwice(r, newest)

		got := fetchMachine(newest)
		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonControlPlaneOverCap))
		Expect(fake.dryRunCalls).To(Equal(0))
		Expect(fake.applyCalls).To(BeEmpty())

		// The older two are not held.
		rOld, _ := reconcilerFor()
		reconcileTwice(rOld, middle)
		if c := readyCondition(fetchMachine(middle)); c != nil {
			Expect(c.Reason).NotTo(Equal(reasonControlPlaneOverCap))
		}

		// An operator removes one approval: the hold lifts on the next reconcile.
		gone := fetchMachine(middle)
		gone.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateRejected
		Expect(k8sClient.Update(ctx, gone)).To(Succeed())

		reconcileOK(r, newest)

		if c := readyCondition(fetchMachine(newest)); c != nil {
			Expect(c.Reason).NotTo(Equal(reasonControlPlaneOverCap))
		}
	})

	It("breaks an equal approval time by name, the later name being held", func() {
		a := newTestControlPlane(cluster.Name, "1")
		b := newTestControlPlane(cluster.Name, "2")
		c := newTestControlPlane(cluster.Name, "3")
		for _, m := range []*ichorv1alpha1.TalosMachine{a, b, c} {
			approveAt(m, base)
		}

		last := a
		for _, m := range []*ichorv1alpha1.TalosMachine{b, c} {
			if m.Name > last.Name {
				last = m
			}
		}

		r, _ := reconcilerFor()
		reconcileTwice(r, last)
		Expect(readyCondition(fetchMachine(last)).Reason).To(Equal(reasonControlPlaneOverCap))
	})

	It("does not hold anything at count+1, the replacement state", func() {
		older := newTestControlPlane(cluster.Name, "1")
		newer := newTestControlPlane(cluster.Name, "2")
		approveAt(older, base)
		approveAt(newer, base.Add(time.Minute))

		r, _ := reconcilerFor()
		reconcileTwice(r, newer)

		if c := readyCondition(fetchMachine(newer)); c != nil {
			Expect(c.Reason).NotTo(Equal(reasonControlPlaneOverCap))
		}
	})
})

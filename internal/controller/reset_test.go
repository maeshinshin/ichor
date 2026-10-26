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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// optIntoReset annotates resetOnDeleteAnnotation and deletes m. Re-fetches
// first: these tests reconcile m through its own first pass (adding
// machineRemovalFinalizer) before calling this, which bumps m's real
// ResourceVersion without this package's own local pointer ever seeing
// it -- Reconcile always re-Gets into its own local variable, never back
// into the caller's. CheckDelete's allow-delete annotation is this
// package's admission webhook's own gate, never wired into these plain
// envtest specs, so it needs no equivalent here.
func optIntoReset(m *ichorv1alpha1.TalosMachine) {
	got := fetchMachine(m)
	if got.Annotations == nil {
		got.Annotations = map[string]string{}
	}
	got.Annotations[resetOnDeleteAnnotation] = resetOnDeleteValue
	Expect(k8sClient.Update(ctx, got)).To(Succeed())
	Expect(k8sClient.Delete(ctx, got)).To(Succeed())
}

var _ = Describe("resetNode", func() {
	It("does not reset, and still warns about lingering credentials, when the removal did not opt in", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestClusterWithCount(1, secret.Name)
		cp1 := newTestControlPlane(cluster.Name, "r1")
		cp2 := newTestControlPlane(cluster.Name, "r2")
		cp3 := newTestControlPlane(cluster.Name, "r3")

		fake := &fakeTalosClient{nodeName: "reset-node"}
		recorder := record.NewFakeRecorder(10)
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(fake), Recorder: recorder,
		}
		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2, cp3} {
			_, err := reconcileMachine(r, cp)
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(k8sClient.Delete(ctx, cp1)).To(Succeed())
		_, err := reconcileMachine(r, cp1)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.resetCalls).To(Equal(0))
		Eventually(recorder.Events).Should(Receive(ContainSubstring(reasonNodeStillHasClusterCredentials)))

		var gone ichorv1alpha1.TalosMachine
		err = k8sClient.Get(ctx, machineKeyOf(cp1), &gone)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("resets the node when the removal opts in, clears the finalizer, and does not warn about credentials", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestClusterWithCount(1, secret.Name)
		cp1 := newTestControlPlane(cluster.Name, "r4")
		cp2 := newTestControlPlane(cluster.Name, "r5")
		cp3 := newTestControlPlane(cluster.Name, "r6")

		fake := &fakeTalosClient{nodeName: "reset-node"}
		recorder := record.NewFakeRecorder(10)
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(fake), Recorder: recorder,
		}
		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2, cp3} {
			_, err := reconcileMachine(r, cp)
			Expect(err).NotTo(HaveOccurred())
		}

		optIntoReset(cp1)
		_, err := reconcileMachine(r, cp1)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.resetCalls).To(Equal(1))
		Consistently(recorder.Events).ShouldNot(Receive())

		var gone ichorv1alpha1.TalosMachine
		err = k8sClient.Get(ctx, machineKeyOf(cp1), &gone)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "a successful opt-in reset must still let the removal complete")
	})

	It("holds the finalizer and does not clear it when Reset fails", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestClusterWithCount(1, secret.Name)
		cp1 := newTestControlPlane(cluster.Name, "r7")
		cp2 := newTestControlPlane(cluster.Name, "r8")
		cp3 := newTestControlPlane(cluster.Name, "r9")

		fake := &fakeTalosClient{nodeName: "reset-node", resetErr: fmt.Errorf("reset failed")}
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(fake),
		}
		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2, cp3} {
			_, err := reconcileMachine(r, cp)
			Expect(err).NotTo(HaveOccurred())
		}

		optIntoReset(cp1)
		_, err := reconcileMachine(r, cp1)
		Expect(err).NotTo(HaveOccurred())

		got := fetchMachine(cp1)
		cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionRemoval)
		Expect(cond.Reason).To(Equal(reasonResetFailed))
		Expect(got.Finalizers).To(ContainElement(machineRemovalFinalizer))
		Expect(fake.resetCalls).To(Equal(1))
	})

	It("does not ask Talos to reset twice: a resumed reconcile that already recorded the request skips it", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestClusterWithCount(1, secret.Name)
		cp1 := newTestControlPlane(cluster.Name, "r10")
		cp2 := newTestControlPlane(cluster.Name, "r11")
		cp3 := newTestControlPlane(cluster.Name, "r12")

		fake := &fakeTalosClient{nodeName: "reset-node"}
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(fake),
		}
		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2, cp3} {
			_, err := reconcileMachine(r, cp)
			Expect(err).NotTo(HaveOccurred())
		}

		optIntoReset(cp1)

		// Simulate a crash immediately after Reset's own RPC acknowledged the
		// request, before the removal otherwise completed: write exactly the
		// condition resetNode itself would have written, without ever calling
		// Reset through the fake. Re-fetched first: optIntoReset's own Update
		// and Delete calls moved cp1's ResourceVersion on past what this
		// test's own local copy still holds.
		got := fetchMachine(cp1)
		got.Status.Conditions = []metav1.Condition{{
			Type: conditionRemoval, Status: metav1.ConditionTrue, Reason: reasonResetRequested,
			Message: "reset requested", LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		_, err := reconcileMachine(r, cp1)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.resetCalls).To(Equal(0), "already requested -- a fire-and-forget RPC has nothing left to retry")

		var gone ichorv1alpha1.TalosMachine
		err = k8sClient.Get(ctx, machineKeyOf(cp1), &gone)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the resumed reconcile must still complete the removal")
	})
})

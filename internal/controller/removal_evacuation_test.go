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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/storage"
)

// evacuatingRemoval sets up a 4-control-plane cluster declared at 3 (so
// removing the first passes every removal check, as in removal_test.go),
// gives it a Kubernetes Node of the same name, and returns a reconciler
// wired with evacuator and a fake kube client that records any drain call.
// The returned machine has already been deleted.
func evacuatingRemoval(hostname string, evacuator StorageEvacuator) (
	*TalosMachineReconciler, *kubefake.Clientset, *ichorv1alpha1.TalosMachine,
) {
	r, secretName := removalReadyReconciler()
	cluster := newTestClusterWithCount(3, secretName)

	cps := make([]*ichorv1alpha1.TalosMachine, 4)
	for i, suffix := range []string{"41", "42", "43", "44"} {
		cps[i] = newTestControlPlane(cluster.Name, suffix)
		reconcileOK(r, cps[i])
	}

	setHostname(fetchMachine(cps[0]), hostname)
	newTestNode(hostname, true)

	fakeNode := &corev1.Node{}
	fakeNode.Name = hostname
	kube := kubefake.NewSimpleClientset(fakeNode)
	r.KubeClient = kube
	r.Evacuator = evacuator

	Expect(k8sClient.Delete(ctx, cps[0])).To(Succeed())

	return r, kube, cps[0]
}

func machineGone(m *ichorv1alpha1.TalosMachine) bool {
	return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(m), &ichorv1alpha1.TalosMachine{}))
}

var _ = Describe("TalosMachineReconciler removal: storage evacuation", func() {
	longhorn := func(timeout time.Duration) *evacuationTarget {
		hook, err := storage.NewEvacuationHook(storage.LonghornV1)
		Expect(err).NotTo(HaveOccurred())

		return &evacuationTarget{hook: hook, timeout: timeout, namespace: defaultLonghornNamespace}
	}

	It("evacuates first and does not drain or remove until the replicas are gone, resuming from status", func() {
		ev := &fakeStorageEvacuator{target: longhorn(time.Hour), remaining: 1}
		r, kube, cp := evacuatingRemoval("node-rm-evac1", ev)

		_, err := reconcileMachine(r, cp)
		Expect(err).To(HaveOccurred(), "waiting must requeue, which Reconcile only does on error")
		Expect(ev.beginCalls).To(Equal(1))

		got := fetchMachine(cp)
		Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, conditionStorageEvacuation)).To(BeTrue())
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionRemoval).Reason).To(Equal(reasonEvacuationPending))

		// A brand new reconciler (controller restart) still waits, and does not Begin again.
		r2 := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: r.TalosClient, KubeClient: kube, Evacuator: ev,
		}
		_, err = reconcileMachine(r2, cp)
		Expect(err).To(HaveOccurred())
		Expect(ev.beginCalls).To(Equal(1))
		Expect(kube.Actions()).To(BeEmpty(), "no drain may start while replicas remain")
		Expect(machineGone(cp)).To(BeFalse())

		ev.remaining = 0

		reconcileOK(r2, cp)
		Expect(kube.Actions()).NotTo(BeEmpty(), "drain runs once evacuation is done")
		Expect(machineGone(cp)).To(BeTrue())
		Expect(ev.endCalls).To(BeZero())
	})

	It("stops the removal on evacuation timeout, re-enables scheduling, and never drains", func() {
		ev := &fakeStorageEvacuator{target: longhorn(time.Minute), remaining: 2}
		r, kube, cp := evacuatingRemoval("node-rm-evac2", ev)

		_, err := reconcileMachine(r, cp)
		Expect(err).To(HaveOccurred())

		got := fetchMachine(cp)
		cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionStorageEvacuation)
		Expect(cond).NotTo(BeNil())
		cond.LastTransitionTime = metav1.NewTime(time.Now().Add(-2 * time.Minute))
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		_, err = reconcileMachine(r, cp)
		Expect(err).To(MatchError(ContainSubstring("timed out")))
		Expect(ev.endCalls).To(Equal(1))
		Expect(kube.Actions()).To(BeEmpty())

		got = fetchMachine(cp)
		Expect(controllerutil.ContainsFinalizer(got, machineRemovalFinalizer)).To(BeTrue())
		removal := apimeta.FindStatusCondition(got.Status.Conditions, conditionRemoval)
		Expect(removal.Status).To(Equal(metav1.ConditionFalse))
		Expect(removal.Reason).To(Equal(reasonEvacuationFailed))
	})

	It("removes as before when no TalosStorage selects the node", func() {
		ev := &fakeStorageEvacuator{target: nil}
		r, kube, cp := evacuatingRemoval("node-rm-evac3", ev)

		reconcileOK(r, cp)
		Expect(ev.beginCalls).To(BeZero())
		Expect(kube.Actions()).NotTo(BeEmpty())
		Expect(machineGone(cp)).To(BeTrue())
	})

	It("removes as before when no evacuator is configured", func() {
		r, kube, cp := evacuatingRemoval("node-rm-evac4", nil)

		reconcileOK(r, cp)
		Expect(kube.Actions()).NotTo(BeEmpty())
		Expect(machineGone(cp)).To(BeTrue())
	})
})

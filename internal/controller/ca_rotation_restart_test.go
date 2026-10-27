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

	appsv1 "k8s.io/api/apps/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/maeshinshin/ichor/internal/oidctrust"
	clienttesting "k8s.io/client-go/testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// flannelNodes is the fake DaemonSet's size; the fake has no
// controllers, so a rollout is "finished" exactly when its status says so.
const flannelNodes = 5

// restartFixture is a CA rotation whose in-cluster clients are a fake clientset: CoreDNS and ichor-ui
// (available) and, when flannelAvailable >= 0, a kube-flannel DaemonSet (no kube-proxy, no Cilium).
func restartFixture(flannelAvailable int32) (*caRotFixture, *fake.Clientset) {
	f := newCARotFixture()
	one := int32(1)

	dep := func(ns, name string) *appsv1.Deployment {
		return &appsv1.Deployment{
			Namespace: ns, Name: name, Generation: 1,
			Spec:   appsv1.DeploymentSpec{Replicas: &one},
			Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1},
		}
	}

	uiDep := func(d *appsv1.Deployment) *appsv1.Deployment {
		d.Labels = map[string]string{oidctrust.UIComponentLabel: oidctrust.UIComponentValue}

		return d
	}

	kc := fake.NewClientset(dep(kubeSystemNamespace, corednsName), uiDep(dep(tmNamespace, "ichor-ichor-ui")))
	if flannelAvailable >= 0 {
		_, err := kc.AppsV1().DaemonSets(kubeSystemNamespace).Create(ctx, &appsv1.DaemonSet{
			Namespace: kubeSystemNamespace, Name: "kube-flannel", Generation: 1,
			Status: appsv1.DaemonSetStatus{
				ObservedGeneration: 1, DesiredNumberScheduled: flannelNodes, UpdatedNumberScheduled: flannelNodes,
				NumberAvailable: flannelAvailable,
			},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
	}

	f.kube = kc

	return f, kc
}

func patchCount(kc *fake.Clientset) map[string]int {
	n := map[string]int{}

	for _, a := range kc.Actions() {
		if p, ok := a.(clienttesting.PatchAction); ok {
			n[a.GetResource().Resource+"/"+p.GetName()]++
		}
	}

	return n
}

var _ = Describe("TalosOperation ca-rotation client restarts", func() {
	var oldTimeout time.Duration

	BeforeEach(func() { oldTimeout = caClientRestartTimeout })
	AfterEach(func() { caClientRestartTimeout = oldTimeout })

	restarting := func(op *ichorv1alpha1.TalosOperation) bool {
		c := apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation)

		return c != nil && c.Reason == reasonCARotationRestarting
	}

	It("restarts CoreDNS, the CNI and ichor-ui once, only after the old CAs are dropped, and resumes without repeating", func() {
		f, kc := restartFixture(flannelNodes)
		op := f.newOp()

		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageFinalizing))
		Expect(patchCount(kc)).To(BeEmpty(), "nothing is restarted while the old CAs are still accepted")

		op, _ = f.runUntil(op, completed)
		Expect(patchCount(kc)).To(Equal(map[string]int{
			"deployments/coredns": 1, "daemonsets/kube-flannel": 1, "deployments/ichor-ichor-ui": 1,
		}))

		d, err := kc.AppsV1().Deployments(kubeSystemNamespace).Get(ctx, corednsName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Spec.Template.Annotations).To(HaveKey(restartedAtAnnotation))
		Expect(d.Spec.Template.Annotations[caRestartMarker]).To(Equal(string(op.UID)))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).NotTo(ContainSubstring("not back"))

		// A controller that comes back to Finalizing finds the markers and patches nothing.
		run := &caRun{r: f.newReconciler(), op: op}
		_, note, done, err := run.restartClients(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(note).To(BeEmpty())
		Expect(patchCount(kc)["deployments/coredns"]).To(Equal(1))
	})

	It("waits in Finalizing while a restarted workload is not available, then completes", func() {
		f, kc := restartFixture(3)
		op := f.newOp()

		op, _ = f.runUntil(op, restarting)
		for range 3 {
			op = f.step(op)
		}

		Expect(stageOf(op)).To(Equal(ichorv1alpha1.CARotationStageFinalizing))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).To(ContainSubstring("kube-flannel"))
		Expect(patchCount(kc)["daemonsets/kube-flannel"]).To(Equal(1), "waiting must not restart again")

		ds, err := kc.AppsV1().DaemonSets(kubeSystemNamespace).Get(ctx, "kube-flannel", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		ds.Status.NumberAvailable = flannelNodes
		_, err = kc.AppsV1().DaemonSets(kubeSystemNamespace).UpdateStatus(ctx, ds, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		op, _ = f.runUntil(op, completed)
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).NotTo(ContainSubstring("not back"))
	})

	It("finishes the rotation, naming what is not back, once the wait is over", func() {
		f, _ := restartFixture(3)
		caClientRestartTimeout = time.Nanosecond
		op := f.newOp()

		op, _ = f.runUntil(op, completed)
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).
			To(And(ContainSubstring("not back after the restart"), ContainSubstring("kube-flannel")))
	})

	It("ignores a CNI DaemonSet that is not there", func() {
		f, kc := restartFixture(-1)
		op := f.newOp()

		op, _ = f.runUntil(op, completed)
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		Expect(patchCount(kc)).To(Equal(map[string]int{"deployments/coredns": 1, "deployments/ichor-ichor-ui": 1}))
	})
})

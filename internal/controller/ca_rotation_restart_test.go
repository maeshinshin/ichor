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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/maeshinshin/ichor/internal/oidctrust"
	clienttesting "k8s.io/client-go/testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ciliumNodes is the fake DaemonSet's size; the fake has no
// controllers, so a rollout is "finished" exactly when its status says so.
const ciliumNodes = 5

const (
	corednsPatchKey = "deployments/coredns"
	uiPatchKey      = "deployments/ichor-ichor-ui"

	operatorPatchKey = "deployments/cilium-operator"
)

// restartFixture is a CA rotation whose in-cluster clients are a fake clientset: CoreDNS and ichor-ui
// (available) and, when ciliumAvailable >= 0, a cilium DaemonSet and an available cilium-operator (no kube-proxy).
func restartFixture(ciliumAvailable int32) (*caRotFixture, *fake.Clientset) {
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
	if ciliumAvailable >= 0 {
		_, err := kc.AppsV1().DaemonSets(kubeSystemNamespace).Create(ctx, &appsv1.DaemonSet{
			Namespace: kubeSystemNamespace, Name: ciliumName, Generation: 1,
			Status: appsv1.DaemonSetStatus{
				ObservedGeneration: 1, DesiredNumberScheduled: ciliumNodes, UpdatedNumberScheduled: ciliumNodes,
				NumberAvailable: ciliumAvailable,
			},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		_, err = kc.AppsV1().Deployments(kubeSystemNamespace).Create(ctx, dep(kubeSystemNamespace, ciliumOperatorName), metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
	}

	f.kube = kc

	old := caKubeletSettleDelay
	caKubeletSettleDelay = time.Nanosecond

	DeferCleanup(func() { caKubeletSettleDelay = old })

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
		f, kc := restartFixture(ciliumNodes)
		op := f.newOp()

		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageFinalizing))
		Expect(patchCount(kc)).To(BeEmpty(), "nothing is restarted while the old CAs are still accepted")

		op, _ = f.runUntil(op, completed)
		Expect(patchCount(kc)).To(Equal(map[string]int{
			corednsPatchKey: 1, "daemonsets/cilium": 1, operatorPatchKey: 1, uiPatchKey: 1,
		}))

		d, err := kc.AppsV1().Deployments(kubeSystemNamespace).Get(ctx, corednsName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Spec.Template.Annotations).To(HaveKey(restartedAtAnnotation))
		Expect(d.Spec.Template.Annotations[caRestartMarker]).To(Equal(string(op.UID)))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).NotTo(ContainSubstring("not back"))

		// A controller that comes back to Finalizing finds the markers and patches nothing.
		run := &caRun{r: f.newReconciler(), op: op, cluster: &ichorv1alpha1.TalosCluster{}}
		_, note, done, err := run.restartClients(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(note).To(BeEmpty())
		Expect(patchCount(kc)[corednsPatchKey]).To(Equal(1))
	})

	It("waits in Finalizing while a restarted workload is not available, then completes", func() {
		f, kc := restartFixture(3)
		op := f.newOp()

		op, _ = f.runUntil(op, restarting)
		for range 3 {
			op = f.step(op)
		}

		Expect(rotationStage(op)).To(Equal(ichorv1alpha1.CARotationStageFinalizing))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).To(ContainSubstring("cilium"))
		Expect(patchCount(kc)["daemonsets/cilium"]).To(Equal(1), "waiting must not restart again")
		Expect(patchCount(kc)).NotTo(HaveKey(corednsPatchKey), "CoreDNS waits for the CNI to be available: it stays Pending on the agent-not-ready taint")
		Expect(patchCount(kc)).NotTo(HaveKey(uiPatchKey), "ichor-ui waits for the CNI to be available")

		ds, err := kc.AppsV1().DaemonSets(kubeSystemNamespace).Get(ctx, "cilium", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		ds.Status.NumberAvailable = ciliumNodes
		_, err = kc.AppsV1().DaemonSets(kubeSystemNamespace).UpdateStatus(ctx, ds, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		op, _ = f.runUntil(op, completed)
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).NotTo(ContainSubstring("not back"))
	})

	It("restarts nothing while a Node is not Ready, and rolls out the CNI before CoreDNS before ichor-ui", func() {
		f, kc := restartFixture(ciliumNodes)
		op := f.newOp()

		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageFinalizing))
		f.world.notReady = []string{"cp-restart-test"}

		for range 3 {
			op = f.step(op)
		}

		Expect(patchCount(kc)).To(BeEmpty(), "no restart before the replaced bundle is verified and the Nodes are Ready")

		f.world.notReady = nil
		_, _ = f.runUntil(op, completed)

		var order []string

		for _, a := range kc.Actions() {
			if _, ok := a.(clienttesting.PatchAction); ok {
				order = append(order, a.GetResource().Resource+"/"+a.(clienttesting.PatchAction).GetName())
			}
		}

		Expect(order).To(Equal([]string{"daemonsets/cilium", operatorPatchKey, corednsPatchKey, uiPatchKey}))
	})

	It("starts no restart until the Nodes have stayed Ready for the settle window, asked live", func() {
		f, kc := restartFixture(ciliumNodes)
		op := f.newOp()

		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageFinalizing))
		caKubeletSettleDelay = time.Hour

		checks := f.nodeChecks

		for range 4 {
			op = f.step(op)
		}

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation)
		Expect(cond.Reason).To(Equal(reasonCARotationSettlingNodes), "a Ready read right after Dropping is not enough")
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress), "it waits, it does not pause")
		Expect(patchCount(kc)).To(BeEmpty())
		Expect(f.nodeChecks).To(BeNumerically(">", checks), "every reconcile reads the Nodes again")

		// A kubelet that goes down inside the window resets it, still without a pause or a restart.
		f.world.notReady = []string{"cp-restart-test"}
		op = f.step(op)

		cond = apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation)
		Expect(cond.Reason).To(Equal(reasonCARotationAwaitingNodes))
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))

		f.world.notReady = nil
		caKubeletSettleDelay = time.Nanosecond

		_, _ = f.runUntil(op, completed)
		Expect(patchCount(kc)).To(HaveKeyWithValue(corednsPatchKey, 1))
	})

	It("finishes the rotation, naming what is not back, once the wait is over, and does not look like a clean success", func() {
		f, _ := restartFixture(3)
		caClientRestartTimeout = time.Nanosecond
		op, _ := f.runUntil(f.newOp(), completed)

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation)
		Expect(cond.Reason).To(Equal(reasonCARotationClientsNotBack), "a distinct reason")
		Expect(cond.Message).To(And(ContainSubstring("not back after the restart"), ContainSubstring("cilium")))

		var cluster ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: f.cluster.Name}, &cluster)).To(Succeed())
		Expect(apimeta.IsStatusConditionTrue(cluster.Status.Conditions, ichorv1alpha1.ConditionCAClientsNotRestarted)).To(BeTrue())
		Expect(strings.Join(drainEvents(f.recorder), "\n")).To(And(
			ContainSubstring("Warning"), ContainSubstring(ichorv1alpha1.ConditionCAClientsNotRestarted)))
	})

	It("lowers the condition again after a rotation whose clients come back", func() {
		f, _ := restartFixture(ciliumNodes)
		op := f.newOp()

		var cluster ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: f.cluster.Name}, &cluster)).To(Succeed())
		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: ichorv1alpha1.ConditionCAClientsNotRestarted, Status: metav1.ConditionTrue, Reason: "CARotated", Message: testOldValue,
		})
		Expect(k8sClient.Status().Update(ctx, &cluster)).To(Succeed())

		op, _ = f.runUntil(op, completed)
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Reason).To(Equal(reasonCARotationComplete))
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: f.cluster.Name}, &cluster)).To(Succeed())
		Expect(apimeta.IsStatusConditionTrue(cluster.Status.Conditions, ichorv1alpha1.ConditionCAClientsNotRestarted)).To(BeFalse())
	})

	It("restarts no CNI workload on an unmanaged cluster, and the CNI first otherwise", func() {
		f, _ := restartFixture(ciliumNodes)
		cluster := &ichorv1alpha1.TalosCluster{}
		cluster.Spec.CNI.Preset = ichorv1alpha1.TalosClusterCNIPresetUnmanaged
		run := &caRun{r: f.newReconciler(), cluster: cluster, op: &ichorv1alpha1.TalosOperation{}}

		names := func() (out []string) {
			targets, err := run.restartTargets(ctx)
			Expect(err).NotTo(HaveOccurred())

			for _, t := range targets {
				out = append(out, t.name)
			}

			return out
		}

		Expect(names()).To(Equal([]string{kubeProxyName, corednsName, "ichor-ichor-ui"}))

		cluster.Spec.CNI.Preset = ciliumName
		Expect(names()).To(Equal([]string{ciliumName, ciliumOperatorName, kubeProxyName, corednsName, "ichor-ichor-ui"}))
	})

	It("ignores a CNI DaemonSet that is not there", func() {
		f, kc := restartFixture(-1)
		op := f.newOp()

		op, _ = f.runUntil(op, completed)
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		Expect(patchCount(kc)).To(Equal(map[string]int{corednsPatchKey: 1, uiPatchKey: 1}))
	})
})

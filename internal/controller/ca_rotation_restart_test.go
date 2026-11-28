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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	ciliumPatchKey   = "daemonsets/cilium"
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
			corednsPatchKey: 1, ciliumPatchKey: 1, operatorPatchKey: 1, uiPatchKey: 1,
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
		Expect(patchCount(kc)[ciliumPatchKey]).To(Equal(1), "waiting must not restart again")
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

	It("on an OnDelete cilium DaemonSet evicts the old agent pods, which are not Ready, one node at a time", func() {
		kc, op, replaced, maxGone := onDeleteRotation(0, ciliumNodes, true, nil)

		Expect(completed(op)).To(BeTrue())
		Expect(replaced).To(Equal(ciliumNodes), "every agent pod was replaced before the rotation completed")
		Expect(maxGone).To(Equal(1), "one node at a time")
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).NotTo(ContainSubstring("not back"))

		for _, a := range kc.Actions() {
			Expect(a.Matches("delete", "pods")).To(BeFalse(), "a pod is evicted, never deleted")
		}
	})

	It("CA rotation restarts every later client even after an earlier one ran out its wait", func() {
		advanceCAClock(10*time.Second, 40*time.Second)

		kc, op, _, _ := onDeleteRotation(0, ciliumNodes, false, caClockTick)

		Expect(completed(op)).To(BeTrue())
		Expect(patchCount(kc)).To(Equal(map[string]int{
			ciliumPatchKey: 1, operatorPatchKey: 1, "daemonsets/kube-proxy": 1, corednsPatchKey: 1, uiPatchKey: 1,
		}))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).To(
			And(ContainSubstring("not back after the restart"), ContainSubstring("cilium")))
	})

	It("does not time out an OnDelete DaemonSet that keeps replacing pods, however long the whole takes", func() {
		advanceCAClock(100*time.Second, 40*time.Second)

		_, op, replaced, _ := onDeleteRotation(0, 8, true, caClockTick)

		Expect(completed(op)).To(BeTrue())
		Expect(replaced).To(Equal(8))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).NotTo(ContainSubstring("not back"))
	})

	It("waits while a PodDisruptionBudget refuses the first eviction, then completes", func() {
		kc, op, replaced, _ := onDeleteRotation(2, ciliumNodes, true, nil)

		Expect(completed(op)).To(BeTrue())
		Expect(replaced).To(Equal(ciliumNodes))

		refused := 0

		for _, a := range kc.Actions() {
			if a.Matches("create", "pods") && a.GetSubresource() == evictionSubresource {
				refused++
			}
		}

		Expect(refused).To(BeNumerically(">", ciliumNodes), "the refused evictions were asked again")
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

		Expect(order).To(Equal([]string{ciliumPatchKey, operatorPatchKey, corednsPatchKey, uiPatchKey}))
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

var _ = Describe("TalosOperation one-shot cluster conditions", func() {
	// staleAndOther returns a copy of the cluster read before another writer raised other on the server.
	staleAndOther := func(f *caRotFixture, other string) *ichorv1alpha1.TalosCluster {
		key := types.NamespacedName{Namespace: tmNamespace, Name: f.cluster.Name}
		stale := &ichorv1alpha1.TalosCluster{}
		Expect(k8sClient.Get(ctx, key, stale)).To(Succeed())

		var live ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, key, &live)).To(Succeed())
		apimeta.SetStatusCondition(&live.Status.Conditions, metav1.Condition{
			Type: other, Status: metav1.ConditionTrue, Reason: "Other", Message: "raised by another writer",
		})
		Expect(k8sClient.Status().Update(ctx, &live)).To(Succeed())

		return stale
	}
	serverHas := func(f *caRotFixture, typ string) bool {
		var live ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: f.cluster.Name}, &live)).To(Succeed())

		return apimeta.IsStatusConditionTrue(live.Status.Conditions, typ)
	}

	It("setClientsNotRestarted keeps a condition another writer raised after the cluster was read", func() {
		f, _ := restartFixture(ciliumNodes)
		stale := staleAndOther(f, ichorv1alpha1.ConditionBundleBackupStale)
		run := &caRun{r: f.newReconciler(), cluster: stale, op: &ichorv1alpha1.TalosOperation{}}

		Expect(run.setClientsNotRestarted(ctx, true, "a client is not back")).To(Succeed())
		Expect(serverHas(f, ichorv1alpha1.ConditionCAClientsNotRestarted)).To(BeTrue())
		Expect(serverHas(f, ichorv1alpha1.ConditionBundleBackupStale)).To(BeTrue())
	})

	It("raiseBundleBackupStale keeps a condition another writer raised after the cluster was read", func() {
		f, _ := restartFixture(ciliumNodes)
		stale := staleAndOther(f, ichorv1alpha1.ConditionCAClientsNotRestarted)

		Expect(f.newReconciler().raiseBundleBackupStale(ctx, stale, "Stale", "export the bundle")).To(Succeed())
		Expect(serverHas(f, ichorv1alpha1.ConditionBundleBackupStale)).To(BeTrue())
		Expect(serverHas(f, ichorv1alpha1.ConditionCAClientsNotRestarted)).To(BeTrue())
	})
})

// onDeleteRotation runs a CA rotation over an OnDelete cilium DaemonSet whose old pods are not Ready (the old agents
// do not trust the new CA). The fake has no DaemonSet controller: an evicted pod is deleted, and recreated on the new
// template, Ready. The first refuse evictions answer 429, as a PodDisruptionBudget does.
func onDeleteRotation(refuse, nodes int, replacementReady bool, tick func()) (kc *fake.Clientset, op *ichorv1alpha1.TalosOperation, replaced, maxGone int) {
	f, kc := restartFixture(ciliumNodes)
	sel := map[string]string{"k8s-app": ciliumName}

	ds, err := kc.AppsV1().DaemonSets(kubeSystemNamespace).Get(ctx, ciliumName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	ds.Spec.UpdateStrategy.Type = appsv1.OnDeleteDaemonSetStrategyType
	ds.Spec.Selector = &metav1.LabelSelector{MatchLabels: sel}
	ds.Status.DesiredNumberScheduled, ds.Status.NumberAvailable = int32(nodes), int32(nodes)
	ds.Status.UpdatedNumberScheduled = 0 // what the template patch leaves: no pod matches it
	_, err = kc.AppsV1().DaemonSets(kubeSystemNamespace).Update(ctx, ds, metav1.UpdateOptions{})
	Expect(err).NotTo(HaveOccurred())

	_, err = kc.AppsV1().DaemonSets(kubeSystemNamespace).Create(ctx, &appsv1.DaemonSet{
		Namespace: kubeSystemNamespace, Name: kubeProxyName, Generation: 1, Status: appsv1.DaemonSetStatus{ObservedGeneration: 1},
	}, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())

	newPod := func(name, token string) *corev1.Pod {
		ready := corev1.ConditionFalse
		if token != "" && replacementReady {
			ready = corev1.ConditionTrue
		}

		p := &corev1.Pod{Namespace: kubeSystemNamespace, Name: name, Labels: sel,
			Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: ready}}}}
		if token != "" {
			p.Annotations = map[string]string{caRestartMarker: token}
		}

		return p
	}

	for i := range nodes {
		_, err = kc.CoreV1().Pods(kubeSystemNamespace).Create(ctx, newPod(fmt.Sprintf("cilium-%d", i), ""), metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
	}

	kc.Resources = []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{
		{Name: "pods/eviction", Kind: "Eviction", Group: "policy", Version: "v1"},
	}}}
	kc.PrependReactor("create", "pods", func(a clienttesting.Action) (bool, runtime.Object, error) {
		ev, ok := a.(clienttesting.CreateAction).GetObject().(*policyv1.Eviction)
		if a.GetSubresource() != evictionSubresource || !ok {
			return false, nil, nil
		}

		if refuse > 0 {
			refuse--

			return true, nil, apierrors.NewTooManyRequests("disruption budget", 1)
		}

		return true, nil, kc.Tracker().Delete(schema.GroupVersionResource{Version: "v1", Resource: "pods"}, ev.Namespace, ev.Name)
	})

	op = f.newOp()

	for range 200 {
		op = f.step(op)

		pods, lerr := kc.CoreV1().Pods(kubeSystemNamespace).List(ctx, metav1.ListOptions{})
		Expect(lerr).NotTo(HaveOccurred())
		maxGone = max(maxGone, nodes-len(pods.Items))

		for i := len(pods.Items); i < nodes; i++ {
			_, err = kc.CoreV1().Pods(kubeSystemNamespace).Create(ctx, newPod(fmt.Sprintf("cilium-new-%d", replaced), string(op.UID)), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			replaced++

			d, _ := kc.AppsV1().DaemonSets(kubeSystemNamespace).Get(ctx, ciliumName, metav1.GetOptions{})
			d.Status.UpdatedNumberScheduled = int32(replaced)
			_, err = kc.AppsV1().DaemonSets(kubeSystemNamespace).UpdateStatus(ctx, d, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())
		}

		if tick != nil {
			tick()
		}

		if completed(op) {
			break
		}
	}

	return kc, op, replaced, maxGone
}

var caFakeNow time.Time

// advanceCAClock gives the restart waits a fake clock and a timeout; caClockTick moves it by step.
func advanceCAClock(timeout, step time.Duration) {
	oldTimeout, oldNow := caClientRestartTimeout, caClientNow
	caFakeNow, caClientRestartTimeout, caClockStep = time.Now(), timeout, step
	caClientNow = func() time.Time { return caFakeNow }

	DeferCleanup(func() { caClientRestartTimeout, caClientNow = oldTimeout, oldNow })
}

var caClockStep time.Duration

func caClockTick() { caFakeNow = caFakeNow.Add(caClockStep) }

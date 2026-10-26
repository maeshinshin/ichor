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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

const (
	// testK8sOld and testK8sTarget are one minor apart (KubernetesSkew allows it);
	// testK8sTooFar is two minors past testK8sOld.
	testK8sOld    = "v1.35.4"
	testK8sTarget = "v1.36.0"
	testK8sTooFar = "v1.37.0"

	testKubeSystem = "kube-system"
)

// fakeKubernetesAdmin is the KubernetesAdmin test double: it records calls and returns
// scripted results.
type fakeKubernetesAdmin struct {
	syncCalls   int
	syncErr     error
	removed     []string
	removedErr  error
	removedFor  []string
	removedCall int
}

func (f *fakeKubernetesAdmin) SyncBootstrapManifests(context.Context, TalosClient) error {
	f.syncCalls++

	return f.syncErr
}

func (f *fakeKubernetesAdmin) RemovedAPIsInUse(_ context.Context, _ TalosClient, targetMinor string) ([]string, error) {
	f.removedCall++
	f.removedFor = append(f.removedFor, targetMinor)

	return f.removed, f.removedErr
}

// newK8sNode creates a Node reporting kubeletVersion and readiness. Names are unique per test
// (Nodes are cluster-scoped and the envtest API server is shared by the whole suite).
func newK8sNode(name, kubeletVersion string, ready bool) {
	n := &corev1.Node{}
	n.Name = name
	Expect(k8sClient.Create(ctx, n)).To(Succeed())

	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}

	n.Status.NodeInfo.KubeletVersion = kubeletVersion
	n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}
	Expect(k8sClient.Status().Update(ctx, n)).To(Succeed())
}

func setK8sNode(name, kubeletVersion string, ready bool) {
	var n corev1.Node
	Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name}, &n)).To(Succeed())

	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}

	n.Status.NodeInfo.KubeletVersion = kubeletVersion
	n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}
	Expect(k8sClient.Status().Update(ctx, &n)).To(Succeed())
}

// newStaticPod creates the mirror pod of a control-plane component on node, running version.
// The pods are removed again when the spec ends: the advancer lists every apiserver pod in the
// cluster to decide whether all control planes are done, so a leaked one would leak across tests.
func newStaticPod(component, node, version string, ready bool) {
	p := &corev1.Pod{}
	p.Namespace = testKubeSystem
	p.Name = component + "-" + node
	p.Labels = map[string]string{"k8s-app": component}
	p.Spec.NodeName = node
	p.Spec.Containers = []corev1.Container{{Name: component, Image: "registry.k8s.io/" + component + ":" + version}}
	Expect(k8sClient.Create(ctx, p)).To(Succeed())
	DeferCleanup(func() { _ = k8sClient.Delete(ctx, p) })

	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}

	p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: status}}
	Expect(k8sClient.Status().Update(ctx, p)).To(Succeed())
}

func setStaticPodsFor(node, version string) {
	for _, c := range []string{"kube-apiserver", "kube-controller-manager", "kube-scheduler"} {
		newStaticPod(c, node, version, true)
	}
}

// newKubeProxyDaemonSet creates kube-system/kube-proxy with the given readiness and removes it
// when the spec ends.
func newKubeProxyDaemonSet(version string, desired, ready int32) {
	ds := &appsv1.DaemonSet{}
	ds.Namespace = testKubeSystem
	ds.Name = "kube-proxy"
	ds.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-proxy"}}
	ds.Spec.Template.Labels = map[string]string{"k8s-app": "kube-proxy"}
	ds.Spec.Template.Spec.Containers = []corev1.Container{{Name: "kube-proxy", Image: "registry.k8s.io/kube-proxy:" + version}}
	Expect(k8sClient.Create(ctx, ds)).To(Succeed())
	DeferCleanup(func() { _ = k8sClient.Delete(ctx, ds) })

	ds.Status.DesiredNumberScheduled = desired
	ds.Status.UpdatedNumberScheduled = ready
	ds.Status.NumberReady = ready
	Expect(k8sClient.Status().Update(ctx, ds)).To(Succeed())
}

func newKubernetesAdvancer(fake *fakeTalosClient, admin KubernetesAdmin) *kubernetesUpgradeAdvancer {
	return &kubernetesUpgradeAdvancer{
		Client: k8sClient, Reader: k8sClient, ControllerNamespace: tmNamespace,
		TalosClient: clientFactory(fake), Admin: admin,
	}
}

// k8sCluster creates a cluster whose spec.kubernetesVersion is target.
func k8sCluster(target string) *ichorv1alpha1.TalosCluster {
	secret := newTestSecret(newTestBundle("v1.14"))
	cluster := newTestCluster(secret.Name, "v1.14")
	cluster.Spec.KubernetesVersion = target
	Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

	return cluster
}

// renderAt renders m as cluster would be rendered if it ran Kubernetes version.
func renderAt(cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, version string) []byte {
	c := cluster.DeepCopy()
	c.Spec.KubernetesVersion = version

	bundle, err := loadBundle(ctx, k8sClient, tmNamespace, c)
	Expect(err).NotTo(HaveOccurred())

	rendered, _, _, err := (&TalosMachineReconciler{Client: k8sClient, ControllerNamespace: tmNamespace}).
		renderMachine(ctx, c, m, bundle, nil)
	Expect(err).NotTo(HaveOccurred())

	return rendered
}

func newK8sWorker(clusterName, suffix string) *ichorv1alpha1.TalosMachine {
	m := newOpTestMachine(clusterName, roleWorker, "k8s-"+suffix)
	setHostname(m, "node-k8s-"+suffix)

	return fetchMachine(m)
}

func newK8sControlPlane(clusterName, suffix string) *ichorv1alpha1.TalosMachine {
	m := newOpTestMachine(clusterName, roleControlPlane, "k8s-"+suffix)
	setHostname(m, "node-k8s-"+suffix)

	return fetchMachine(m)
}

var _ = Describe("kubernetesUpgradeAdvancer", func() {
	It("refuses a skipped minor without touching the node", func() {
		cluster := k8sCluster(testK8sTooFar)
		w := newK8sWorker(cluster.Name, "skip1")
		newK8sNode(w.Spec.Hostname, testK8sOld, true)

		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, w, testK8sOld)}
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		done, err := adv.Advance(ctx, cluster, w, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("skips a minor version"))
		Expect(done).To(BeFalse())
		Expect(fake.applyCalls).To(BeEmpty())
		Expect(fake.pulled).To(BeEmpty())
	})

	It("refuses a Kubernetes version the node's running Talos does not support", func() {
		cluster := k8sCluster(testK8sTarget)
		w := newK8sWorker(cluster.Name, "pair1")
		newK8sNode(w.Spec.Hostname, testK8sOld, true)

		// Talos 1.9 does not support Kubernetes 1.36.
		fake := &fakeTalosClient{version: "v1.9.0", current: renderAt(cluster, w, testK8sOld)}
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		_, err := adv.Advance(ctx, cluster, w, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not supported with talos"))
		Expect(fake.applyCalls).To(BeEmpty())
	})

	It("blocks while the CNI hand-over is unfinished, and proceeds once it is complete", func() {
		cluster := k8sCluster(testK8sTarget)
		cluster.Spec.CNI.Preset = "cilium"
		cluster.Spec.CNI.Handover = "automatic"
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: render.ConditionCNIHandover, Status: metav1.ConditionFalse,
			Reason: reasonHandoverWaitingForSync, Message: "Argo CD reports cilium sync=OutOfSync",
		})
		Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

		w := newK8sWorker(cluster.Name, "ho1")
		newK8sNode(w.Spec.Hostname, testK8sOld, true)

		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, w, testK8sOld)}
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		done, err := adv.Advance(ctx, cluster, w, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("hand-over"))
		Expect(done).To(BeFalse())
		Expect(fake.applyCalls).To(BeEmpty(), "a stale inline CNI manifest would be re-applied by the manifest sync")

		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: render.ConditionCNIHandover, Status: metav1.ConditionTrue, Reason: reasonHandoverComplete, Message: "done",
		})
		Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

		// The handed-over render no longer carries the inline manifest, so the node's current
		// config is re-rendered the same way.
		fake.current = renderAt(cluster, w, testK8sOld)

		_, err = adv.Advance(ctx, cluster, w, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.applyCalls).To(HaveLen(1))
	})

	It("lets a cluster whose hand-over is disabled through: re-applying its inline manifest is the intended state", func() {
		cluster := k8sCluster(testK8sTarget)
		cluster.Spec.CNI.Preset = "cilium"
		cluster.Spec.CNI.Handover = "disabled"
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: render.ConditionCNIHandover, Status: metav1.ConditionFalse, Reason: reasonHandoverDisabled, Message: "disabled",
		})
		Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

		w := newK8sWorker(cluster.Name, "ho2")
		newK8sNode(w.Spec.Hostname, testK8sOld, true)

		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, w, testK8sOld)}
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		_, err := adv.Advance(ctx, cluster, w, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.applyCalls).To(HaveLen(1))
	})

	It("refuses to start on a node that is not Ready", func() {
		cluster := k8sCluster(testK8sTarget)
		w := newK8sWorker(cluster.Name, "nr1")
		newK8sNode(w.Spec.Hostname, testK8sOld, false)

		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, w, testK8sOld)}
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		_, err := adv.Advance(ctx, cluster, w, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not Ready"))
		Expect(fake.applyCalls).To(BeEmpty())
	})

	It("refuses when the node also has an unrelated change that would need a reboot", func() {
		cluster := k8sCluster(testK8sTarget)
		w := newK8sWorker(cluster.Name, "unrel1")
		newK8sNode(w.Spec.Hostname, testK8sOld, true)

		current := append(renderAt(cluster, w, testK8sOld),
			[]byte("---\napiVersion: v1alpha1\nkind: VolumeConfig\nname: EPHEMERAL\nprovisioning:\n  minSize: 2GiB\n")...)
		fake := &fakeTalosClient{version: testTalosVersion, current: current}
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		_, err := adv.Advance(ctx, cluster, w, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unrelated"))
		Expect(fake.applyCalls).To(BeEmpty())
	})

	It("warns about removed APIs in use on the operation but still proceeds", func() {
		cluster := k8sCluster(testK8sTarget)
		w := newK8sWorker(cluster.Name, "warn1")
		newK8sNode(w.Spec.Hostname, testK8sOld, true)

		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, w, testK8sOld)}
		admin := &fakeKubernetesAdmin{removed: []string{"flowcontrol.apiserver.k8s.io/v1beta3 flowschemas"}}
		adv := newKubernetesAdvancer(fake, admin)

		op := &ichorv1alpha1.TalosOperation{}

		_, err := adv.Advance(ctx, cluster, w, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(admin.removedFor).To(ConsistOf("1.36"))
		Expect(fake.applyCalls).To(HaveLen(1))

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionRemovedAPIsInUse)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Message).To(ContainSubstring("flowschemas"))
	})

	It("applies a worker's kubelet image once, pre-pulls it, and finishes when the node runs the target", func() {
		cluster := k8sCluster(testK8sTarget)
		w := newK8sWorker(cluster.Name, "w1")
		newK8sNode(w.Spec.Hostname, testK8sOld, true)

		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, w, testK8sOld)}
		admin := &fakeKubernetesAdmin{}
		adv := newKubernetesAdvancer(fake, admin)

		done, err := adv.Advance(ctx, cluster, w, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(fake.applyCalls[0]).To(Equal(renderAt(cluster, w, testK8sTarget)))
		Expect(fake.pulled).To(ConsistOf(ContainSubstring(":" + testK8sTarget)))
		Expect(apimeta.IsStatusConditionTrue(fetchMachine(w).Status.Conditions, conditionKubernetesUpgrade)).To(BeTrue())

		// The node has the new config but the kubelet has not restarted yet: resume from the
		// observed state, never apply again.
		fake.current = fake.applyCalls[0]

		done, err = adv.Advance(ctx, cluster, fetchMachine(w), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.applyCalls).To(HaveLen(1))

		setK8sNode(w.Spec.Hostname, testK8sTarget, true)

		done, err = adv.Advance(ctx, cluster, fetchMachine(w), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(admin.syncCalls).To(BeZero(), "bootstrap manifests are synced when the control planes finish, not per worker")
		Expect(apimeta.IsStatusConditionTrue(fetchMachine(w).Status.Conditions, conditionKubernetesUpgrade)).To(BeFalse())
	})

	It("pauses (errors) when the node never converges within the verification timeout", func() {
		cluster := k8sCluster(testK8sTarget)
		w := newK8sWorker(cluster.Name, "to1")
		newK8sNode(w.Spec.Hostname, testK8sOld, true)

		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, w, testK8sOld)}
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		_, err := adv.Advance(ctx, cluster, w, nil)
		Expect(err).NotTo(HaveOccurred())

		fake.current = fake.applyCalls[0]

		got := fetchMachine(w)
		cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionKubernetesUpgrade)
		Expect(cond).NotTo(BeNil())
		cond.LastTransitionTime = metav1.NewTime(time.Now().Add(-2 * kubernetesVerifyTimeout))
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		done, err := adv.Advance(ctx, cluster, fetchMachine(w), nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("did not converge"))
		Expect(done).To(BeFalse())
	})

	It("verifies a control plane's static pods, then syncs bootstrap manifests only once every control plane is done", func() {
		cluster := k8sCluster(testK8sTarget)
		cp1 := newK8sControlPlane(cluster.Name, "cp1")
		cp2 := newK8sControlPlane(cluster.Name, "cp2")
		newK8sNode(cp1.Spec.Hostname, testK8sOld, true)
		newK8sNode(cp2.Spec.Hostname, testK8sOld, true)
		setStaticPodsFor(cp1.Spec.Hostname, testK8sOld)
		setStaticPodsFor(cp2.Spec.Hostname, testK8sOld)
		newKubeProxyDaemonSet(testK8sOld, 2, 2)

		fake1 := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, cp1, testK8sOld)}
		admin := &fakeKubernetesAdmin{}
		adv := newKubernetesAdvancer(fake1, admin)

		done, err := adv.Advance(ctx, cluster, cp1, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake1.applyCalls).To(HaveLen(1))
		Expect(fake1.pulled).To(HaveLen(4), "the apiserver, controller-manager, scheduler and kubelet images are pre-pulled")

		fake1.current = fake1.applyCalls[0]

		// The node's kubelet is on the target, but its static pods have not been replaced yet.
		setK8sNode(cp1.Spec.Hostname, testK8sTarget, true)

		done, err = adv.Advance(ctx, cluster, fetchMachine(cp1), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse(), "the static pods still run the old version")

		for _, c := range []string{"kube-apiserver", "kube-controller-manager", "kube-scheduler"} {
			var p corev1.Pod
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: c + "-" + cp1.Spec.Hostname}, &p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &p)).To(Succeed())
			newStaticPod(c, cp1.Spec.Hostname, testK8sTarget, true)
		}

		done, err = adv.Advance(ctx, cluster, fetchMachine(cp1), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(admin.syncCalls).To(BeZero(), "cp2 still runs the old apiserver: the manifest sync must wait for it")
	})

	It("syncs the bootstrap manifests and waits for kube-proxy once the last control plane is done", func() {
		cluster := k8sCluster(testK8sTarget)
		cp := newK8sControlPlane(cluster.Name, "cpL")
		newK8sNode(cp.Spec.Hostname, testK8sTarget, true)
		setStaticPodsFor(cp.Spec.Hostname, testK8sTarget)
		newKubeProxyDaemonSet(testK8sTarget, 3, 1)

		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, cp, testK8sTarget)}
		admin := &fakeKubernetesAdmin{}
		adv := newKubernetesAdvancer(fake, admin)

		done, err := adv.Advance(ctx, cluster, cp, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse(), "kube-proxy has not finished rolling out")
		Expect(admin.syncCalls).To(Equal(1))
		Expect(fake.applyCalls).To(BeEmpty(), "nothing to apply: the node already has the rendered config")

		var ds appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: "kube-proxy"}, &ds)).To(Succeed())
		ds.Status.NumberReady = 3
		ds.Status.UpdatedNumberScheduled = 3
		Expect(k8sClient.Status().Update(ctx, &ds)).To(Succeed())

		done, err = adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
	})

	It("skips kube-proxy entirely when the CNI replaces it", func() {
		cluster := k8sCluster(testK8sTarget)
		cluster.Spec.CNI.Preset = "cilium"
		cluster.Spec.CNI.KubeProxy = "replaced"
		cluster.Spec.CNI.Handover = "disabled"
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		cp := newK8sControlPlane(cluster.Name, "cpR")
		newK8sNode(cp.Spec.Hostname, testK8sTarget, true)
		setStaticPodsFor(cp.Spec.Hostname, testK8sTarget)

		// No kube-proxy DaemonSet exists at all.
		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, cp, testK8sTarget)}
		admin := &fakeKubernetesAdmin{}
		adv := newKubernetesAdvancer(fake, admin)

		done, err := adv.Advance(ctx, cluster, cp, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(admin.syncCalls).To(Equal(1))
	})
})

var _ = Describe("kind: kubernetes through TalosOperationReconciler", func() {
	It("advances control planes one at a time, and a failed step pauses the operation", func() {
		cluster := k8sCluster(testK8sTarget)
		cp1 := newK8sControlPlane(cluster.Name, "op1")
		cp2 := newK8sControlPlane(cluster.Name, "op2")
		cp3 := newK8sControlPlane(cluster.Name, "op3")

		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2, cp3} {
			setUpgradeOrder(cp, cp.Name)
			newK8sNode(cp.Spec.Hostname, testK8sOld, true)
		}

		byName := map[string]*fakeTalosClient{}
		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2, cp3} {
			byName[cp.Name] = &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, cp, testK8sOld)}
		}

		adv := &kubernetesUpgradeAdvancer{
			Client: k8sClient, Reader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: perMachineClientFactory(byName), Admin: &fakeKubernetesAdmin{},
		}

		op := newOpTestOperation(cluster.Name)
		op.Spec.Kind = ichorv1alpha1.TalosOperationKindKubernetes
		Expect(k8sClient.Update(ctx, op)).To(Succeed())

		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: adv.Advance}

		_, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())

		applied := 0

		for _, f := range byName {
			applied += len(f.applyCalls)
		}

		Expect(applied).To(Equal(1), "exactly one control plane is switched per pass")

		got := fetchOperation(op)
		Expect(got.Status.Machines).To(HaveLen(1))

		// The first control plane's node now reports a kubelet that fell back to the old version
		// while its config was rewritten: make the next pass fail the skew check by pointing the
		// target two minors past it.
		cluster.Spec.KubernetesVersion = testK8sTooFar
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		first := got.Status.Machines[0].Name
		byName[first].current = byName[first].applyCalls[0]
		var firstMachine ichorv1alpha1.TalosMachine
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: tmNamespace, Name: first}, &firstMachine)).To(Succeed())
		setK8sNode(firstMachine.Spec.Hostname, testK8sOld, true)

		_, err = reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())

		got = fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).NotTo(BeEmpty(), fmt.Sprintf("status: %+v", got.Status))
		Expect(got.Status.Machines).To(HaveLen(1), "the other control planes were never started")
	})
})

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
	"regexp"
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
	"github.com/maeshinshin/ichor/pkg/storage"
)

const (
	// testK8sOld and testK8sTarget are one minor apart (KubernetesSkew allows it);
	// testK8sTooFar is two minors past testK8sOld.
	testK8sOld    = "v1.35.4"
	testK8sTarget = "v1.36.0"
	testK8sTooFar = "v1.37.0"

	testKubeSystem = "kube-system"

	compKubelet           = "kubelet"
	compAPIServer         = "kube-apiserver"
	compControllerManager = "kube-controller-manager"
	compScheduler         = "kube-scheduler"

	testK8sContract = "v1.14"
	testCilium      = "cilium"
)

// fakeKubernetesAdmin is the KubernetesAdmin test double: it records calls and returns
// scripted results.
type fakeKubernetesAdmin struct {
	syncCalls   int
	syncErr     error
	removed     []string
	removedErr  error
	removedFor  []string
	removedFrom []TalosClient
	removedCall int
}

func (f *fakeKubernetesAdmin) SyncBootstrapManifests(context.Context, TalosClient) error {
	f.syncCalls++

	return f.syncErr
}

func (f *fakeKubernetesAdmin) RemovedAPIsInUse(_ context.Context, talos TalosClient, targetMinor string) ([]string, error) {
	f.removedCall++
	f.removedFor = append(f.removedFor, targetMinor)
	f.removedFrom = append(f.removedFrom, talos)

	return f.removed, f.removedErr
}

// newK8sNode creates a Node whose kubelet runs testK8sOld, with the given readiness. Names are
// unique per test (Nodes are cluster-scoped and the envtest API server is shared by the whole suite).
func newK8sNode(name string, ready bool) {
	n := &corev1.Node{}
	n.Name = name
	Expect(k8sClient.Create(ctx, n)).To(Succeed())

	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}

	n.Status.NodeInfo.KubeletVersion = testK8sOld
	n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}
	Expect(k8sClient.Status().Update(ctx, n)).To(Succeed())
}

// bareNode is a Node object for a fake clientset.
func bareNode(name string) *corev1.Node {
	n := &corev1.Node{}
	n.Name = name

	return n
}

// setK8sNode makes name's kubelet report kubeletVersion, Ready.
func setK8sNode(name, kubeletVersion string) {
	var n corev1.Node
	Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name}, &n)).To(Succeed())

	n.Status.NodeInfo.KubeletVersion = kubeletVersion
	n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}
	Expect(k8sClient.Status().Update(ctx, &n)).To(Succeed())
}

// newStaticPod creates the mirror pod of a control-plane component on node, running version.
// The pods are removed again when the spec ends: the advancer lists every apiserver pod in the
// cluster to decide whether all control planes are done, so a leaked one would leak across tests.
func newStaticPod(component, node, version string, ready bool) {
	p := &corev1.Pod{}
	p.Namespace = testKubeSystem
	p.Name = component + "-" + node
	p.Labels = map[string]string{labelK8sApp: component}
	p.Spec.NodeName = node
	p.Spec.Containers = []corev1.Container{{Name: component, Image: "registry.k8s.io/" + component + ":" + version}}
	Expect(k8sClient.Create(ctx, p)).To(Succeed())
	// Grace period 0: there is no kubelet in envtest to confirm a graceful delete.
	DeferCleanup(func() { _ = k8sClient.Delete(ctx, p, client.GracePeriodSeconds(0)) })

	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}

	p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: status}}
	Expect(k8sClient.Status().Update(ctx, p)).To(Succeed())
}

func setStaticPodsFor(node, version string) {
	for _, c := range staticPodComponents {
		newStaticPod(c, node, version, true)
	}
}

// newKubeProxyDaemonSet creates kube-system/kube-proxy with the given readiness and removes it
// when the spec ends.
func newKubeProxyDaemonSet(version string, desired, ready int32) {
	ds := &appsv1.DaemonSet{}
	ds.Namespace = testKubeSystem
	ds.Name = kubeProxyName
	ds.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{labelK8sApp: kubeProxyName}}
	ds.Spec.Template.Labels = map[string]string{labelK8sApp: kubeProxyName}
	ds.Spec.Template.Spec.Containers = []corev1.Container{{Name: kubeProxyName, Image: "registry.k8s.io/kube-proxy:" + version}}
	Expect(k8sClient.Create(ctx, ds)).To(Succeed())
	DeferCleanup(func() {
		_ = k8sClient.Delete(ctx, ds, client.GracePeriodSeconds(0), client.PropagationPolicy(metav1.DeletePropagationBackground))
	})

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

// advanceOK runs one advance of a machine and fails the test when it errors.
func advanceOK(advance AdvanceMachineFunc, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, op *ichorv1alpha1.TalosOperation) bool {
	GinkgoHelper()

	done, err := advance(ctx, cluster, m, op)
	Expect(err).NotTo(HaveOccurred())

	return done
}

// k8sCluster creates a cluster whose spec.kubernetesVersion is target.
func k8sCluster(target string, cniSpec ...ichorv1alpha1.TalosClusterCNI) *ichorv1alpha1.TalosCluster {
	secret := newTestSecret(newTestBundle(testK8sContract))

	cluster := &ichorv1alpha1.TalosCluster{
		GenerateName: testClusterGenerate, Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosClusterSpec{
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: testEndpointType, Address: testClusterAddress},
			TalosVersion:      testTalosVersion,
			KubernetesVersion: target,
			VersionContract:   testK8sContract,
			SecretsRef:        corev1.LocalObjectReference{Name: secret.Name},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: testSchedulable},
		},
	}
	// Cilium is the only CNI. The cluster starts handed over so the hand-over guard
	// does not hold the upgrade; the test of the guard itself sets the condition to False.
	cluster.Spec.CNI = ichorv1alpha1.TalosClusterCNI{Preset: testCilium, KubeProxy: modeEnabled}
	if len(cniSpec) > 0 {
		cluster.Spec.CNI = cniSpec[0]
	}

	Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

	apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type: render.ConditionCNIHandover, Status: metav1.ConditionTrue, Reason: reasonHandoverComplete, Message: "handed over",
	})
	Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

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

// newStaticPodImage moves node's one static pod component to version, as the kubelet does once
// Talos has rewritten its manifest.
func newStaticPodImage(node, component, version string) {
	var p corev1.Pod
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: component + "-" + node}, &p)).To(Succeed())
	p.Spec.Containers[0].Image = "registry.k8s.io/" + component + ":" + version
	Expect(k8sClient.Update(ctx, &p)).To(Succeed())
}

// setStaticPodImages moves node's three static pods to version.
func setStaticPodImages(node, version string) {
	for _, c := range staticPodComponents {
		newStaticPodImage(node, c, version)
	}
}

// setStaticPodImagesFromConfig moves node's static pods to the versions the applied configuration
// names, component by component.
func setStaticPodImagesFromConfig(node string, cfg []byte) {
	tags := imageTags(cfg)

	for _, c := range staticPodComponents {
		newStaticPodImage(node, c, tags[c])
	}
}

// renderStaged renders m as the executor renders it in a stage that pins the kubelet and kube-proxy.
func renderStaged(cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, staged *render.StagedKubernetes) []byte {
	bundle, err := loadBundle(ctx, k8sClient, tmNamespace, cluster)
	Expect(err).NotTo(HaveOccurred())

	sys, err := render.ResolveSystemInputs(ctx, k8sClient, tmNamespace, cluster)
	Expect(err).NotTo(HaveOccurred())

	sys.Staged = staged

	rendered, _, err := render.Render(cluster, m, bundle, nil, sys)
	Expect(err).NotTo(HaveOccurred())

	return rendered
}

// opAtStage is an operation whose recorded stage is stage.
func opAtStage(stage string) *ichorv1alpha1.TalosOperation {
	op := &ichorv1alpha1.TalosOperation{}
	op.Status.Kubernetes = &ichorv1alpha1.TalosOperationKubernetesStatus{Stage: stage}

	return op
}

var componentImage = regexp.MustCompile(`image:\s*\S*/(kube-apiserver|kube-controller-manager|kube-scheduler|kubelet|kube-proxy):(v[0-9.]+)`)

// imageTags maps each Kubernetes component in a rendered configuration to its image tag.
func imageTags(cfg []byte) map[string]string {
	tags := map[string]string{}

	for _, m := range componentImage.FindAllStringSubmatch(string(cfg), -1) {
		tags[m[1]] = m[2]
	}

	return tags
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

// k8sWorkerFixture is a cluster targeting target, one worker of it whose Node (Ready or not) runs
// testK8sOld, and the fake Talos client of that worker, which holds the old configuration.
func k8sWorkerFixture(target, suffix string, ready bool) (*ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *fakeTalosClient) {
	cluster := k8sCluster(target)
	w := newK8sWorker(cluster.Name, suffix)
	newK8sNode(w.Spec.Hostname, ready)

	return cluster, w, &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, w, testK8sOld)}
}

var _ = Describe("kubernetesUpgradeAdvancer", func() {
	It("refuses a skipped minor without touching the node", func() {
		cluster, w, fake := k8sWorkerFixture(testK8sTooFar, "skip1", true)
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		done, err := adv.Advance(ctx, cluster, w, opAtStage(ichorv1alpha1.KubernetesStageKubelets))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("skips a minor version"))
		Expect(done).To(BeFalse())
		Expect(fake.applyCalls).To(BeEmpty())
		Expect(fake.pulled).To(BeEmpty())
	})

	It("refuses a Kubernetes version the node's running Talos does not support", func() {
		cluster := k8sCluster(testK8sTarget)
		w := newK8sWorker(cluster.Name, "pair1")
		newK8sNode(w.Spec.Hostname, true)

		// Talos 1.9 does not support Kubernetes 1.36.
		fake := &fakeTalosClient{version: "v1.9.0", current: renderAt(cluster, w, testK8sOld)}
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		_, err := adv.Advance(ctx, cluster, w, opAtStage(ichorv1alpha1.KubernetesStageKubelets))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not supported with talos"))
		Expect(fake.applyCalls).To(BeEmpty())
	})

	It("blocks while the CNI hand-over is unfinished, and proceeds once it is complete", func() {
		cluster := k8sCluster(testK8sTarget, ichorv1alpha1.TalosClusterCNI{Preset: testCilium, KubeProxy: "enabled"})

		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: render.ConditionCNIHandover, Status: metav1.ConditionFalse,
			Reason: reasonHandoverWaitingForCilium, Message: "the cilium DaemonSet has 0 of 1 pods ready",
		})
		Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

		w := newK8sWorker(cluster.Name, "ho1")
		newK8sNode(w.Spec.Hostname, true)

		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, w, testK8sOld)}
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		done, err := adv.Advance(ctx, cluster, w, opAtStage(ichorv1alpha1.KubernetesStageKubelets))
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

		advanceOK(adv.Advance, cluster, w, opAtStage(ichorv1alpha1.KubernetesStageKubelets))
		Expect(fake.applyCalls).To(HaveLen(1))
	})

	It("gives the apply its own long deadline, not what is left of the 15s dial bound", func() {
		cluster, w, fake := k8sWorkerFixture(testK8sTarget, "budget1", true)
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		advanceOK(adv.Advance, cluster, w, opAtStage(ichorv1alpha1.KubernetesStageKubelets))
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(fake.applyBudget).To(BeNumerically(">", 4*etcdDialTimeout),
			"Talos restarts static pods during the apply; the call must outlive the %s dial bound", etcdDialTimeout)
		Expect(fake.dryRunBudget).To(BeNumerically(">", 0))
	})

	It("refuses to start on a node that is not Ready", func() {
		cluster, w, fake := k8sWorkerFixture(testK8sTarget, "nr1", false)
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		_, err := adv.Advance(ctx, cluster, w, opAtStage(ichorv1alpha1.KubernetesStageKubelets))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not Ready"))
		Expect(fake.applyCalls).To(BeEmpty())
	})

	It("refuses when the node also has an unrelated change that would need a reboot", func() {
		cluster := k8sCluster(testK8sTarget)
		w := newK8sWorker(cluster.Name, "unrel1")
		newK8sNode(w.Spec.Hostname, true)

		current := append(renderAt(cluster, w, testK8sOld),
			[]byte("---\napiVersion: v1alpha1\nkind: VolumeConfig\nname: EPHEMERAL\nprovisioning:\n  minSize: 2GiB\n")...)
		fake := &fakeTalosClient{version: testTalosVersion, current: current}
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		_, err := adv.Advance(ctx, cluster, w, opAtStage(ichorv1alpha1.KubernetesStageKubelets))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unrelated"))
		Expect(fake.applyCalls).To(BeEmpty())
	})

	It("warns about removed APIs in use on the operation but still proceeds", func() {
		cluster := k8sCluster(testK8sTarget)
		w := newK8sWorker(cluster.Name, "warn1")
		newK8sNode(w.Spec.Hostname, true)
		newK8sControlPlane(cluster.Name, "warn1cp")

		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, w, testK8sOld)}
		admin := &fakeKubernetesAdmin{removed: []string{"flowcontrol.apiserver.k8s.io/v1beta3 flowschemas"}}
		adv := newKubernetesAdvancer(fake, admin)

		op := opAtStage(ichorv1alpha1.KubernetesStageKubelets)

		advanceOK(adv.Advance, cluster, w, op)
		Expect(admin.removedFor).To(ConsistOf("1.36"))
		Expect(fake.applyCalls).To(HaveLen(1))

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionRemovedAPIsInUse)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Message).To(ContainSubstring("flowschemas"))
	})

	It("asks every control plane, never the worker being upgraded, for removed APIs", func() {
		cluster := k8sCluster(testK8sTarget)
		w := newK8sWorker(cluster.Name, "rmcp-w")
		newK8sNode(w.Spec.Hostname, true)
		cp1 := newK8sControlPlane(cluster.Name, "rmcp-cp1")
		cp2 := newK8sControlPlane(cluster.Name, "rmcp-cp2")

		// A worker's Talos API refuses the admin kubeconfig ("only available on control plane
		// nodes"), so a fake that shares the worker's client would hide the bug.
		workerFake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, w, testK8sOld)}
		cp1Fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, cp1, testK8sOld)}
		cp2Fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, cp2, testK8sOld)}
		byName := map[string]*fakeTalosClient{w.Name: workerFake, cp1.Name: cp1Fake, cp2.Name: cp2Fake}
		admin := &fakeKubernetesAdmin{removed: []string{"flowcontrol.apiserver.k8s.io/v1beta3 flowschemas"}}
		adv := &kubernetesUpgradeAdvancer{
			Client: k8sClient, Reader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: perMachineClientFactory(byName), Admin: admin,
		}

		op := opAtStage(ichorv1alpha1.KubernetesStageKubelets)

		advanceOK(adv.Advance, cluster, w, op)
		Expect(admin.removedFrom).To(HaveLen(2))
		Expect(admin.removedFrom).To(ConsistOf(BeIdenticalTo(cp1Fake), BeIdenticalTo(cp2Fake)))

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionRemovedAPIsInUse)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	})

	It("finds the Kubernetes node from what the node reports when spec.hostname is unset", func() {
		cluster := k8sCluster(testK8sTarget)
		w := newK8sWorker(cluster.Name, "w-nohost")
		name := w.Spec.Hostname
		newK8sNode(name, true)

		w.Spec.Hostname = ""
		Expect(k8sClient.Update(ctx, w)).To(Succeed())

		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, w, testK8sOld), nodeName: name}
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		Expect(advanceOK(adv.Advance, cluster, fetchMachine(w), opAtStage(ichorv1alpha1.KubernetesStageKubelets))).To(BeFalse())
		Expect(fake.applyCalls).To(HaveLen(1), "the node was found by the name it reports, and the step ran")
	})

	It("applies a worker's kubelet image once, pre-pulls it, and finishes when the node runs the target", func() {
		cluster, w, fake := k8sWorkerFixture(testK8sTarget, "w1", true)
		admin := &fakeKubernetesAdmin{}
		adv := newKubernetesAdvancer(fake, admin)

		Expect(advanceOK(adv.Advance, cluster, w, opAtStage(ichorv1alpha1.KubernetesStageKubelets))).To(BeFalse())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(fake.applyCalls[0]).To(Equal(renderAt(cluster, w, testK8sTarget)))
		Expect(fake.pulled).To(ConsistOf(ContainSubstring(":" + testK8sTarget)))
		Expect(apimeta.IsStatusConditionTrue(fetchMachine(w).Status.Conditions, conditionKubernetesUpgrade)).To(BeTrue())

		// The node has the new config but the kubelet has not restarted yet: resume from the
		// observed state, never apply again.
		fake.current = fake.applyCalls[0]

		Expect(advanceOK(adv.Advance, cluster, fetchMachine(w), opAtStage(ichorv1alpha1.KubernetesStageKubelets))).To(BeFalse())
		Expect(fake.applyCalls).To(HaveLen(1))

		setK8sNode(w.Spec.Hostname, testK8sTarget)

		Expect(advanceOK(adv.Advance, cluster, fetchMachine(w), opAtStage(ichorv1alpha1.KubernetesStageKubelets))).To(BeTrue())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(admin.syncCalls).To(BeZero(), "bootstrap manifests are synced when the control planes finish, not per worker")
		Expect(apimeta.IsStatusConditionTrue(fetchMachine(w).Status.Conditions, conditionKubernetesUpgrade)).To(BeFalse())
	})

	It("evacuates and drains a worker before rolling its kubelet, then uncordons and ends the evacuation", func() {
		cluster := k8sCluster(testK8sTarget)
		w := newK8sWorker(cluster.Name, "wdrain")
		name := w.Spec.Hostname
		newK8sNode(name, true)

		hook, err := storage.NewEvacuationHook(storage.LonghornV1)
		Expect(err).NotTo(HaveOccurred())

		evacuator := &fakeStorageEvacuator{target: &evacuationTarget{hook: hook, timeout: time.Minute, namespace: defaultLonghornNamespace}}
		kube := newDrainFake(func() int { return 0 }, bareNode(name))

		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, w, testK8sOld)}
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})
		adv.Evacuator, adv.Kube = evacuator, kube
		op := opAtStage(ichorv1alpha1.KubernetesStageKubelets)

		Expect(advanceOK(adv.Advance, cluster, w, op)).To(BeFalse())
		Expect(evacuator.beginCalls).To(Equal(1))
		Expect(fake.applyCalls).To(BeEmpty(), "the kubelet is not rolled before the evacuation finished")

		Expect(advanceOK(adv.Advance, cluster, fetchMachine(w), op)).To(BeFalse())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(kube.node(name).Spec.Unschedulable).To(BeTrue(), "the node was cordoned before the roll")

		fake.current = fake.applyCalls[0]
		setK8sNode(name, testK8sTarget)

		Expect(advanceOK(adv.Advance, cluster, fetchMachine(w), op)).To(BeTrue())
		Expect(kube.node(name).Spec.Unschedulable).To(BeFalse())
		Expect(evacuator.endCalls).To(Equal(1))
	})

	It("does not roll a kubelet whose evacuation timed out, and leaves the node schedulable", func() {
		cluster := k8sCluster(testK8sTarget)
		w := newK8sWorker(cluster.Name, "wevac")
		name := w.Spec.Hostname
		newK8sNode(name, true)

		hook, err := storage.NewEvacuationHook(storage.LonghornV1)
		Expect(err).NotTo(HaveOccurred())

		evacuator := &fakeStorageEvacuator{
			target: &evacuationTarget{hook: hook, timeout: time.Nanosecond, namespace: defaultLonghornNamespace}, remaining: 2,
		}
		kube := newDrainFake(func() int { return 0 }, bareNode(name))

		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, w, testK8sOld)}
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})
		adv.Evacuator, adv.Kube = evacuator, kube
		op := opAtStage(ichorv1alpha1.KubernetesStageKubelets)

		advanceOK(adv.Advance, cluster, w, op)

		_, err = adv.Advance(ctx, cluster, fetchMachine(w), op)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("timed out"))
		Expect(fake.applyCalls).To(BeEmpty())
		Expect(evacuator.endCalls).To(Equal(1))
		Expect(kube.node(name).Spec.Unschedulable).To(BeFalse())
	})

	It("records the applied marker even when something else wrote the machine's status during the long calls", func() {
		cluster, w, fake := k8sWorkerFixture(testK8sTarget, "conf1", true)
		fake.dryRunHook = func() {
			other := fetchMachine(w)
			apimeta.SetStatusCondition(&other.Status.Conditions, metav1.Condition{
				Type: "Other", Status: metav1.ConditionTrue, Reason: "Concurrent",
			})
			Expect(k8sClient.Status().Update(ctx, other)).To(Succeed())
		}
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		_, err := adv.Advance(ctx, cluster, w, opAtStage(ichorv1alpha1.KubernetesStageKubelets))
		Expect(err).NotTo(HaveOccurred(), "a stale object must not pause the upgrade")

		got := fetchMachine(w)
		Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, conditionKubernetesUpgrade)).To(BeTrue())
		Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, "Other")).To(BeTrue(), "the other writer's condition survives")
	})

	It("pauses (errors) when the node never converges within the verification timeout", func() {
		cluster, w, fake := k8sWorkerFixture(testK8sTarget, "to1", true)
		adv := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{})

		advanceOK(adv.Advance, cluster, w, opAtStage(ichorv1alpha1.KubernetesStageKubelets))

		fake.current = fake.applyCalls[0]

		got := fetchMachine(w)
		cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionKubernetesUpgrade)
		Expect(cond).NotTo(BeNil())
		cond.LastTransitionTime = metav1.NewTime(time.Now().Add(-2 * kubernetesVerifyTimeout))
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		done, err := adv.Advance(ctx, cluster, fetchMachine(w), opAtStage(ichorv1alpha1.KubernetesStageKubelets))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("did not converge"))
		Expect(done).To(BeFalse())
	})

	It("in the last stage applies a control plane's whole configuration and does not sync the bootstrap manifests", func() {
		cluster := k8sCluster(testK8sTarget)
		cp1 := newK8sControlPlane(cluster.Name, "cp1")
		newK8sNode(cp1.Spec.Hostname, true)
		setStaticPodsFor(cp1.Spec.Hostname, testK8sOld)
		newKubeProxyDaemonSet(testK8sOld, 2, 2)

		fake1 := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, cp1, testK8sOld)}
		admin := &fakeKubernetesAdmin{}
		adv := newKubernetesAdvancer(fake1, admin)
		op := opAtStage(ichorv1alpha1.KubernetesStageKubelets)

		Expect(advanceOK(adv.Advance, cluster, cp1, op)).To(BeFalse())
		Expect(fake1.applyCalls).To(HaveLen(1))
		Expect(fake1.pulled).To(HaveLen(4), "the apiserver, controller-manager, scheduler and kubelet images are pre-pulled")

		fake1.current = fake1.applyCalls[0]

		// The node's kubelet is on the target, but its static pods have not been replaced yet.
		setK8sNode(cp1.Spec.Hostname, testK8sTarget)

		Expect(advanceOK(adv.Advance, cluster, fetchMachine(cp1), op)).To(BeFalse(), "the static pods still run the old version")

		setStaticPodImages(cp1.Spec.Hostname, testK8sTarget)

		Expect(advanceOK(adv.Advance, cluster, fetchMachine(cp1), op)).To(BeTrue())
		Expect(admin.syncCalls).To(BeZero(), "the manifests are synced in the KubeProxy stage")
	})

	It("stage APIServers moves the apiserver only and pins controller-manager, scheduler, kubelet and kube-proxy to the old version", func() {
		cluster := k8sCluster(testK8sTarget)
		cp := newK8sControlPlane(cluster.Name, "st1")
		newK8sNode(cp.Spec.Hostname, true)
		setStaticPodsFor(cp.Spec.Hostname, testK8sOld)

		fake := &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, cp, testK8sOld)}
		admin := &fakeKubernetesAdmin{}
		op := opAtStage(ichorv1alpha1.KubernetesStageAPIServers)

		Expect(advanceOK(newKubernetesAdvancer(fake, admin).Advance, cluster, cp, op)).To(BeFalse())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(imageTags(fake.applyCalls[0])).To(Equal(map[string]string{
			compAPIServer: testK8sTarget, compControllerManager: testK8sOld, compScheduler: testK8sOld,
			compKubelet: testK8sOld, "kube-proxy": testK8sOld,
		}))
		Expect(fake.pulled).To(HaveLen(1), "only the apiserver image is pulled")

		// A restarted controller (a new advancer) finds the configuration applied and only verifies.
		fake.current = fake.applyCalls[0]

		Expect(advanceOK(newKubernetesAdvancer(fake, admin).Advance, cluster, fetchMachine(cp), op)).To(BeFalse(), "the apiserver still runs the old version")
		Expect(fake.applyCalls).To(HaveLen(1))

		// Only the apiserver pod moved; the stage does not wait for the other two.
		newStaticPodImage(cp.Spec.Hostname, compAPIServer, testK8sTarget)

		Expect(advanceOK(newKubernetesAdvancer(fake, admin).Advance, cluster, fetchMachine(cp), op)).To(BeTrue(), "the apiserver is Ready on the target; the others are not this stage's business")
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(admin.syncCalls).To(BeZero())
	})

	It("stage ControlPlaneComponents moves controller-manager and scheduler and leaves the kubelet and kube-proxy on the old version", func() {
		cluster := k8sCluster(testK8sTarget)
		cp := newK8sControlPlane(cluster.Name, "st1b")
		newK8sNode(cp.Spec.Hostname, true)
		setStaticPodsFor(cp.Spec.Hostname, testK8sOld)
		newStaticPodImage(cp.Spec.Hostname, compAPIServer, testK8sTarget)

		fake := &fakeTalosClient{
			version: testTalosVersion,
			current: renderStaged(cluster, cp, &render.StagedKubernetes{
				KubeletVersion: testK8sOld, KubeProxyVersion: testK8sOld, ControllerManagerVersion: testK8sOld, SchedulerVersion: testK8sOld,
			}),
		}
		admin := &fakeKubernetesAdmin{}
		op := opAtStage(ichorv1alpha1.KubernetesStageControlPlaneComponents)

		Expect(advanceOK(newKubernetesAdvancer(fake, admin).Advance, cluster, cp, op)).To(BeFalse())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(imageTags(fake.applyCalls[0])).To(Equal(map[string]string{
			compAPIServer: testK8sTarget, compControllerManager: testK8sTarget, compScheduler: testK8sTarget,
			compKubelet: testK8sOld, "kube-proxy": testK8sOld,
		}))
		Expect(fake.pulled).To(HaveLen(2), "the kubelet image is not pulled yet")

		fake.current = fake.applyCalls[0]

		Expect(advanceOK(newKubernetesAdvancer(fake, admin).Advance, cluster, fetchMachine(cp), op)).To(BeFalse(), "controller-manager and scheduler still run the old version")

		setStaticPodImages(cp.Spec.Hostname, testK8sTarget)

		Expect(advanceOK(newKubernetesAdvancer(fake, admin).Advance, cluster, fetchMachine(cp), op)).To(BeTrue(), "the kubelet is still old and that is what this stage wants")
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(admin.syncCalls).To(BeZero())
	})

	It("stage KubeProxy moves kube-proxy only, then syncs the manifests and waits for the rollout", func() {
		cluster := k8sCluster(testK8sTarget)
		cp := newK8sControlPlane(cluster.Name, "st2")
		newK8sNode(cp.Spec.Hostname, true)
		setStaticPodsFor(cp.Spec.Hostname, testK8sTarget)
		newKubeProxyDaemonSet(testK8sTarget, 3, 1)

		fake := &fakeTalosClient{
			version: testTalosVersion,
			current: renderStaged(cluster, cp, &render.StagedKubernetes{KubeletVersion: testK8sOld, KubeProxyVersion: testK8sOld}),
		}
		admin := &fakeKubernetesAdmin{}
		op := opAtStage(ichorv1alpha1.KubernetesStageKubeProxy)

		Expect(advanceOK(newKubernetesAdvancer(fake, admin).Advance, cluster, cp, op)).To(BeFalse())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(imageTags(fake.applyCalls[0])).To(HaveKeyWithValue("kube-proxy", testK8sTarget))
		Expect(imageTags(fake.applyCalls[0])).To(HaveKeyWithValue(compKubelet, testK8sOld))
		Expect(imageTags(fake.applyCalls[0])).To(HaveKeyWithValue(compAPIServer, testK8sTarget))
		Expect(fake.pulled).To(BeEmpty(), "kube-proxy is a DaemonSet the sync rolls out, and nothing else changed")
		Expect(admin.syncCalls).To(BeZero())

		fake.current = fake.applyCalls[0]

		Expect(advanceOK(newKubernetesAdvancer(fake, admin).Advance, cluster, fetchMachine(cp), op)).To(BeFalse(), "kube-proxy has not finished rolling out")
		Expect(admin.syncCalls).To(Equal(1))
		Expect(fake.applyCalls).To(HaveLen(1))

		var ds appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: kubeProxyName}, &ds)).To(Succeed())
		ds.Status.NumberReady = 3
		ds.Status.UpdatedNumberScheduled = 3
		Expect(k8sClient.Status().Update(ctx, &ds)).To(Succeed())

		Expect(advanceOK(newKubernetesAdvancer(fake, admin).Advance, cluster, fetchMachine(cp), op)).To(BeTrue())
	})

	It("stage KubeProxy syncs the manifests but waits for no kube-proxy when the CNI replaces it", func() {
		cluster := k8sCluster(testK8sTarget, ichorv1alpha1.TalosClusterCNI{Preset: testCilium, KubeProxy: "replaced"})

		cp := newK8sControlPlane(cluster.Name, "st2r")
		newK8sNode(cp.Spec.Hostname, true)
		setStaticPodsFor(cp.Spec.Hostname, testK8sTarget)

		// No kube-proxy DaemonSet exists at all.
		fake := &fakeTalosClient{
			version: testTalosVersion,
			current: renderStaged(cluster, cp, &render.StagedKubernetes{KubeletVersion: testK8sOld}),
		}
		admin := &fakeKubernetesAdmin{}

		Expect(advanceOK(newKubernetesAdvancer(fake, admin).Advance, cluster, cp, opAtStage(ichorv1alpha1.KubernetesStageKubeProxy))).To(BeTrue())
		Expect(admin.syncCalls).To(Equal(1))
	})

	It("stage Kubelets moves the kubelet only, and the node ends on the cluster's desired configuration", func() {
		cluster := k8sCluster(testK8sTarget)
		cp := newK8sControlPlane(cluster.Name, "st3")
		newK8sNode(cp.Spec.Hostname, true)
		setStaticPodsFor(cp.Spec.Hostname, testK8sTarget)

		fake := &fakeTalosClient{
			version: testTalosVersion,
			current: renderStaged(cluster, cp, &render.StagedKubernetes{KubeletVersion: testK8sOld}),
		}
		admin := &fakeKubernetesAdmin{}
		op := opAtStage(ichorv1alpha1.KubernetesStageKubelets)

		Expect(advanceOK(newKubernetesAdvancer(fake, admin).Advance, cluster, cp, op)).To(BeFalse())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(fake.applyCalls[0]).To(Equal(renderAt(cluster, cp, testK8sTarget)))
		Expect(fake.pulled).To(ConsistOf(ContainSubstring("kubelet:" + testK8sTarget)))

		fake.current = fake.applyCalls[0]

		Expect(advanceOK(newKubernetesAdvancer(fake, admin).Advance, cluster, fetchMachine(cp), op)).To(BeFalse(), "the kubelet has not restarted yet")
		Expect(fake.applyCalls).To(HaveLen(1))

		setK8sNode(cp.Spec.Hostname, testK8sTarget)

		Expect(advanceOK(newKubernetesAdvancer(fake, admin).Advance, cluster, fetchMachine(cp), op)).To(BeTrue())
		Expect(fake.applyCalls).To(HaveLen(1))
	})

	for i, stage := range []string{
		ichorv1alpha1.KubernetesStageAPIServers, ichorv1alpha1.KubernetesStageControlPlaneComponents,
		ichorv1alpha1.KubernetesStageKubeProxy, ichorv1alpha1.KubernetesStageKubelets,
	} {
		It("stage "+stage+": a failed apply is an error, so the operation pauses, and nothing is marked applied", func() {
			cluster := k8sCluster(testK8sTarget)
			cp := newK8sControlPlane(cluster.Name, fmt.Sprintf("fail%d", i))
			newK8sNode(cp.Spec.Hostname, true)
			setStaticPodsFor(cp.Spec.Hostname, testK8sOld)

			current := map[string][]byte{
				ichorv1alpha1.KubernetesStageAPIServers: renderAt(cluster, cp, testK8sOld),
				ichorv1alpha1.KubernetesStageControlPlaneComponents: renderStaged(cluster, cp, &render.StagedKubernetes{
					KubeletVersion: testK8sOld, KubeProxyVersion: testK8sOld, ControllerManagerVersion: testK8sOld, SchedulerVersion: testK8sOld,
				}),
				ichorv1alpha1.KubernetesStageKubeProxy: renderStaged(cluster, cp,
					&render.StagedKubernetes{KubeletVersion: testK8sOld, KubeProxyVersion: testK8sOld}),
				ichorv1alpha1.KubernetesStageKubelets: renderStaged(cluster, cp, &render.StagedKubernetes{KubeletVersion: testK8sOld}),
			}[stage]

			fake := &fakeTalosClient{version: testTalosVersion, current: current, applyErr: fmt.Errorf("boom")}

			done, err := newKubernetesAdvancer(fake, &fakeKubernetesAdmin{}).Advance(ctx, cluster, cp, opAtStage(stage))
			Expect(err).To(MatchError(ContainSubstring("boom")))
			Expect(done).To(BeFalse())
			Expect(apimeta.IsStatusConditionTrue(fetchMachine(cp).Status.Conditions, conditionKubernetesUpgrade)).To(BeFalse())
		})
	}
})

// kubernetesFleet is a small fake cluster for the staged upgrade: machines with a fake Talos
// client each, whose Node and static pods follow whatever configuration is applied to them.
type kubernetesFleet struct {
	cluster  *ichorv1alpha1.TalosCluster
	machines []*ichorv1alpha1.TalosMachine
	byName   map[string]*fakeTalosClient
	admin    *fakeKubernetesAdmin
	op       *ichorv1alpha1.TalosOperation
	seen     map[string]int

	// ownNode and kube, when set, make every pass's advancer the controller of that node, draining
	// through kube.
	ownNode string
	kube    *drainFake
}

func newKubernetesFleet(suffix string, controlPlanes, workers int) *kubernetesFleet {
	f := &kubernetesFleet{
		cluster: k8sCluster(testK8sTarget), byName: map[string]*fakeTalosClient{}, admin: &fakeKubernetesAdmin{},
		seen: map[string]int{},
	}

	for i := range controlPlanes {
		cp := newK8sControlPlane(f.cluster.Name, fmt.Sprintf("%s-cp%d", suffix, i+1))
		setUpgradeOrder(cp, fmt.Sprint(i+1))
		newK8sNode(cp.Spec.Hostname, true)
		setStaticPodsFor(cp.Spec.Hostname, testK8sOld)
		f.machines = append(f.machines, fetchMachine(cp))
	}

	for i := range workers {
		w := newK8sWorker(f.cluster.Name, fmt.Sprintf("%s-w%d", suffix, i+1))
		newK8sNode(w.Spec.Hostname, true)
		f.machines = append(f.machines, w)
	}

	for _, m := range f.machines {
		f.byName[m.Name] = &fakeTalosClient{version: testTalosVersion, current: renderAt(f.cluster, m, testK8sOld)}
	}

	// kube-proxy's DaemonSet is rolled by the manifest sync, which is faked: it is simply ready.
	newKubeProxyDaemonSet(testK8sTarget, 3, 3)

	f.op = newOpTestOperationOfKind(f.cluster.Name, ichorv1alpha1.TalosOperationKindKubernetes)

	return f
}

// reconcile runs one pass with a brand new reconciler and advancer, as a restarted controller
// would, then lets every node take over the configuration applied to it during the pass. It
// returns the (machine, stage) of each apply in the pass.
func (f *kubernetesFleet) reconcile() (applied [][2]string) {
	adv := &kubernetesUpgradeAdvancer{
		Client: k8sClient, Reader: k8sClient, ControllerNamespace: tmNamespace,
		TalosClient: perMachineClientFactory(f.byName), Admin: f.admin, NodeName: f.ownNode,
	}
	if f.kube != nil { // a nil *drainFake in the interface would not be nil
		adv.Kube = f.kube
	}
	r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: adv.Advance, NodeName: f.ownNode}

	mustReconcileOperation(r, f.op)

	stage := ""
	if st := fetchOperation(f.op).Status.Kubernetes; st != nil {
		stage = st.Stage
	}

	for _, m := range f.machines {
		fake := f.byName[m.Name]
		if len(fake.applyCalls) == f.seen[m.Name] {
			continue
		}

		f.seen[m.Name] = len(fake.applyCalls)
		cfg := fake.applyCalls[len(fake.applyCalls)-1]
		fake.current = cfg
		tags := imageTags(cfg)

		setK8sNode(m.Spec.Hostname, tags[compKubelet])

		if m.Spec.Role == roleControlPlane {
			setStaticPodImagesFromConfig(m.Spec.Hostname, cfg)
		}

		applied = append(applied, [2]string{m.Name, stage})

		f.expectNoKubeletNewerThanAnAPIServer()
		f.expectNoComponentNewerThanAnAPIServer()
	}

	return applied
}

// expectNoKubeletNewerThanAnAPIServer is Kubernetes' version-skew policy, checked on the live
// objects after every single apply.
func (f *kubernetesFleet) expectNoKubeletNewerThanAnAPIServer() {
	GinkgoHelper()

	for _, m := range f.machines {
		var node corev1.Node
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: m.Spec.Hostname}, &node)).To(Succeed())

		for _, cp := range f.machines {
			if cp.Spec.Role != roleControlPlane {
				continue
			}

			var pod corev1.Pod
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: "kube-apiserver-" + cp.Spec.Hostname}, &pod)).To(Succeed())
			Expect(semverLess(imageTag(&pod), node.Status.NodeInfo.KubeletVersion)).To(BeFalse(),
				"the kubelet of %s (%s) is newer than the apiserver of %s (%s)",
				m.Name, node.Status.NodeInfo.KubeletVersion, cp.Name, imageTag(&pod))
		}
	}
}

// expectNoComponentNewerThanAnAPIServer is the rule the hardware run broke: the v1.37 scheduler
// reaches the apiservers through an endpoint that balances across all of them, so no
// controller-manager or scheduler may be newer than any apiserver.
func (f *kubernetesFleet) expectNoComponentNewerThanAnAPIServer() {
	GinkgoHelper()

	for _, m := range f.machines {
		if m.Spec.Role != roleControlPlane {
			continue
		}

		for _, component := range []string{compControllerManager, compScheduler} {
			var pod corev1.Pod
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: component + "-" + m.Spec.Hostname}, &pod)).To(Succeed())

			for _, cp := range f.machines {
				if cp.Spec.Role != roleControlPlane {
					continue
				}

				var api corev1.Pod
				Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: "kube-apiserver-" + cp.Spec.Hostname}, &api)).To(Succeed())
				Expect(semverLess(imageTag(&api), imageTag(&pod))).To(BeFalse(),
					"%s of %s (%s) is newer than the apiserver of %s (%s)", component, m.Name, imageTag(&pod), cp.Name, imageTag(&api))
			}
		}
	}
}

func semverLess(a, b string) bool {
	parse := func(v string) (out [3]int) {
		_, _ = fmt.Sscanf(v, "v%d.%d.%d", &out[0], &out[1], &out[2])

		return out
	}

	x, y := parse(a), parse(b)

	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}

	return false
}

var _ = Describe("a staged Kubernetes upgrade through TalosOperationReconciler", func() {
	It("moves every control plane's components, then kube-proxy, then the kubelets; a restarted controller resumes in each phase", func() {
		f := newKubernetesFleet("seq", 3, 1)
		cp1, cp2, cp3, w := f.machines[0], f.machines[1], f.machines[2], f.machines[3]

		var (
			applied        [][2]string
			syncsAtKubelet = -1
		)

		for range 60 {
			for _, a := range f.reconcile() {
				if a[1] == ichorv1alpha1.KubernetesStageKubelets && syncsAtKubelet < 0 {
					syncsAtKubelet = f.admin.syncCalls
				}

				applied = append(applied, a)
			}

			if fetchOperation(f.op).Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted {
				break
			}
		}

		Expect(fetchOperation(f.op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		Expect(applied).To(Equal([][2]string{
			{cp1.Name, ichorv1alpha1.KubernetesStageAPIServers}, {cp2.Name, ichorv1alpha1.KubernetesStageAPIServers},
			{cp3.Name, ichorv1alpha1.KubernetesStageAPIServers},
			{cp1.Name, ichorv1alpha1.KubernetesStageControlPlaneComponents}, {cp2.Name, ichorv1alpha1.KubernetesStageControlPlaneComponents},
			{cp3.Name, ichorv1alpha1.KubernetesStageControlPlaneComponents},
			{cp1.Name, ichorv1alpha1.KubernetesStageKubeProxy}, {cp2.Name, ichorv1alpha1.KubernetesStageKubeProxy},
			{cp3.Name, ichorv1alpha1.KubernetesStageKubeProxy},
			{cp1.Name, ichorv1alpha1.KubernetesStageKubelets}, {cp2.Name, ichorv1alpha1.KubernetesStageKubelets},
			{cp3.Name, ichorv1alpha1.KubernetesStageKubelets},
			{w.Name, ichorv1alpha1.KubernetesStageKubelets},
		}))
		Expect(syncsAtKubelet).To(BeNumerically(">=", 1), "the bootstrap manifests are synced before the first kubelet moves")

		for _, m := range f.machines {
			Expect(f.byName[m.Name].current).To(Equal(renderAt(f.cluster, m, testK8sTarget)),
				"%s ends on the cluster's desired configuration", m.Name)
		}
	})

	It("rolls the controller's own node's kubelet last, drained, and a restarted controller resumes it", func() {
		f := newKubernetesFleet("own", 3, 1)
		cp1, cp2, cp3, w := f.machines[0], f.machines[1], f.machines[2], f.machines[3]
		f.ownNode = cp1.Spec.Hostname
		f.kube = newDrainFake(func() int { return 0 }, bareNode(cp1.Spec.Hostname), bareNode(cp2.Spec.Hostname), bareNode(cp3.Spec.Hostname), bareNode(w.Spec.Hostname))
		kube := f.kube

		var kubelets []string

		cordonedAtOwnRoll := false

		for range 60 {
			for _, a := range f.reconcile() { // every pass is a fresh controller
				if a[1] != ichorv1alpha1.KubernetesStageKubelets {
					continue
				}

				kubelets = append(kubelets, a[0])

				if a[0] == cp1.Name {
					cordonedAtOwnRoll = kube.node(cp1.Spec.Hostname).Spec.Unschedulable
				}
			}

			if fetchOperation(f.op).Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted {
				break
			}
		}

		Expect(fetchOperation(f.op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		Expect(kubelets).To(Equal([]string{cp2.Name, cp3.Name, w.Name, cp1.Name}), "the controller's own node goes last")
		Expect(cordonedAtOwnRoll).To(BeTrue(), "the own node was drained before its kubelet rolled")
		Expect(kube.node(cp1.Spec.Hostname).Spec.Unschedulable).To(BeFalse(), "and released afterwards")
	})

	It("pauses in the APIServers stage when a control plane's apply fails, and touches no kubelet", func() {
		f := newKubernetesFleet("pause", 3, 1)
		cp1, cp2 := f.machines[0], f.machines[1]

		Expect(f.reconcile()).To(Equal([][2]string{{cp1.Name, ichorv1alpha1.KubernetesStageAPIServers}}))

		f.byName[cp2.Name].applyErr = fmt.Errorf("boom")

		for range 6 {
			f.reconcile()
		}

		got := fetchOperation(f.op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).To(ContainSubstring("boom"))
		Expect(got.Status.Kubernetes.Stage).To(Equal(ichorv1alpha1.KubernetesStageAPIServers))

		for _, m := range f.machines {
			Expect(imageTags(f.byName[m.Name].current)[compKubelet]).To(Equal(testK8sOld), "%s", m.Name)
		}
	})

	It("a single control plane is staged too: its apiserver moves before its own kubelet", func() {
		f := newKubernetesFleet("single", 1, 0)
		cp := f.machines[0]

		var applied [][2]string

		for range 20 {
			applied = append(applied, f.reconcile()...)

			if fetchOperation(f.op).Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted {
				break
			}
		}

		Expect(applied).To(Equal([][2]string{
			{cp.Name, ichorv1alpha1.KubernetesStageAPIServers},
			{cp.Name, ichorv1alpha1.KubernetesStageControlPlaneComponents},
			{cp.Name, ichorv1alpha1.KubernetesStageKubeProxy},
			{cp.Name, ichorv1alpha1.KubernetesStageKubelets},
		}))
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
			newK8sNode(cp.Spec.Hostname, true)
		}

		byName := map[string]*fakeTalosClient{}
		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2, cp3} {
			byName[cp.Name] = &fakeTalosClient{version: testTalosVersion, current: renderAt(cluster, cp, testK8sOld)}
		}

		adv := &kubernetesUpgradeAdvancer{
			Client: k8sClient, Reader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: perMachineClientFactory(byName), Admin: &fakeKubernetesAdmin{},
		}

		op := newOpTestOperationOfKind(cluster.Name, ichorv1alpha1.TalosOperationKindKubernetes)

		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: adv.Advance}

		mustReconcileOperation(r, op)

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
		setK8sNode(firstMachine.Spec.Hostname, testK8sOld)

		mustReconcileOperation(r, op)

		got = fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).NotTo(BeEmpty(), fmt.Sprintf("status: %+v", got.Status))
		Expect(got.Status.Machines).To(HaveLen(1), "the other control planes were never started")
	})
})

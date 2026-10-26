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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

const (
	proxyOffInConfig = "enabled: false"
	proxyOnInConfig  = "enabled: true"
	modeReplaced     = "replaced"
	modeEnabled      = "enabled"
)

// fakeKubeProxyAdmin records the removal and, like the real one, deletes the DaemonSet. It also
// records how many nodes had been switched when the removal happened.
type fakeKubeProxyAdmin struct {
	calls     int
	appliedAt int
	applied   func() int
	err       error
}

func (f *fakeKubeProxyAdmin) RemoveKubeProxy(ctx context.Context, _ TalosClient) error {
	f.calls++
	if f.applied != nil {
		f.appliedAt = f.applied()
	}

	if f.err != nil {
		return f.err
	}

	ds := &appsv1.DaemonSet{}
	ds.Namespace, ds.Name = testKubeSystem, kubeProxyName

	return client.IgnoreNotFound(k8sClient.Delete(ctx, ds, client.PropagationPolicy(metav1.DeletePropagationBackground)))
}

// newCilium creates the cilium-config ConfigMap and the cilium DaemonSet, removed when the spec ends.
func newCilium(kpr string, rolledOut bool) {
	cm := &corev1.ConfigMap{}
	cm.Namespace, cm.Name = testKubeSystem, ciliumConfigName
	cm.Data = map[string]string{ciliumKPRKey: kpr}
	Expect(k8sClient.Create(ctx, cm)).To(Succeed())

	ds := &appsv1.DaemonSet{}
	ds.Namespace, ds.Name = testKubeSystem, ciliumName
	ds.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{labelK8sApp: ciliumName}}
	ds.Spec.Template.Labels = map[string]string{labelK8sApp: ciliumName}
	ds.Spec.Template.Spec.Containers = []corev1.Container{{Name: ciliumName, Image: "quay.io/cilium/cilium:v1.18.0"}}
	Expect(k8sClient.Create(ctx, ds)).To(Succeed())

	DeferCleanup(func() {
		_ = k8sClient.Delete(ctx, cm)
		_ = k8sClient.Delete(ctx, ds, client.GracePeriodSeconds(0), client.PropagationPolicy(metav1.DeletePropagationBackground))
	})

	setCiliumRollout(rolledOut)
}

func setCiliumRollout(rolledOut bool) {
	var ds appsv1.DaemonSet
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: ciliumName}, &ds)).To(Succeed())

	ds.Status.ObservedGeneration = ds.Generation
	ds.Status.DesiredNumberScheduled = 3
	ds.Status.UpdatedNumberScheduled, ds.Status.NumberReady = 3, 3

	if !rolledOut {
		ds.Status.UpdatedNumberScheduled, ds.Status.NumberReady = 1, 1
	}

	Expect(k8sClient.Status().Update(ctx, &ds)).To(Succeed())
}

func setCiliumKPR(kpr string) {
	var cm corev1.ConfigMap
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: ciliumConfigName}, &cm)).To(Succeed())
	cm.Data[ciliumKPRKey] = kpr
	Expect(k8sClient.Update(ctx, &cm)).To(Succeed())
}

func ciliumConfigVersion() string {
	var cm corev1.ConfigMap
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: ciliumConfigName}, &cm)).To(Succeed())

	return cm.ResourceVersion
}

func kubeProxyExists() bool {
	var ds appsv1.DaemonSet

	err := k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: kubeProxyName}, &ds)
	if apierrors.IsNotFound(err) {
		return false
	}

	Expect(err).NotTo(HaveOccurred())

	return true
}

func newKubeProxyAdvancer(clients map[string]*fakeTalosClient, admin KubeProxyAdmin) *cniKubeProxyAdvancer {
	return &cniKubeProxyAdvancer{
		Client: k8sClient, Reader: k8sClient, ControllerNamespace: tmNamespace,
		TalosClient: perMachineClientFactory(clients), Admin: admin,
	}
}

func cniSwitchCluster(mode string) *ichorv1alpha1.TalosCluster {
	return k8sCluster(testK8sTarget, ichorv1alpha1.TalosClusterCNI{Preset: testCilium, KubeProxy: mode, Handover: testHandoverDisabled})
}

// nodeConfig renders m as a node that still runs the old mode: spec says old, status unset.
func nodeConfig(cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, old string) []byte {
	c := cluster.DeepCopy()
	c.Spec.CNI.KubeProxy = old
	c.Status.CNI = nil

	bundle, err := loadBundle(ctx, k8sClient, tmNamespace, c)
	Expect(err).NotTo(HaveOccurred())

	rendered, _, _, err := (&TalosMachineReconciler{Client: k8sClient, ControllerNamespace: tmNamespace}).
		renderMachine(ctx, c, m, bundle, nil)
	Expect(err).NotTo(HaveOccurred())

	return rendered
}

func ageCondition(op *ichorv1alpha1.TalosOperation, condType string, by time.Duration) {
	cond := apimeta.FindStatusCondition(op.Status.Conditions, condType)
	Expect(cond).NotTo(BeNil())
	cond.LastTransitionTime = metav1.NewTime(time.Now().Add(-by))
}

func markCompleted(op *ichorv1alpha1.TalosOperation, machines ...*ichorv1alpha1.TalosMachine) {
	for _, m := range machines {
		setMachineStatus(op, m.Name, ichorv1alpha1.TalosOperationMachinePhaseCompleted)
	}
}

var _ = Describe("cniKubeProxyAdvancer", func() {
	It("replaced: touches nothing while Cilium has not taken over, and says what to change", func() {
		newCilium("false", true)
		newKubeProxyDaemonSet(testK8sOld, 2, 2)

		cluster := cniSwitchCluster(modeReplaced)
		cp := newK8sControlPlane(cluster.Name, "kp-wait")
		fake := &fakeTalosClient{current: nodeConfig(cluster, cp, modeEnabled)}
		admin := &fakeKubeProxyAdmin{}
		adv := newKubeProxyAdvancer(map[string]*fakeTalosClient{cp.Name: fake}, admin)
		before := ciliumConfigVersion()

		op := &ichorv1alpha1.TalosOperation{}
		done, err := adv.Advance(ctx, cluster, cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.applyCalls).To(BeEmpty(), "the old side stays until the new side is verified")
		Expect(admin.calls).To(BeZero())
		Expect(kubeProxyExists()).To(BeTrue())
		Expect(fetchCluster(cluster).Status.CNI).To(BeNil(), "the Talos side is not switched yet")
		Expect(ciliumConfigVersion()).To(Equal(before), "Ichor never writes Cilium's configuration")

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionAwaitingCNIChange)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Message).To(ContainSubstring("kubeProxyReplacement: true"))
		Expect(cond.Message).To(ContainSubstring("cluster.inlineManifests"), "not handed over: the entry is in a patch")
	})

	It("replaced: for a handed-over cluster the message points at Git and nothing is written", func() {
		newCilium("false", true)

		cluster := cniSwitchCluster(modeReplaced)
		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: render.ConditionCNIHandover, Status: metav1.ConditionTrue, Reason: reasonHandoverComplete, Message: "done",
		})
		Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

		cp := newK8sControlPlane(cluster.Name, "kp-ho")
		fake := &fakeTalosClient{current: nodeConfig(cluster, cp, modeEnabled)}
		adv := newKubeProxyAdvancer(map[string]*fakeTalosClient{cp.Name: fake}, &fakeKubeProxyAdmin{})
		before := ciliumConfigVersion()

		op := &ichorv1alpha1.TalosOperation{}
		_, err := adv.Advance(ctx, fetchCluster(cluster), cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.applyCalls).To(BeEmpty())
		Expect(ciliumConfigVersion()).To(Equal(before))

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionAwaitingCNIChange)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Message).To(ContainSubstring("Argo CD"))
		Expect(cond.Message).To(ContainSubstring("Git"))
	})

	It("replaced: waits for the Cilium agents to roll out, then pauses on timeout", func() {
		newCilium("true", false)

		cluster := cniSwitchCluster(modeReplaced)
		cp := newK8sControlPlane(cluster.Name, "kp-roll")
		fake := &fakeTalosClient{current: nodeConfig(cluster, cp, modeEnabled)}
		adv := newKubeProxyAdvancer(map[string]*fakeTalosClient{cp.Name: fake}, &fakeKubeProxyAdmin{})

		op := &ichorv1alpha1.TalosOperation{}
		done, err := adv.Advance(ctx, cluster, cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.applyCalls).To(BeEmpty())
		Expect(apimeta.IsStatusConditionTrue(op.Status.Conditions, conditionVerifyingNewSide)).To(BeTrue())

		ageCondition(op, conditionVerifyingNewSide, 2*kubeProxyVerifyTimeout)

		_, err = adv.Advance(ctx, cluster, cp, op)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("timed out"))
		Expect(fake.applyCalls).To(BeEmpty())
	})

	It("replaced: pauses when nobody changes Cilium within the owner's window", func() {
		newCilium("false", true)

		cluster := cniSwitchCluster(modeReplaced)
		cp := newK8sControlPlane(cluster.Name, "kp-owner")
		fake := &fakeTalosClient{current: nodeConfig(cluster, cp, modeEnabled)}
		adv := newKubeProxyAdvancer(map[string]*fakeTalosClient{cp.Name: fake}, &fakeKubeProxyAdmin{})

		op := &ichorv1alpha1.TalosOperation{}
		_, err := adv.Advance(ctx, cluster, cp, op)
		Expect(err).NotTo(HaveOccurred())

		ageCondition(op, conditionAwaitingCNIChange, 2*cniChangeTimeout)

		_, err = adv.Advance(ctx, cluster, cp, op)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("timed out"))
	})

	It("replaced: switches every control plane, then removes kube-proxy once, last", func() {
		newCilium("true", true)
		newKubeProxyDaemonSet(testK8sOld, 2, 2)

		cluster := cniSwitchCluster(modeReplaced)
		cp1 := newK8sControlPlane(cluster.Name, "kp-a")
		cp2 := newK8sControlPlane(cluster.Name, "kp-b")
		f1 := &fakeTalosClient{current: nodeConfig(cluster, cp1, modeEnabled)}
		f2 := &fakeTalosClient{current: nodeConfig(cluster, cp2, modeEnabled)}
		admin := &fakeKubeProxyAdmin{applied: func() int { return len(f1.applyCalls) + len(f2.applyCalls) }}
		adv := newKubeProxyAdvancer(map[string]*fakeTalosClient{cp1.Name: f1, cp2.Name: f2}, admin)
		op := &ichorv1alpha1.TalosOperation{}

		done, err := adv.Advance(ctx, cluster, cp1, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(f1.applyCalls).To(HaveLen(1))
		Expect(string(f1.applyCalls[0])).To(ContainSubstring(proxyOffInConfig))
		Expect(fetchCluster(cluster).Status.CNI).NotTo(BeNil())
		Expect(fetchCluster(cluster).Status.CNI.KubeProxy).To(Equal(modeReplaced))

		f1.current = f1.applyCalls[0]

		done, err = adv.Advance(ctx, fetchCluster(cluster), cp1, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(admin.calls).To(BeZero(), "cp2 still renders kube-proxy: Talos would re-apply what is removed")
		Expect(kubeProxyExists()).To(BeTrue())

		markCompleted(op, cp1)

		_, err = adv.Advance(ctx, fetchCluster(cluster), cp2, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(admin.calls).To(BeZero())

		f2.current = f2.applyCalls[0]

		done, err = adv.Advance(ctx, fetchCluster(cluster), cp2, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(admin.calls).To(Equal(1))
		Expect(admin.appliedAt).To(Equal(2), "removed only after both control planes were switched")
		Expect(kubeProxyExists()).To(BeFalse())
	})

	It("replaced: refuses to remove kube-proxy while a held control plane still renders it", func() {
		newCilium("true", true)
		newKubeProxyDaemonSet(testK8sOld, 2, 2)

		cluster := cniSwitchCluster(modeReplaced)
		cp1 := newK8sControlPlane(cluster.Name, "kp-h1")
		cp2 := newK8sControlPlane(cluster.Name, "kp-h2")
		cp2.Annotations = map[string]string{ichorv1alpha1.TalosMachineUpgradeHoldAnnotation: "maintenance"}
		Expect(k8sClient.Update(ctx, cp2)).To(Succeed())

		f1 := &fakeTalosClient{current: nodeConfig(cluster, cp1, modeEnabled)}
		admin := &fakeKubeProxyAdmin{}
		adv := newKubeProxyAdvancer(map[string]*fakeTalosClient{cp1.Name: f1}, admin)
		op := &ichorv1alpha1.TalosOperation{}

		_, err := adv.Advance(ctx, cluster, cp1, op)
		Expect(err).NotTo(HaveOccurred())

		f1.current = f1.applyCalls[0]

		_, err = adv.Advance(ctx, fetchCluster(cluster), cp1, op)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(cp2.Name))
		Expect(err.Error()).To(ContainSubstring("hold"))
		Expect(admin.calls).To(BeZero())
		Expect(kubeProxyExists()).To(BeTrue())
	})

	It("enabled: brings kube-proxy up first and only then waits for Cilium to be turned off", func() {
		newCilium("true", true)

		cluster := cniSwitchCluster(modeEnabled)
		cp := newK8sControlPlane(cluster.Name, "kp-rev")
		fake := &fakeTalosClient{current: nodeConfig(cluster, cp, modeReplaced)}
		admin := &fakeKubeProxyAdmin{}
		adv := newKubeProxyAdvancer(map[string]*fakeTalosClient{cp.Name: fake}, admin)
		before := ciliumConfigVersion()
		op := &ichorv1alpha1.TalosOperation{}

		done, err := adv.Advance(ctx, cluster, cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(string(fake.applyCalls[0])).To(ContainSubstring(proxyOnInConfig))
		Expect(fetchCluster(cluster).Status.CNI.KubeProxy).To(Equal(modeEnabled))

		fake.current = fake.applyCalls[0]

		// Talos has not created the DaemonSet yet.
		done, err = adv.Advance(ctx, fetchCluster(cluster), cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(apimeta.IsStatusConditionTrue(op.Status.Conditions, conditionVerifyingNewSide)).To(BeTrue())
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionAwaitingCNIChange)).To(BeNil(),
			"Cilium is not asked to step aside before kube-proxy runs")

		newKubeProxyDaemonSet(testK8sTarget, 3, 1)

		done, err = adv.Advance(ctx, fetchCluster(cluster), cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionAwaitingCNIChange)).To(BeNil())

		var ds appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: kubeProxyName}, &ds)).To(Succeed())
		ds.Status.NumberReady, ds.Status.UpdatedNumberScheduled = 3, 3
		Expect(k8sClient.Status().Update(ctx, &ds)).To(Succeed())

		done, err = adv.Advance(ctx, fetchCluster(cluster), cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionAwaitingCNIChange)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Message).To(ContainSubstring("kubeProxyReplacement: false"))

		setCiliumKPR("false")
		setCiliumRollout(true)

		done, err = adv.Advance(ctx, fetchCluster(cluster), cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(admin.calls).To(BeZero(), "kube-proxy is the new side here: nothing is removed")
		Expect(apimeta.IsStatusConditionTrue(op.Status.Conditions, conditionAwaitingCNIChange)).To(BeFalse())
		Expect(ciliumConfigVersion()).NotTo(Equal(before), "only the test changed it")
	})

	It("refuses a CNI whose live state it cannot read", func() {
		cluster := k8sCluster(testK8sTarget, ichorv1alpha1.TalosClusterCNI{
			Preset: "custom", KubeProxy: modeReplaced, Handover: testHandoverDisabled,
			Custom: &ichorv1alpha1.TalosClusterCNICustom{Source: ichorv1alpha1.TalosClusterCNICustomSource{
				RepoURL: "https://example.invalid/cni.git", Path: "cni", TargetRevision: "main",
			}},
		})
		cp := newK8sControlPlane(cluster.Name, "kp-custom")
		fake := &fakeTalosClient{current: nodeConfig(cluster, cp, modeEnabled)}
		adv := newKubeProxyAdvancer(map[string]*fakeTalosClient{cp.Name: fake}, &fakeKubeProxyAdmin{})

		_, err := adv.Advance(ctx, cluster, cp, &ichorv1alpha1.TalosOperation{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cannot verify"))
		Expect(fake.applyCalls).To(BeEmpty())
	})
})

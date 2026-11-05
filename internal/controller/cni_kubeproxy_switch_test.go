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
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
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
	got       TalosClient // the client of the latest call
	appliedAt int
	applied   func() int
	err       error

	// restores counts RestoreKubeProxy calls; like Talos's rendered set it creates the DaemonSet
	// (one node, not yet ready) unless restoreErr is set.
	restores   int
	restoreErr error
}

func (f *fakeKubeProxyAdmin) RestoreKubeProxy(ctx context.Context, _ TalosClient) error {
	f.restores++
	if f.restoreErr != nil {
		return f.restoreErr
	}

	if kubeProxyExists() {
		return nil
	}

	ds := &appsv1.DaemonSet{}
	ds.Namespace, ds.Name = testKubeSystem, kubeProxyName
	ds.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{labelK8sApp: kubeProxyName}}
	ds.Spec.Template.Labels = map[string]string{labelK8sApp: kubeProxyName}
	ds.Spec.Template.Spec.Containers = []corev1.Container{{Name: kubeProxyName, Image: "registry.k8s.io/kube-proxy:" + testK8sTarget}}
	DeferCleanup(func() {
		_ = k8sClient.Delete(ctx, ds, client.GracePeriodSeconds(0), client.PropagationPolicy(metav1.DeletePropagationBackground))
	})

	return k8sClient.Create(ctx, ds)
}

func (f *fakeKubeProxyAdmin) RemoveKubeProxy(ctx context.Context, talos TalosClient) error {
	f.calls++
	f.got = talos
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
		TalosClient: perMachineClientFactory(clients), Admin: admin, Images: &fakeInstallerImages{digest: testInstallerDigest},
	}
}

// switchRig is one control plane that still runs the old kube-proxy mode, its fake Talos client, and an
// advancer that reaches it.
func switchRig(
	cluster *ichorv1alpha1.TalosCluster, suffix, old string, admin KubeProxyAdmin,
) (*ichorv1alpha1.TalosMachine, *fakeTalosClient, *cniKubeProxyAdvancer) {
	cp := newK8sControlPlane(cluster.Name, suffix)
	fake := &fakeTalosClient{current: nodeConfig(cluster, cp, old)}

	return cp, fake, newKubeProxyAdvancer(map[string]*fakeTalosClient{cp.Name: fake}, admin)
}

func cniSwitchCluster(mode string) *ichorv1alpha1.TalosCluster {
	return k8sCluster(testK8sTarget, ichorv1alpha1.TalosClusterCNI{Preset: testCilium, KubeProxy: mode})
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

func cniJobExists(cluster *ichorv1alpha1.TalosCluster) bool {
	hash, err := CNIValuesHash(cluster, specMode(cluster), liveNodes())
	Expect(err).NotTo(HaveOccurred())

	err = k8sClient.Get(ctx, client.ObjectKey{Namespace: cniJobNamespace, Name: cniHelmJobName(cluster, hash)}, &batchv1.Job{})
	Expect(client.IgnoreNotFound(err)).To(Succeed())

	return err == nil
}

// finishCNIJob creates the Helm Job for cluster and completes it, as the Job controller and the
// Job's pod would (envtest runs neither).
func finishCNIJob(cluster *ichorv1alpha1.TalosCluster) {
	GinkgoHelper()

	var inst ichorv1alpha1.IchorInstallation
	Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ichorv1alpha1.IchorInstallationName}, &inst)).To(Succeed())

	_, err := AdvanceCNIHelmJob(ctx, k8sClient, tmNamespace, cluster, specMode(cluster), liveNodes(), &inst, &fakeInstallerImages{digest: testInstallerDigest}, nil, "", time.Time{})
	Expect(err).NotTo(HaveOccurred())

	setJobCondition(cniJob(cluster), batchv1.JobComplete, "")
}

func markCompleted(op *ichorv1alpha1.TalosOperation, machines ...*ichorv1alpha1.TalosMachine) {
	for _, m := range machines {
		setMachineStatus(op, m.Name, ichorv1alpha1.TalosOperationMachinePhaseCompleted)
	}
}

var _ = Describe("cniKubeProxyAdvancer", func() {
	BeforeEach(func() {
		createInstallation("v0.2.0")
		DeferCleanup(func() {
			Expect(k8sClient.DeleteAllOf(ctx, &batchv1.Job{}, client.InNamespace(cniJobNamespace),
				client.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())
		})
	})

	Describe("apply with an unrelated pending change", func() {
		// renderAs renders m as the node with the old mode and baseline, and the cluster's desired
		// configuration with cfg added: the pending change is cfg alone, besides the kube-proxy mode.
		renderAs := func(cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, cfg *typedconfig.Config) []byte {
			bundle, err := loadBundle(ctx, k8sClient, tmNamespace, cluster)
			Expect(err).NotTo(HaveOccurred())

			patch := ichorv1alpha1.TalosConfigPatch{
				Name: "extra", Namespace: tmNamespace,
				Spec: ichorv1alpha1.TalosConfigPatchSpec{
					ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Layer: layerCluster,
					Documents: []ichorv1alpha1.TalosConfigPatchDocument{{Kind: kindConfig, Config: cfg}},
				},
			}

			out, _, _, err := (&TalosMachineReconciler{Client: k8sClient, ControllerNamespace: tmNamespace}).
				renderMachine(ctx, cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch})
			Expect(err).NotTo(HaveOccurred())

			return out
		}

		run := func(extra func(*typedconfig.Config)) (*fakeTalosClient, error) {
			cluster := cniSwitchCluster(modeReplaced)
			cp := newK8sControlPlane(cluster.Name, "kp-extra")

			old := cluster.DeepCopy()
			old.Spec.CNI.KubeProxy = modeEnabled
			base := minimalConfigSkeleton()
			base.MachineConfig.MachineType = roleControlPlane
			current := renderAs(old, cp, base)

			cfg := minimalConfigSkeleton()
			cfg.MachineConfig.MachineType = roleControlPlane
			extra(cfg)

			fake := &fakeTalosClient{}
			err := newKubeProxyAdvancer(nil, nil).apply(ctx, fake, cluster, cp, current, renderAs(cluster, cp, cfg))

			return fake, err
		}

		It("applies a safe pending path together with the switch", func() {
			fake, err := run(func(c *typedconfig.Config) {
				c.ClusterConfig.ClusterInlineManifests = []*typedconfig.ClusterInlineManifest{
					{InlineManifestName: "extra", InlineManifestContents: "kind: List"},
				}
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(fake.applyCalls).To(HaveLen(1))
		})

		It("still refuses a pending reboot-requiring path", func() {
			fake, err := run(func(c *typedconfig.Config) {
				c.MachineConfig.MachineEnv = map[string]string{"HTTP_PROXY": "http://proxy.invalid:3128"}
			})
			Expect(err).To(MatchError(ContainSubstring("unrelated pending configuration change")))
			Expect(fake.applyCalls).To(BeEmpty())
		})
	})

	It("replaced: runs the Cilium Helm Job first and touches nothing else until cilium-config verifies", func() {
		newCilium("false", true)
		newKubeProxyDaemonSet(testK8sOld, 2, 2)

		cluster := cniSwitchCluster(modeReplaced)
		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: render.ConditionCNIHandover, Status: metav1.ConditionFalse, Reason: reasonHandoverWaitingForCilium, Message: "not yet",
		})
		Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

		admin := &fakeKubeProxyAdmin{}
		cp, fake, adv := switchRig(cluster, "kp-wait", modeEnabled, admin)

		op := &ichorv1alpha1.TalosOperation{}
		done, err := adv.Advance(ctx, cluster, cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.applyCalls).To(BeEmpty(), "the old side stays until the new side is verified")
		Expect(admin.calls).To(BeZero())
		Expect(kubeProxyExists()).To(BeTrue())
		Expect(fetchCluster(cluster).Status.CNI).To(BeNil(), "the Talos side is not switched yet")

		job := cniJob(cluster)
		hash, err := CNIValuesHash(cluster, specMode(cluster), liveNodes())
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Annotations).To(HaveKeyWithValue(CNIValuesHashAnnotation, hash))

		other := cluster.DeepCopy()
		other.Spec.CNI.KubeProxy = modeEnabled
		otherHash, err := CNIValuesHash(other, specMode(other), liveNodes())
		Expect(err).NotTo(HaveOccurred())
		Expect(hash).NotTo(Equal(otherHash), "the Job carries the target mode's values")

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionVerifyingNewSide)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Message).To(ContainSubstring("Helm Job"))

		// The Job finishes but cilium-config still says false: keep waiting, still nothing applied.
		setJobCondition(job, batchv1.JobComplete, "")

		done, err = adv.Advance(ctx, cluster, cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.applyCalls).To(BeEmpty())
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionVerifyingNewSide).Message).
			To(ContainSubstring("cilium-config does not match"))

		// Helm updated cilium-config: now the Talos side is switched.
		setCiliumKPR("true")

		done, err = adv.Advance(ctx, cluster, cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(fetchCluster(cluster).Status.CNI.KubeProxy).To(Equal(modeReplaced))
	})

	It("a spec-only kube-proxy change flips nothing; the switch operation runs the Job, and the hand-over agrees afterwards", func() {
		newCilium("false", true)
		newKubeProxyDaemonSet(testK8sOld, 2, 2)

		cluster := cniSwitchCluster(modeEnabled)
		r := &CNIHandoverReconciler{Client: k8sClient, APIReader: k8sClient, Namespace: testIchorNamespace, Images: &fakeInstallerImages{digest: testInstallerDigest}}

		// Hand over at kubeProxy=enabled.
		reconcileHandoverOK(r, cluster)
		setJobCondition(cniJob(cluster), batchv1.JobComplete, "")
		helmRelease(1, "deployed", effectiveValues(cluster))
		reconcileHandoverOK(r, cluster)

		before := fetchCluster(cluster).Status.CNI.AppliedValuesHash
		Expect(before).NotTo(BeEmpty())
		Expect(cniJobCount(cluster)).To(Equal(1))

		// The spec is written, as `ichor cni kube-proxy replaced` does before it creates the operation.
		edited := fetchCluster(cluster)
		edited.Spec.CNI.KubeProxy = modeReplaced
		edited.Annotations = map[string]string{ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation: modeReplaced}
		Expect(k8sClient.Update(ctx, edited)).To(Succeed())

		cmVersion := ciliumConfigVersion()

		for range 2 {
			result := reconcileHandoverOK(r, cluster)
			Expect(result).To(Equal(ctrl.Result{}))
		}

		Expect(cniJobCount(cluster)).To(Equal(1), "the spec write alone starts no Job")
		Expect(ciliumConfigVersion()).To(Equal(cmVersion))
		Expect(fetchCluster(cluster).Status.CNI.AppliedValuesHash).To(Equal(before))

		// The operation: Cilium first, inside the operation, under the verification clock.
		cluster = fetchCluster(cluster)
		cp, fake, adv := switchRig(cluster, "kp-spec", modeEnabled, &fakeKubeProxyAdmin{})

		op := &ichorv1alpha1.TalosOperation{}
		done, err := adv.Advance(ctx, cluster, cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(cniJobCount(cluster)).To(Equal(2), "the operation runs the Job")
		Expect(fake.applyCalls).To(BeEmpty())

		job := cniJob(cluster)
		Expect(job.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--kube-proxy=replaced"))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionVerifyingNewSide).Message).To(ContainSubstring("Helm Job"))

		setJobCondition(job, batchv1.JobComplete, "")
		setCiliumKPR("true")

		done, err = adv.Advance(ctx, cluster, cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fetchCluster(cluster).Status.CNI.KubeProxy).To(Equal(modeReplaced))

		// The hand-over now computes the replaced values (Cilium runs replaced, status says replaced):
		// it adopts the switch's finished Job and does not revert it.
		replaced := fetchCluster(cluster)
		helmRelease(2, "deployed", effectiveValues(replaced))

		reconcileHandoverOK(r, cluster)

		hash, err := CNIValuesHash(replaced, cni.KubeProxyReplaced, liveNodes())
		Expect(err).NotTo(HaveOccurred())
		Expect(fetchCluster(cluster).Status.CNI.AppliedValuesHash).To(Equal(hash))
		Expect(cniJobCount(cluster)).To(Equal(2), "no further Job")
	})

	It("replaced: a failed Helm Job pauses the operation with its reason and changes nothing", func() {
		newCilium("false", true)
		newKubeProxyDaemonSet(testK8sOld, 2, 2)

		cluster := cniSwitchCluster(modeReplaced)
		admin := &fakeKubeProxyAdmin{}
		cp, fake, adv := switchRig(cluster, "kp-jobfail", modeEnabled, admin)
		op := &ichorv1alpha1.TalosOperation{}

		_, err := adv.Advance(ctx, cluster, cp, op)
		Expect(err).NotTo(HaveOccurred())

		setJobCondition(cniJob(cluster), batchv1.JobFailed, "rollout did not become ready")

		_, err = adv.Advance(ctx, cluster, cp, op)
		Expect(err).To(MatchError(ContainSubstring("cilium Helm Job")))
		Expect(err).To(MatchError(ContainSubstring("rollout did not become ready")))
		Expect(fake.applyCalls).To(BeEmpty())
		Expect(admin.calls).To(BeZero())
		Expect(kubeProxyExists()).To(BeTrue())
		Expect(fetchCluster(cluster).Status.CNI).To(BeNil())
	})

	It("replaced: waits for the Cilium agents to roll out, then pauses on timeout", func() {
		newCilium("true", false)

		cluster := cniSwitchCluster(modeReplaced)
		finishCNIJob(cluster)
		cp, fake, adv := switchRig(cluster, "kp-roll", modeEnabled, &fakeKubeProxyAdmin{})

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

	It("replaced: pauses when the Helm Job does not finish within the verification window", func() {
		newCilium("false", true)

		cluster := cniSwitchCluster(modeReplaced)
		cp, _, adv := switchRig(cluster, "kp-owner", modeEnabled, &fakeKubeProxyAdmin{})

		op := &ichorv1alpha1.TalosOperation{}
		_, err := adv.Advance(ctx, cluster, cp, op)
		Expect(err).NotTo(HaveOccurred())

		ageCondition(op, conditionVerifyingNewSide, 2*kubeProxyVerifyTimeout)

		_, err = adv.Advance(ctx, cluster, cp, op)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("timed out"))
	})

	It("replaced: gives the apply its own long deadline, not what is left of the 15s dial bound", func() {
		newCilium("true", true)

		cluster := cniSwitchCluster(modeReplaced)
		finishCNIJob(cluster)
		cp, fake, adv := switchRig(cluster, "kp-budget", modeEnabled, &fakeKubeProxyAdmin{})

		_, err := adv.Advance(ctx, cluster, cp, &ichorv1alpha1.TalosOperation{})
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(fake.applyBudget).To(BeNumerically(">", 4*etcdDialTimeout))
	})

	It("replaced: switches every control plane, then removes kube-proxy once, last", func() {
		newCilium("true", true)
		newKubeProxyDaemonSet(testK8sOld, 2, 2)

		cluster := cniSwitchCluster(modeReplaced)
		finishCNIJob(cluster)
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

	It("replaced: after the removal the kubernetes Service must answer before success is declared", func() {
		newCilium("true", true)
		newKubeProxyDaemonSet(testK8sOld, 2, 2)

		cluster := cniSwitchCluster(modeReplaced)
		finishCNIJob(cluster)
		cp, _, adv := switchRig(cluster, "kp-probe", modeReplaced, &fakeKubeProxyAdmin{})
		probeErr := errors.New("connection refused")
		adv.ServiceProbe = func(context.Context) error { return probeErr }
		op := &ichorv1alpha1.TalosOperation{}

		done, err := adv.Advance(ctx, cluster, cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse(), "the Service does not answer: not done")

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionVerifyingNewSide)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Message).To(ContainSubstring("until it reboots"))

		probeErr = nil

		done, err = adv.Advance(ctx, fetchCluster(cluster), cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
	})

	It("replaced: when a worker finishes last, kube-proxy is removed through a control plane's client", func() {
		newCilium("true", true)
		newKubeProxyDaemonSet(testK8sOld, 2, 2)

		cluster := cniSwitchCluster(modeReplaced)
		finishCNIJob(cluster)
		cp := newK8sControlPlane(cluster.Name, "kp-wcp")
		w := newK8sWorker(cluster.Name, "kp-wrk")
		fcp := &fakeTalosClient{current: nodeConfig(cluster, cp, modeEnabled)}
		fw := &fakeTalosClient{current: nodeConfig(cluster, w, modeEnabled)}
		admin := &fakeKubeProxyAdmin{}
		adv := newKubeProxyAdvancer(map[string]*fakeTalosClient{cp.Name: fcp, w.Name: fw}, admin)
		op := &ichorv1alpha1.TalosOperation{}
		markCompleted(op, cp)

		// A v1.14 worker renders no KubeProxyConfig, so there is nothing to apply to it.
		done, err := adv.Advance(ctx, cluster, w, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(admin.calls).To(Equal(1))
		Expect(admin.got).To(BeIdenticalTo(fcp), "a worker's Talos API has no kubeconfig to give")
	})

	It("replaced: refuses to remove kube-proxy while a held control plane still renders it", func() {
		newCilium("true", true)
		newKubeProxyDaemonSet(testK8sOld, 2, 2)

		cluster := cniSwitchCluster(modeReplaced)
		finishCNIJob(cluster)
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

	It("enabled: a Job for the same values finished before the operation (init's, or an earlier switch) is replaced, not taken as done", func() {
		newCilium("true", true)

		cluster := cniSwitchCluster(modeEnabled)

		var inst ichorv1alpha1.IchorInstallation
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ichorv1alpha1.IchorInstallationName}, &inst)).To(Succeed())
		_, err := AdvanceCNIHelmJob(ctx, k8sClient, tmNamespace, cluster, specMode(cluster), liveNodes(), &inst, &fakeInstallerImages{digest: testInstallerDigest}, nil, "", time.Time{})
		Expect(err).NotTo(HaveOccurred())

		old := cniJob(cluster)
		setJobCondition(old, batchv1.JobComplete, "")

		adv := newKubeProxyAdvancer(map[string]*fakeTalosClient{}, &fakeKubeProxyAdmin{})
		op := &ichorv1alpha1.TalosOperation{}
		op.CreationTimestamp = metav1.NewTime(time.Now().Add(time.Hour))

		ready, err := adv.requireCilium(ctx, cluster, op, cni.KubeProxyEnabled)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())
		Eventually(func() bool { return cniJobExists(cluster) }).Should(BeFalse(), "the stale Job is deleted")

		ready, err = adv.requireCilium(ctx, cluster, op, cni.KubeProxyEnabled)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())
		Expect(cniJobExists(cluster)).To(BeTrue(), "a new Job runs")
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionVerifyingNewSide).Message).To(ContainSubstring("Helm Job"))

		fresh := cniJob(cluster)
		Expect(fresh.UID).NotTo(Equal(old.UID))
		Expect(jobOutcomeDone(fresh)).To(BeFalse())

		setJobCondition(fresh, batchv1.JobComplete, "")
		op.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Hour))
		setCiliumKPR("false")

		ready, err = adv.requireCilium(ctx, cluster, op, cni.KubeProxyEnabled)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
	})

	It("enabled: brings kube-proxy up first and only then waits for Cilium to be turned off", func() {
		newCilium("true", true)

		cluster := cniSwitchCluster(modeEnabled)
		admin := &fakeKubeProxyAdmin{}
		cp, fake, adv := switchRig(cluster, "kp-rev", modeReplaced, admin)
		before := ciliumConfigVersion()
		op := &ichorv1alpha1.TalosOperation{}

		done, err := adv.Advance(ctx, cluster, cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(string(fake.applyCalls[0])).To(ContainSubstring(proxyOnInConfig))
		Expect(fetchCluster(cluster).Status.CNI.KubeProxy).To(Equal(modeEnabled))

		fake.current = fake.applyCalls[0]

		// Talos has not created the DaemonSet (its inventory still lists it): Ichor restores it.
		done, err = adv.Advance(ctx, fetchCluster(cluster), cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(admin.restores).To(Equal(1))
		Expect(kubeProxyExists()).To(BeTrue())
		Expect(apimeta.IsStatusConditionTrue(op.Status.Conditions, conditionVerifyingNewSide)).To(BeTrue())
		Expect(cniJobExists(cluster)).To(BeFalse(), "Cilium is not asked to step aside before kube-proxy runs")

		var created appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: kubeProxyName}, &created)).To(Succeed())
		created.Status.DesiredNumberScheduled, created.Status.UpdatedNumberScheduled, created.Status.NumberReady = 3, 1, 1
		Expect(k8sClient.Status().Update(ctx, &created)).To(Succeed())

		// Once it exists nothing is applied again.
		done, err = adv.Advance(ctx, fetchCluster(cluster), cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(admin.restores).To(Equal(1))

		done, err = adv.Advance(ctx, fetchCluster(cluster), cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(cniJobExists(cluster)).To(BeFalse())

		var ds appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: kubeProxyName}, &ds)).To(Succeed())
		ds.Status.NumberReady, ds.Status.UpdatedNumberScheduled = 3, 3
		Expect(k8sClient.Status().Update(ctx, &ds)).To(Succeed())

		done, err = adv.Advance(ctx, fetchCluster(cluster), cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())

		// kube-proxy runs everywhere: now the Helm Job turns Cilium's replacement off.
		Expect(cniJobExists(cluster)).To(BeTrue())
		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionVerifyingNewSide)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Message).To(ContainSubstring("Helm Job"))

		setJobCondition(cniJob(cluster), batchv1.JobComplete, "")

		done, err = adv.Advance(ctx, fetchCluster(cluster), cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse(), "the Job finished but cilium-config still says true")

		setCiliumKPR("false")
		setCiliumRollout(true)

		done, err = adv.Advance(ctx, fetchCluster(cluster), cp, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(admin.calls).To(BeZero(), "kube-proxy is the new side here: nothing is removed")
		Expect(apimeta.IsStatusConditionTrue(op.Status.Conditions, conditionVerifyingNewSide)).To(BeFalse())
		Expect(ciliumConfigVersion()).NotTo(Equal(before), "only the test changed it")
	})
})

func jobOutcomeDone(job *batchv1.Job) bool {
	done, _ := jobOutcome(job)

	return done
}

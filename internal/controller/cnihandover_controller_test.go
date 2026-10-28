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
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/siderolabs/talos/pkg/machinery/constants"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/render"
)

const helmStatusKey = "status"

func reconcileHandover(r *CNIHandoverReconciler, namespace, name string) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{Namespace: namespace, Name: name})
}

// newTestCiliumCluster is newTestCluster with the CNI preset set to cilium before Create --
// spec.cni.preset is immutable once a TalosCluster exists (the CEL rule on TalosClusterCNI).
func newTestCiliumCluster() *ichorv1alpha1.TalosCluster {
	cluster := &ichorv1alpha1.TalosCluster{
		GenerateName: testClusterGenerate, Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosClusterSpec{
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: testEndpointType, Address: testClusterAddress},
			TalosVersion:      testTalosVersion,
			KubernetesVersion: "v" + constants.DefaultKubernetesVersion,
			VersionContract:   testK8sContract,
			SecretsRef:        corev1.LocalObjectReference{Name: "does-not-exist"},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: testSchedulable},
			CNI:               ichorv1alpha1.TalosClusterCNI{Preset: ciliumName},
		},
	}
	Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
	DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))).To(Succeed()) })

	return cluster
}

func getCluster(cluster *ichorv1alpha1.TalosCluster) *ichorv1alpha1.TalosCluster {
	GinkgoHelper()

	var fetched ichorv1alpha1.TalosCluster
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, &fetched)).To(Succeed())

	return &fetched
}

func conditionOf(cluster *ichorv1alpha1.TalosCluster, condType string) *metav1.Condition {
	GinkgoHelper()

	cond := apimeta.FindStatusCondition(getCluster(cluster).Status.Conditions, condType)
	Expect(cond).NotTo(BeNil(), "condition "+condType)

	return cond
}

// ciliumDaemonSet creates the bootstrapped cilium DaemonSet in kube-system (envtest runs no
// controllers, so its status is whatever the test writes) and returns a setter for readiness.
func ciliumDaemonSet() (setReady func(ready bool)) {
	labels := map[string]string{labelK8sApp: ciliumName}
	ds := &appsv1.DaemonSet{
		Namespace: kubeSystemNamespace, Name: ciliumName,
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "cilium"}}},
			},
		},
	}
	Expect(k8sClient.Create(ctx, ds)).To(Succeed())
	DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, ds))).To(Succeed()) })

	return func(ready bool) {
		GinkgoHelper()

		n := int32(1)
		ds.Status = appsv1.DaemonSetStatus{
			ObservedGeneration: ds.Generation, DesiredNumberScheduled: n, CurrentNumberScheduled: n,
		}

		if ready {
			ds.Status.NumberReady, ds.Status.UpdatedNumberScheduled = n, n
		}

		Expect(k8sClient.Status().Update(ctx, ds)).To(Succeed())
	}
}

// helmRelease writes a Helm release record for cilium the way Helm's Secrets driver does:
// base64 of gzipped JSON, with the labels the driver lists by.
func helmRelease(version int, status string, config map[string]any) {
	GinkgoHelper()

	raw, err := json.Marshal(map[string]any{labelName: ciliumName, "version": version, "config": config, "info": map[string]any{helmStatusKey: status}})
	Expect(err).NotTo(HaveOccurred())

	var zipped bytes.Buffer

	zw := gzip.NewWriter(&zipped)
	_, err = zw.Write(raw)
	Expect(err).NotTo(HaveOccurred())
	Expect(zw.Close()).To(Succeed())

	secret := &corev1.Secret{
		Namespace: testIchorNamespace, Name: "sh.helm.release.v1.cilium.v" + strconv.Itoa(version),
		Type:   "helm.sh/release.v1",
		Labels: map[string]string{helmOwnerLabel: "helm", labelName: ciliumName, helmStatusKey: status, "version": strconv.Itoa(version)},
		Data:   map[string][]byte{"release": []byte(base64.StdEncoding.EncodeToString(zipped.Bytes()))},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).To(Succeed()) })
}

func effectiveValues(cluster *ichorv1alpha1.TalosCluster) map[string]any {
	GinkgoHelper()

	vals, err := cilium.EffectiveValuesFor(cluster)
	Expect(err).NotTo(HaveOccurred())

	return vals
}

func handoverJob(cluster *ichorv1alpha1.TalosCluster) *batchv1.Job {
	GinkgoHelper()

	hash, err := CNIValuesHash(cluster)
	Expect(err).NotTo(HaveOccurred())

	var job batchv1.Job
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testIchorNamespace, Name: cniHelmJobName(cluster, hash)}, &job)).To(Succeed())
	DeferCleanup(func() {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground)))).To(Succeed())
	})

	return &job
}

func cniJobCount(cluster *ichorv1alpha1.TalosCluster) int {
	GinkgoHelper()

	var jobs batchv1.JobList
	Expect(k8sClient.List(ctx, &jobs, client.InNamespace(testIchorNamespace),
		client.MatchingLabels{cniClusterLabel: cniHelmClusterHash(cluster)})).To(Succeed())

	return len(jobs.Items)
}

func cniLockHolder(cluster *ichorv1alpha1.TalosCluster) (string, bool) {
	GinkgoHelper()

	holder, held, err := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0).Holder(ctx)
	Expect(err).NotTo(HaveOccurred())

	return holder, held
}

var _ = Describe("CNIHandoverReconciler", func() {
	var r *CNIHandoverReconciler

	BeforeEach(func() {
		r = &CNIHandoverReconciler{Client: k8sClient, APIReader: k8sClient, Namespace: testIchorNamespace}
		createInstallation(ichorv1alpha1.IchorManagementModeIchor, "v0.2.0")
	})

	It("sets CNIUnsupported on a flannel cluster and leaves the hand-over condition alone", func() {
		cluster := &ichorv1alpha1.TalosCluster{
			GenerateName: testClusterGenerate, Namespace: tmNamespace,
			Spec: ichorv1alpha1.TalosClusterSpec{
				Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: testEndpointType, Address: testClusterAddress},
				TalosVersion:      testTalosVersion,
				KubernetesVersion: "v" + constants.DefaultKubernetesVersion,
				VersionContract:   "v1.14",
				SecretsRef:        corev1.LocalObjectReference{Name: "does-not-exist"},
				ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: testSchedulable},
				CNI:               ichorv1alpha1.TalosClusterCNI{Preset: "flannel"},
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		result, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())

		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIUnsupported).Status).To(Equal(metav1.ConditionTrue))
		Expect(apimeta.FindStatusCondition(getCluster(cluster).Status.Conditions, render.ConditionCNIHandover)).To(BeNil())
	})

	It("ignores a TalosCluster that no longer exists", func() {
		result, err := reconcileHandover(r, tmNamespace, "no-such-cluster")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
	})

	It("waits for the bootstrapped Cilium before it starts a Job", func() {
		cluster := newTestCiliumCluster()

		result, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(handoverRequeueInterval))

		cond := conditionOf(cluster, render.ConditionCNIHandover)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("WaitingForCilium"))

		Expect(cniJobCount(cluster)).To(BeZero())
	})

	It("completes the hand-over after the Job succeeded, the release is deployed and the DaemonSet is ready", func() {
		cluster := newTestCiliumCluster()
		setReady := ciliumDaemonSet()
		setReady(true)

		// The Job is created and the cluster lock is held while it runs.
		result, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(cniJobPollInterval))
		job := handoverJob(cluster)

		holder, held := cniLockHolder(cluster)
		Expect(held).To(BeTrue())
		Expect(holder).To(Equal(cniHelmLockIdentity))

		cond := conditionOf(cluster, render.ConditionCNIHandover)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("AdoptingCilium"))

		// Still running: nothing changes, the lock stays.
		_, err = reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		_, held = cniLockHolder(cluster)
		Expect(held).To(BeTrue())

		// The Job succeeded but no release record exists: not handed over, lock released.
		setJobCondition(job, batchv1.JobComplete, "")
		result, err = reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(handoverRequeueInterval))
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionFalse))
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Reason).To(Equal("WaitingForRelease"))
		_, held = cniLockHolder(cluster)
		Expect(held).To(BeFalse())

		// A release that is not deployed does not count either.
		helmRelease(1, "pending-install", effectiveValues(cluster))
		_, err = reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionFalse))

		// Deployed at the cluster's values, but the DaemonSet is not ready.
		helmRelease(2, "deployed", effectiveValues(cluster))
		setReady(false)
		result, err = reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(handoverRequeueInterval))
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionFalse))

		// All three: done.
		setReady(true)
		result, err = reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())

		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionTrue))
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Reason).To(Equal("Complete"))
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Status).To(Equal(metav1.ConditionTrue))

		hash, err := CNIValuesHash(cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(getCluster(cluster).Status.CNI.AppliedValuesHash).To(Equal(hash))

		// Steady state: nothing more to do.
		result, err = reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(ctrl.Result{}))
	})

	It("starts a new Job when the values change after the hand-over", func() {
		cluster := newTestCiliumCluster()
		ciliumDaemonSet()(true)

		_, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		setJobCondition(handoverJob(cluster), batchv1.JobComplete, "")
		helmRelease(1, "deployed", effectiveValues(cluster))
		_, err = reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionTrue))

		first, err := CNIValuesHash(cluster)
		Expect(err).NotTo(HaveOccurred())

		// The user edits the values.
		edited := getCluster(cluster)
		edited.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{
			Values: &apiextensionsv1.JSON{Raw: []byte(`{"operator":{"replicas":2}}`)},
		}
		Expect(k8sClient.Update(ctx, edited)).To(Succeed())

		result, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(cniJobPollInterval))

		second, err := CNIValuesHash(getCluster(cluster))
		Expect(err).NotTo(HaveOccurred())
		Expect(second).NotTo(Equal(first))

		job := handoverJob(getCluster(cluster))
		Expect(job.Annotations[CNIValuesHashAnnotation]).To(Equal(second))

		// The hand-over is not undone while the new Job runs, and the lock is held.
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionTrue))
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Status).To(Equal(metav1.ConditionFalse))
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Reason).To(Equal("Applying"))
		Expect(getCluster(cluster).Status.CNI.AppliedValuesHash).To(Equal(first))

		holder, held := cniLockHolder(cluster)
		Expect(held).To(BeTrue())
		Expect(holder).To(Equal(cniHelmLockIdentity))

		// Done at the new values: recorded, lock released.
		setJobCondition(job, batchv1.JobComplete, "")
		helmRelease(2, "deployed", effectiveValues(getCluster(cluster)))
		_, err = reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(getCluster(cluster).Status.CNI.AppliedValuesHash).To(Equal(second))
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Status).To(Equal(metav1.ConditionTrue))
		_, held = cniLockHolder(cluster)
		Expect(held).To(BeFalse())
	})

	It("sets the condition with the Job's reason when it failed, and releases the lock", func() {
		cluster := newTestCiliumCluster()
		ciliumDaemonSet()(true)

		_, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())

		job := handoverJob(cluster)
		setJobCondition(job, batchv1.JobFailed, "Job has reached the specified backoff limit")

		result, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())

		cond := conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("JobFailed"))
		Expect(cond.Message).To(ContainSubstring("backoff limit"))
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionFalse))

		_, held := cniLockHolder(cluster)
		Expect(held).To(BeFalse())
	})

	It("puts the Job's last log line in the failure condition", func() {
		cluster := newTestCiliumCluster()
		ciliumDaemonSet()(true)

		_, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())

		job := handoverJob(cluster)
		pod := &corev1.Pod{
			Namespace: testIchorNamespace, GenerateName: "cni-job-pod-", Labels: map[string]string{"job-name": job.Name},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "cni", Image: "x"}}},
		}
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, pod))).To(Succeed()) })

		// client-go's fake clientset answers every log request with "fake logs".
		r.Kube = kubefake.NewSimpleClientset()
		setJobCondition(job, batchv1.JobFailed, "Job has reached the specified backoff limit")

		_, err = reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Message).To(ContainSubstring("fake logs"))
	})

	It("holds back while a node rollout has the cluster lock, and goes on when it ends", func() {
		cluster := newTestCiliumCluster()
		ciliumDaemonSet()(true)

		rollout := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
		Expect(rollout.Acquire(ctx, controllerIdentity)).To(Succeed())
		DeferCleanup(func() { Expect(rollout.Release(ctx, controllerIdentity)).To(Succeed()) })

		result, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(handoverRequeueInterval))
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Reason).To(Equal("WaitingForLock"))

		Expect(cniJobCount(cluster)).To(BeZero())

		Expect(rollout.Release(ctx, controllerIdentity)).To(Succeed())

		result, err = reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(cniJobPollInterval))
		handoverJob(cluster)
	})
})

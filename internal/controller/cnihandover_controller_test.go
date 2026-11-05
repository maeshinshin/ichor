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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

const (
	helmStatusKey  = "status"
	helmVersionKey = "version"

	missingSecretName = "does-not-exist"
)

func reconcileHandover(r *CNIHandoverReconciler, namespace, name string) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{Namespace: namespace, Name: name})
}

// reconcileHandoverOK reconciles cluster once and requires that it succeeds.
func reconcileHandoverOK(r *CNIHandoverReconciler, cluster *ichorv1alpha1.TalosCluster) ctrl.Result {
	GinkgoHelper()

	result, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
	Expect(err).NotTo(HaveOccurred())

	return result
}

// newTestCiliumCluster is newTestCluster with the CNI preset set to cilium before Create --
// spec.cni.preset is immutable once a TalosCluster exists (the CEL rule on TalosClusterCNI).
func newTestCiliumCluster() *ichorv1alpha1.TalosCluster { return newTestPresetCluster(ciliumName) }

// newTestPresetCluster creates a TalosCluster with the given CNI preset.
func newTestPresetCluster(preset string) *ichorv1alpha1.TalosCluster {
	spec := newClusterSpec(missingSecretName, testK8sContract)
	spec.CNI = ichorv1alpha1.TalosClusterCNI{Preset: preset}

	cluster := &ichorv1alpha1.TalosCluster{GenerateName: testClusterGenerate, Namespace: tmNamespace, Spec: spec}
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
	helmReleaseAt(version, status, "", config)
}

// helmReleaseAt is helmRelease for a release deployed from chart chartVersion.
func helmReleaseAt(version int, status, chartVersion string, config map[string]any) {
	GinkgoHelper()

	raw, err := json.Marshal(map[string]any{
		labelName: ciliumName, helmVersionKey: version, "config": config, "info": map[string]any{helmStatusKey: status},
		"chart": map[string]any{"metadata": map[string]any{helmVersionKey: chartVersion}},
	})
	Expect(err).NotTo(HaveOccurred())

	var zipped bytes.Buffer

	zw := gzip.NewWriter(&zipped)
	_, err = zw.Write(raw)
	Expect(err).NotTo(HaveOccurred())
	Expect(zw.Close()).To(Succeed())

	secret := &corev1.Secret{
		Namespace: testIchorNamespace, Name: "sh.helm.release.v1.cilium.v" + strconv.Itoa(version),
		Type:   "helm.sh/release.v1",
		Labels: map[string]string{helmOwnerLabel: "helm", labelName: ciliumName, helmStatusKey: status, helmVersionKey: strconv.Itoa(version)},
		Data:   map[string][]byte{"release": []byte(base64.StdEncoding.EncodeToString(zipped.Bytes()))},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).To(Succeed()) })
}

// liveNodes is how many Nodes the envtest API server has, which is what the reconcilers count into the
// Cilium values.
func liveNodes() int {
	GinkgoHelper()

	n, err := clusterNodeCount(ctx, k8sClient)
	Expect(err).NotTo(HaveOccurred())

	return n
}

// failJobJustNow marks job failed at this moment (setJobCondition leaves the transition time empty,
// which reads as long ago and so as a retry interval that is already over).
func failJobJustNow(job *batchv1.Job, msg string) {
	GinkgoHelper()

	setJobCondition(job, batchv1.JobFailed, msg)

	now := metav1.Now()
	for i := range job.Status.Conditions {
		job.Status.Conditions[i].LastTransitionTime = now
	}

	Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())
}

func effectiveValues(cluster *ichorv1alpha1.TalosCluster) map[string]any {
	GinkgoHelper()

	vals, err := cilium.EffectiveValuesForNodes(cluster, specMode(cluster), liveNodes())
	Expect(err).NotTo(HaveOccurred())

	return vals
}

func cniJob(cluster *ichorv1alpha1.TalosCluster) *batchv1.Job {
	GinkgoHelper()

	hash, err := CNIValuesHash(cluster, specMode(cluster), liveNodes())
	Expect(err).NotTo(HaveOccurred())

	var job batchv1.Job
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cniJobNamespace, Name: cniHelmJobName(cluster, hash)}, &job)).To(Succeed())
	DeferCleanup(func() {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground)))).To(Succeed())
	})

	return &job
}

func cniJobCount(cluster *ichorv1alpha1.TalosCluster) int {
	GinkgoHelper()

	var jobs batchv1.JobList
	Expect(k8sClient.List(ctx, &jobs, client.InNamespace(cniJobNamespace),
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
		createInstallation("v0.2.0")
	})

	It("sets CNIUnsupported on a flannel cluster and leaves the hand-over condition alone", func() {
		cluster := newTestPresetCluster("flannel")

		result := reconcileHandoverOK(r, cluster)
		Expect(result.RequeueAfter).To(BeZero())

		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIUnsupported).Status).To(Equal(metav1.ConditionTrue))
		Expect(apimeta.FindStatusCondition(getCluster(cluster).Status.Conditions, render.ConditionCNIHandover)).To(BeNil())
	})

	It("does nothing for an unmanaged cluster: no conditions, no Job, and not CNIUnsupported", func() {
		cluster := newTestPresetCluster(ichorv1alpha1.TalosClusterCNIPresetUnmanaged)
		cluster.Annotations = map[string]string{ichorv1alpha1.TalosClusterAdoptedAnnotation: resetOnDeleteValue}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		result := reconcileHandoverOK(r, cluster)
		Expect(result.RequeueAfter).To(BeZero())

		got := getCluster(cluster)
		Expect(got.Status.Conditions).To(BeEmpty())

		var jobs batchv1.JobList
		Expect(k8sClient.List(ctx, &jobs, client.InNamespace(cniJobNamespace))).To(Succeed())
		Expect(jobs.Items).To(BeEmpty())
	})

	It("ignores a TalosCluster that no longer exists", func() {
		result, err := reconcileHandover(r, tmNamespace, "no-such-cluster")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
	})

	It("waits for the bootstrapped Cilium before it starts a Job", func() {
		cluster := newTestCiliumCluster()

		result := reconcileHandoverOK(r, cluster)
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
		result := reconcileHandoverOK(r, cluster)
		Expect(result.RequeueAfter).To(Equal(cniJobPollInterval))
		job := cniJob(cluster)

		holder, held := cniLockHolder(cluster)
		Expect(held).To(BeTrue())
		Expect(holder).To(Equal(cniHelmLockIdentity))

		cond := conditionOf(cluster, render.ConditionCNIHandover)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("AdoptingCilium"))

		// Still running: nothing changes, the lock stays.
		reconcileHandoverOK(r, cluster)
		_, held = cniLockHolder(cluster)
		Expect(held).To(BeTrue())

		// The Job succeeded but no release record exists: not handed over, lock released.
		setJobCondition(job, batchv1.JobComplete, "")
		result = reconcileHandoverOK(r, cluster)
		Expect(result.RequeueAfter).To(Equal(handoverRequeueInterval))
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionFalse))
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Reason).To(Equal("WaitingForRelease"))
		_, held = cniLockHolder(cluster)
		Expect(held).To(BeFalse())

		// A release that is not deployed does not count either.
		helmRelease(1, "pending-install", effectiveValues(cluster))
		reconcileHandoverOK(r, cluster)
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionFalse))

		// Deployed at the cluster's values, but the DaemonSet is not ready.
		helmRelease(2, "deployed", effectiveValues(cluster))
		setReady(false)
		result = reconcileHandoverOK(r, cluster)
		Expect(result.RequeueAfter).To(Equal(handoverRequeueInterval))
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionFalse))

		// All three: done.
		setReady(true)
		result = reconcileHandoverOK(r, cluster)
		Expect(result.RequeueAfter).To(BeZero())

		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionTrue))
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Reason).To(Equal("Complete"))
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Status).To(Equal(metav1.ConditionTrue))

		hash, err := CNIValuesHash(cluster, specMode(cluster), liveNodes())
		Expect(err).NotTo(HaveOccurred())
		Expect(getCluster(cluster).Status.CNI.AppliedValuesHash).To(Equal(hash))

		// Steady state: nothing more to do.
		result = reconcileHandoverOK(r, cluster)
		Expect(result).To(Equal(ctrl.Result{}))
	})

	It("runs the takeover Job for an adopted cluster and records its inline manifest only once handed over", func() {
		cluster := newTestCiliumCluster()
		cluster.Annotations = map[string]string{
			ichorv1alpha1.TalosClusterAdoptedAnnotation:           strconv.FormatBool(true),
			ichorv1alpha1.TalosClusterCNIInlineManifestAnnotation: "my-cilium",
		}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
		ciliumDaemonSet()(true)

		reconcileHandoverOK(r, cluster)

		job := cniJob(cluster)
		Expect(job.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--takeover"))
		Expect(getCluster(cluster).Status.CNI).To(BeNil(), "nothing is recorded before the hand-over completes")

		setJobCondition(job, batchv1.JobComplete, "")
		helmRelease(1, "deployed", effectiveValues(cluster))
		reconcileHandoverOK(r, cluster)

		got := getCluster(cluster)
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionTrue))
		Expect(got.Status.CNI.TakenOverInlineManifest).To(Equal("my-cilium"))
	})

	// `ichor adopt` installed Cilium itself, so Ichor's release is already deployed at
	// the effective values when the TalosCluster appears. Hand-over needs no Job and nothing to remove.
	It("hands over without a Job when Ichor's release is already deployed at the cluster's values", func() {
		cluster := newTestCiliumCluster()
		ciliumDaemonSet()(true)
		helmRelease(1, "deployed", effectiveValues(cluster))

		result := reconcileHandoverOK(r, cluster)
		Expect(result).To(Equal(ctrl.Result{}))

		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionTrue))
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Status).To(Equal(metav1.ConditionTrue))
		Expect(cniJobCount(cluster)).To(BeZero())
		Expect(getCluster(cluster).Status.CNI.TakenOverInlineManifest).To(BeEmpty())

		_, held := cniLockHolder(cluster)
		Expect(held).To(BeFalse())
	})

	It("refuses to change the tunnel of a running Cilium: TunnelMismatch, no Job", func() {
		cluster := newTestCiliumCluster()
		newCilium("false", true)

		var cm corev1.ConfigMap
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: ciliumConfigName}, &cm)).To(Succeed())
		cm.Data["tunnel-protocol"] = "geneve"
		Expect(k8sClient.Update(ctx, &cm)).To(Succeed())

		reconcileHandoverOK(r, cluster)

		c := conditionOf(cluster, render.ConditionCNIHandover)
		Expect(c.Status).To(Equal(metav1.ConditionFalse))
		Expect(c.Reason).To(Equal("TunnelMismatch"))
		Expect(c.Message).To(ContainSubstring("spec.cni.tunnel is vxlan but the running Cilium uses geneve"))
		Expect(cniJobCount(cluster)).To(BeZero())
	})

	It("still runs a Job when the deployed release is at other values", func() {
		cluster := newTestCiliumCluster()
		ciliumDaemonSet()(true)
		helmRelease(1, "deployed", map[string]any{"debug": map[string]any{"enabled": true}})

		reconcileHandoverOK(r, cluster)
		Expect(cniJobCount(cluster)).To(Equal(1))
		cniJob(cluster)
	})

	It("does not ask a cluster Ichor bootstrapped to take over anything", func() {
		cluster := newTestCiliumCluster()
		ciliumDaemonSet()(true)

		reconcileHandoverOK(r, cluster)
		Expect(cniJob(cluster).Spec.Template.Spec.Containers[0].Args).NotTo(ContainElement("--takeover"))
	})

	It("starts a new Job when the values change after the hand-over", func() {
		cluster := newTestCiliumCluster()
		ciliumDaemonSet()(true)

		reconcileHandoverOK(r, cluster)
		setJobCondition(cniJob(cluster), batchv1.JobComplete, "")
		helmRelease(1, "deployed", effectiveValues(cluster))
		reconcileHandoverOK(r, cluster)
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionTrue))

		first, err := CNIValuesHash(cluster, specMode(cluster), liveNodes())
		Expect(err).NotTo(HaveOccurred())

		// The user edits the values.
		edited := getCluster(cluster)
		edited.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{
			Values: &apiextensionsv1.JSON{Raw: []byte(`{"operator":{"replicas":2}}`)},
		}
		Expect(k8sClient.Update(ctx, edited)).To(Succeed())

		result := reconcileHandoverOK(r, cluster)
		Expect(result.RequeueAfter).To(Equal(cniJobPollInterval))

		second, err := CNIValuesHash(getCluster(cluster), specMode(cluster), liveNodes())
		Expect(err).NotTo(HaveOccurred())
		Expect(second).NotTo(Equal(first))

		job := cniJob(getCluster(cluster))
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
		reconcileHandoverOK(r, cluster)
		Expect(getCluster(cluster).Status.CNI.AppliedValuesHash).To(Equal(second))
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Status).To(Equal(metav1.ConditionTrue))
		_, held = cniLockHolder(cluster)
		Expect(held).To(BeFalse())
	})

	It("sets the condition with the Job's reason when it failed, and releases the lock", func() {
		cluster := newTestCiliumCluster()
		ciliumDaemonSet()(true)

		reconcileHandoverOK(r, cluster)

		job := cniJob(cluster)
		setJobCondition(job, batchv1.JobFailed, "Job has reached the specified backoff limit")

		result := reconcileHandoverOK(r, cluster)
		Expect(result.RequeueAfter).To(BeNumerically(">", 0), "a failed Job is retried, not waited on forever")

		cond := conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("JobFailed"))
		Expect(cond.Message).To(ContainSubstring("backoff limit"))
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionFalse))

		_, held := cniLockHolder(cluster)
		Expect(held).To(BeFalse())
	})

	It("replaces a failed Job with a new one once the retry interval has passed", func() {
		cluster := newTestCiliumCluster()
		ciliumDaemonSet()(true)
		r.JobRetryInterval = time.Nanosecond

		reconcileHandoverOK(r, cluster)

		failedJob := cniJob(cluster)
		setJobCondition(failedJob, batchv1.JobFailed, "another operation (install/upgrade/rollback) is in progress")

		// The failure is reported and the failed Job removed.
		reconcileHandoverOK(r, cluster)
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Reason).To(Equal("JobFailed"))

		// The next pass starts a new Job under the same name.
		Eventually(func(g Gomega) {
			_, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
			g.Expect(err).NotTo(HaveOccurred())

			var job batchv1.Job
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(failedJob), &job)).To(Succeed())
			g.Expect(job.UID).NotTo(Equal(failedJob.UID))
			_, failedCond := jobOutcome(&job)
			g.Expect(failedCond).To(BeNil())
		}).Should(Succeed())
	})

	// A Job that fails again with the same values waits twice as long each time (up to 30 minutes), and
	// the condition says how often it failed, so it does not hold the cluster lock in a near-continuous
	// loop.
	DescribeTable("waits longer after each failure of the same values",
		func(earlierFailures int32, wantWait time.Duration, wantMessage string) {
			cluster := newTestCiliumCluster()
			ciliumDaemonSet()(true)
			r.JobRetryInterval = time.Minute

			reconcileHandoverOK(r, cluster)

			hash, err := CNIValuesHash(cluster, specMode(cluster), liveNodes())
			Expect(err).NotTo(HaveOccurred())

			seeded := getCluster(cluster)
			seeded.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{JobFailures: earlierFailures, JobFailuresHash: hash}
			Expect(k8sClient.Status().Update(ctx, seeded)).To(Succeed())

			failJobJustNow(cniJob(cluster), "Job has reached the specified backoff limit")

			result := reconcileHandoverOK(r, cluster)
			Expect(result.RequeueAfter).To(BeNumerically("~", wantWait, 10*time.Second))
			Expect(cniJobCount(cluster)).To(Equal(1), "the failed Job stays until its wait is over")
			Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Message).To(ContainSubstring(wantMessage))
		},
		Entry("the third failure", int32(2), 4*time.Minute, "failed 3 times in a row"),
		Entry("the first failure", int32(0), time.Minute, "failed 1 time in a row"),
		Entry("a long streak stops at the cap", int32(9), 30*time.Minute, "failed 10 times in a row"),
	)

	It("counts the failures of one set of values, starts over for other values and clears them on success", func() {
		cluster := newTestCiliumCluster()
		ciliumDaemonSet()(true)
		r.JobRetryInterval = time.Nanosecond

		failures := func() int32 {
			if cni := getCluster(cluster).Status.CNI; cni != nil {
				return cni.JobFailures
			}

			return 0
		}

		reconcileHandoverOK(r, cluster)

		for want := int32(1); want <= 2; want++ {
			failJobJustNow(cniJob(cluster), "Job has reached the specified backoff limit")

			reconcileHandoverOK(r, cluster) // retires the failed Job
			Expect(failures()).To(Equal(want))

			reconcileHandoverOK(r, cluster) // starts the next one
		}

		// Other values are another Job at once, and the count starts again for them.
		edited := getCluster(cluster)
		edited.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{
			Values: &apiextensionsv1.JSON{Raw: []byte(`{"debug":{"enabled":true}}`)},
		}
		Expect(k8sClient.Update(ctx, edited)).To(Succeed())

		reconcileHandoverOK(r, cluster)
		failJobJustNow(cniJob(edited), "Job has reached the specified backoff limit")

		reconcileHandoverOK(r, cluster)
		Expect(failures()).To(Equal(int32(1)))

		// A Job that works clears the count.
		reconcileHandoverOK(r, cluster)
		setJobCondition(cniJob(edited), batchv1.JobComplete, "")
		helmRelease(1, "deployed", effectiveValues(getCluster(cluster)))

		reconcileHandoverOK(r, cluster)
		Expect(failures()).To(BeZero())
		Expect(getCluster(cluster).Status.CNI.JobFailuresHash).To(BeEmpty())
	})

	It("puts the Job's last log line in the failure condition", func() {
		cluster := newTestCiliumCluster()
		ciliumDaemonSet()(true)

		reconcileHandoverOK(r, cluster)

		job := cniJob(cluster)
		pod := &corev1.Pod{
			Namespace: cniJobNamespace, GenerateName: "cni-job-pod-", Labels: map[string]string{"job-name": job.Name},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "cni", Image: "x"}}},
		}
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, pod))).To(Succeed()) })

		// client-go's fake clientset answers every log request with "fake logs".
		r.Kube = kubefake.NewSimpleClientset()
		setJobCondition(job, batchv1.JobFailed, "Job has reached the specified backoff limit")

		reconcileHandoverOK(r, cluster)
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Message).To(ContainSubstring("fake logs"))
	})

	It("fails fast with the FailedCreate message when the Job's pod cannot be created, and releases the lock", func() {
		cluster := newTestCiliumCluster()
		ciliumDaemonSet()(true)

		reconcileHandoverOK(r, cluster)

		job := cniJob(cluster)
		_, held := cniLockHolder(cluster)
		Expect(held).To(BeTrue())

		const refusal = `pods "ichor-cni-x" is forbidden: violates PodSecurity "baseline:latest": host namespaces (hostNetwork=true)`

		event := &corev1.Event{
			Namespace: cniJobNamespace, GenerateName: "cni-job-event-",
			InvolvedObject: corev1.ObjectReference{Kind: "Job", Namespace: cniJobNamespace, Name: job.Name, APIVersion: "batch/v1"},
			Reason:         "FailedCreate", Message: refusal, Type: corev1.EventTypeWarning,
		}
		Expect(k8sClient.Create(ctx, event)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, event))).To(Succeed()) })

		// envtest runs no Job controller, so the Job has no pod; let the grace period be over.
		grace := cniPodGrace
		cniPodGrace = -time.Second
		DeferCleanup(func() { cniPodGrace = grace })

		result := reconcileHandoverOK(r, cluster)
		Expect(result.RequeueAfter).To(Equal(handoverRequeueInterval))

		cond := conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("JobFailed"))
		Expect(cond.Message).To(ContainSubstring("hostNetwork=true"))

		_, held = cniLockHolder(cluster)
		Expect(held).To(BeFalse())
		Expect(cniJobCount(cluster)).To(BeZero(), "the stuck Job is removed so the next pass starts a new one")
	})

	It("holds back while a node rollout has the cluster lock, and goes on when it ends", func() {
		cluster := newTestCiliumCluster()
		ciliumDaemonSet()(true)

		rollout := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
		Expect(rollout.Acquire(ctx, controllerIdentity)).To(Succeed())
		DeferCleanup(func() { Expect(rollout.Release(ctx, controllerIdentity)).To(Succeed()) })

		result := reconcileHandoverOK(r, cluster)
		Expect(result.RequeueAfter).To(Equal(handoverRequeueInterval))
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Reason).To(Equal("WaitingForLock"))

		Expect(cniJobCount(cluster)).To(BeZero())

		Expect(rollout.Release(ctx, controllerIdentity)).To(Succeed())

		result = reconcileHandoverOK(r, cluster)
		Expect(result.RequeueAfter).To(Equal(cniJobPollInterval))
		cniJob(cluster)
	})
})

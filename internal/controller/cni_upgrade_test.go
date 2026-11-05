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
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/mod/semver"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/render"
)

func TestCheckChartStep(t *testing.T) {
	for _, tc := range []struct {
		deployed, target string
		wantErr          string
	}{
		{olderMinor, chartNext, ""},
		{chartNext, "1.20.7", ""},
		{chartNext, chartNext, ""},
		{skipMinor, chartNext, "one minor version at a time"},
		{chartNext, "2.0.0", "one minor version at a time"},
		{"1.21.0", chartNext, "does not downgrade"},
		{"nonsense", chartNext, "cannot compare"},
	} {
		err := checkChartStep(tc.deployed, tc.target)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s -> %s: unexpected error %v", tc.deployed, tc.target, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s -> %s: error %v, want it to contain %q", tc.deployed, tc.target, err, tc.wantErr)
		}
	}
}

// An operation an earlier Ichor created for a Cilium patch is refused, not run as the embedded chart.
func TestUpgradeChartRefusesAnOperationThatNamesAPatch(t *testing.T) {
	op := &ichorv1alpha1.TalosOperation{Spec: ichorv1alpha1.TalosOperationSpec{CNI: &ichorv1alpha1.TalosOperationCNISpec{
		Action: ichorv1alpha1.TalosOperationCNIActionUpgrade, ChartVersion: cilium.ChartVersion, ImageVersion: "v1.20.99",
	}}}

	done, err := (&cniKubeProxyAdvancer{}).upgradeChart(t.Context(), &ichorv1alpha1.TalosCluster{}, op)
	if done || err == nil || !strings.Contains(err.Error(), "no longer runs") {
		t.Fatalf("done = %v, err = %v", done, err)
	}
}

const (
	// The chart is 1.20.x and the rule accepts the minor below it: olderMinor is 1.19, skipMinor 1.18.
	chartNext  = "1.20.4"
	olderMinor = "1.19.5"
	skipMinor  = "1.18.2"
)

var _ = Describe("Cilium chart upgrade", func() {
	var cluster *ichorv1alpha1.TalosCluster

	BeforeEach(func() {
		createInstallation("v0.2.0")
		cluster = newTestCiliumCluster()
		DeferCleanup(func() {
			Expect(k8sClient.DeleteAllOf(ctx, &batchv1.Job{}, client.InNamespace(cniJobNamespace),
				client.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())
		})
	})

	Describe("availability", func() {
		It("is set when the release's chart differs from the embedded one, and cleared when it matches", func() {
			ciliumDaemonSet()(true)

			hash, err := CNIValuesHash(cluster, specMode(cluster), liveNodes())
			Expect(err).NotTo(HaveOccurred())

			// Handed over already.
			cluster.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{AppliedValuesHash: hash}
			apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
				Type: render.ConditionCNIHandover, Status: metav1.ConditionTrue, Reason: reasonHandoverComplete,
			})
			Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

			r := &CNIHandoverReconciler{Client: k8sClient, APIReader: k8sClient, Namespace: testIchorNamespace}

			// No release record: nothing is claimed.
			reconcileHandoverOK(r, cluster)
			Expect(getCluster(cluster).Status.CNI.AvailableChartVersion).To(BeEmpty())
			Expect(apimeta.FindStatusCondition(getCluster(cluster).Status.Conditions, ichorv1alpha1.ConditionCiliumUpgradeAvailable)).To(BeNil())

			helmReleaseAt(1, "deployed", olderMinor, effectiveValues(cluster))
			reconcileHandoverOK(r, cluster)
			Expect(getCluster(cluster).Status.CNI.AvailableChartVersion).To(Equal(cilium.ChartVersion))
			Expect(getCluster(cluster).Status.CNI.AppliedValuesHash).To(Equal(hash), "the other CNI status fields stay")

			cond := conditionOf(cluster, ichorv1alpha1.ConditionCiliumUpgradeAvailable)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Message).To(And(ContainSubstring(cilium.ChartVersion), ContainSubstring(olderMinor)))

			// A release two minors behind is flagged as not applicable in one step.
			helmReleaseAt(2, "deployed", skipMinor, effectiveValues(cluster))
			reconcileHandoverOK(r, cluster)
			Expect(conditionOf(cluster, ichorv1alpha1.ConditionCiliumUpgradeAvailable).Message).To(ContainSubstring("one step"))

			// The release is upgraded.
			helmReleaseAt(3, "deployed", cilium.ChartVersion, effectiveValues(cluster))
			reconcileHandoverOK(r, cluster)
			Expect(getCluster(cluster).Status.CNI.AvailableChartVersion).To(BeEmpty())
			Expect(conditionOf(cluster, ichorv1alpha1.ConditionCiliumUpgradeAvailable).Status).To(Equal(metav1.ConditionFalse))
		})
	})

	Describe("a Cilium patch an earlier Ichor recorded", func() {
		// recorded hands the cluster over with the patch in status.cni.imageOverride and the values hash applied
		// with it, as an earlier Ichor left it, and returns the hash.
		recorded := func(version string) string {
			GinkgoHelper()

			ciliumDaemonSet()(true)

			cluster.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{
				ImageOverride: &ichorv1alpha1.TalosCiliumImageOverride{
					Version: version,
					Images:  map[string]string{"agent": "quay.io/cilium/cilium:" + version + "@sha256:" + strings.Repeat("1", 64)},
				},
			}

			hash, err := CNIValuesHash(cluster, specMode(cluster), liveNodes())
			Expect(err).NotTo(HaveOccurred())

			cluster.Status.CNI.AppliedValuesHash = hash
			apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
				Type: render.ConditionCNIHandover, Status: metav1.ConditionTrue, Reason: reasonHandoverComplete,
			})
			Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

			return hash
		}

		It("stays while the embedded chart is behind it: the values and the hash do not move, and the condition says so", func() {
			// A patch newer than any this chart reaches.
			patch := "v" + semver.MajorMinor("v" + cilium.ChartVersion)[1:] + ".999"
			hash := recorded(patch)
			helmReleaseAt(1, "deployed", cilium.ChartVersion, effectiveValues(cluster))

			r := &CNIHandoverReconciler{Client: k8sClient, APIReader: k8sClient, Namespace: testIchorNamespace}

			reconcileHandoverOK(r, cluster)

			got := getCluster(cluster).Status.CNI
			Expect(got.ImageOverride).NotTo(BeNil(), "the patch is frozen, not dropped")
			Expect(got.ImageOverride.Version).To(Equal(patch))
			Expect(got.AppliedValuesHash).To(Equal(hash), "no step back, so no Job")

			var jobs batchv1.JobList
			Expect(k8sClient.List(ctx, &jobs, client.InNamespace(cniJobNamespace))).To(Succeed())
			Expect(jobs.Items).To(BeEmpty())

			// CiliumUpgradeAvailable, not CNIValuesApplied: a Job's progress rewrites the latter.
			cond := conditionOf(cluster, ichorv1alpha1.ConditionCiliumUpgradeAvailable)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Message).To(And(
				ContainSubstring(strings.TrimPrefix(patch, "v")), ContainSubstring(cilium.ChartVersion), ContainSubstring("earlier Ichor"),
				ContainSubstring("stays")))
		})

		It("is dropped once the embedded chart reaches it, with no step back and no mention in the condition", func() {
			recorded("v" + cilium.ChartVersion)
			helmReleaseAt(1, "deployed", cilium.ChartVersion, effectiveValues(cluster))

			r := &CNIHandoverReconciler{Client: k8sClient, APIReader: k8sClient, Namespace: testIchorNamespace}

			reconcileHandoverOK(r, cluster)
			Expect(getCluster(cluster).Status.CNI.ImageOverride).To(BeNil())

			// The patch was cleared in this pass; the next one computes the availability.
			reconcileHandoverOK(r, cluster)
			Expect(conditionOf(cluster, ichorv1alpha1.ConditionCiliumUpgradeAvailable).Message).NotTo(ContainSubstring("earlier Ichor"))
		})
	})

	Describe("operation", func() {
		var (
			setReady func(bool)
			op       *ichorv1alpha1.TalosOperation
			r        *TalosOperationReconciler
		)

		jobFor := func(op *ichorv1alpha1.TalosOperation) *batchv1.Job {
			GinkgoHelper()

			hash, err := CNIValuesHash(cluster, specMode(cluster), liveNodes())
			Expect(err).NotTo(HaveOccurred())

			var job batchv1.Job
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: cniJobNamespace, Name: cniHelmJobName(cluster, hash+"/upgrade/"+string(op.UID)),
			}, &job)).To(Succeed())

			return &job
		}

		start := func(chartVersion string) {
			newOpTestMachine(cluster.Name, roleControlPlane, "cni-up-cp")

			op = &ichorv1alpha1.TalosOperation{
				GenerateName: "cni-", Namespace: tmNamespace,
				Spec: ichorv1alpha1.TalosOperationSpec{
					ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Kind: ichorv1alpha1.TalosOperationKindCNI,
					CNI: &ichorv1alpha1.TalosOperationCNISpec{Action: ichorv1alpha1.TalosOperationCNIActionUpgrade, ChartVersion: chartVersion},
				},
			}
			Expect(k8sClient.Create(ctx, op)).To(Succeed())
		}

		BeforeEach(func() {
			setReady = ciliumDaemonSet()
			setReady(true)

			adv := &cniKubeProxyAdvancer{Client: k8sClient, Reader: k8sClient, ControllerNamespace: testIchorNamespace}
			r = &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: adv.Advance}
		})

		It("runs the Job, verifies the release and the rollout, and completes", func() {
			helmReleaseAt(1, "deployed", olderMinor, effectiveValues(cluster))
			start(cilium.ChartVersion)

			// A Job from the hand-over for the same values must not count as this upgrade's.
			finishCNIJobAt(cluster)

			_, err := reconcileOperation(r, op)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchOperation(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
			Expect(fetchOperation(op).Status.Machines).To(BeEmpty())

			op = fetchOperation(op)
			job := jobFor(op)
			Expect(job.Annotations).To(HaveKey(CNIValuesHashAnnotation))

			_, held := cniLockHolder(cluster)
			Expect(held).To(BeTrue())

			// The Job finishes; the release is still at the old chart, so the operation keeps waiting.
			setJobCondition(job, batchv1.JobComplete, "")
			_, err = reconcileOperation(r, op)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchOperation(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))

			// Deployed at the new chart, but the agents have not rolled out.
			helmReleaseAt(2, "deployed", cilium.ChartVersion, effectiveValues(cluster))
			setReady(false)
			_, err = reconcileOperation(r, op)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchOperation(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))

			setReady(true)
			_, err = reconcileOperation(r, op)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchOperation(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))

			_, held = cniLockHolder(cluster)
			Expect(held).To(BeFalse())
		})

		It("pauses with the Job's reason when the Job fails", func() {
			helmReleaseAt(1, "deployed", olderMinor, effectiveValues(cluster))
			start(cilium.ChartVersion)

			_, err := reconcileOperation(r, op)
			Expect(err).NotTo(HaveOccurred())

			op = fetchOperation(op)
			setJobCondition(jobFor(op), batchv1.JobFailed, "rollout did not become ready")

			_, err = reconcileOperation(r, op)
			Expect(err).NotTo(HaveOccurred())

			got := fetchOperation(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("cilium Helm Job"))
			Expect(got.Status.FailureReason).To(ContainSubstring("rollout did not become ready"))
		})

		It("refuses a skipped minor version and starts no Job", func() {
			helmReleaseAt(1, "deployed", skipMinor, effectiveValues(cluster))
			start(cilium.ChartVersion)

			_, err := reconcileOperation(r, op)
			Expect(err).NotTo(HaveOccurred())

			got := fetchOperation(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("one minor version at a time"))
			Expect(cniJobCount(cluster)).To(BeZero())
		})

		It("refuses an operation for another chart than the one embedded", func() {
			helmReleaseAt(1, "deployed", olderMinor, effectiveValues(cluster))
			start("9.9.9")

			_, err := reconcileOperation(r, op)
			Expect(err).NotTo(HaveOccurred())

			got := fetchOperation(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("embeds " + cilium.ChartVersion))
			Expect(cniJobCount(cluster)).To(BeZero())
		})
	})
})

// finishCNIJobAt creates the Job AdvanceCNIHelmJob makes for cluster's values (the hand-over's) and completes it.
func finishCNIJobAt(cluster *ichorv1alpha1.TalosCluster) {
	GinkgoHelper()

	var inst ichorv1alpha1.IchorInstallation
	Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ichorv1alpha1.IchorInstallationName}, &inst)).To(Succeed())

	_, err := AdvanceCNIHelmJob(ctx, k8sClient, testIchorNamespace, cluster, specMode(cluster), liveNodes(), &inst, "", time.Time{})
	Expect(err).NotTo(HaveOccurred())

	hash, err := CNIValuesHash(cluster, specMode(cluster), liveNodes())
	Expect(err).NotTo(HaveOccurred())

	var job batchv1.Job
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: cniJobNamespace, Name: cniHelmJobName(cluster, hash)}, &job)).To(Succeed())
	setJobCondition(&job, batchv1.JobComplete, "")
}

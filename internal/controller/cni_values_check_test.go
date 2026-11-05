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
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/render"
)

// The controller re-checks the Cilium values, with the facts the UI uses, before it
// starts a Helm Job.
var _ = Describe("The Cilium values re-check before a Helm Job", func() {
	var r *CNIHandoverReconciler

	masquerade := jsonMap(`{"bpf":{"masquerade":true}}`)
	debug := jsonMap(`{"debug":{"enabled":true}}`)
	gateway := jsonMap(`{"gatewayAPI":{"enabled":true}}`)

	// newValuesCluster is a cilium cluster with a real secrets bundle and one approved control plane, so the
	// controller can render the machine configuration the guards read hostDNS from.
	newValuesCluster := func(user map[string]any) *ichorv1alpha1.TalosCluster {
		GinkgoHelper()

		secret := newTestSecret(newTestBundle(testK8sContract))
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).To(Succeed()) })

		spec := newClusterSpec(secret.Name, testK8sContract)
		spec.CNI = ichorv1alpha1.TalosClusterCNI{Preset: ciliumName, Cilium: &ichorv1alpha1.TalosClusterCNICilium{Values: valuesJSON(user)}}

		cluster := &ichorv1alpha1.TalosCluster{GenerateName: testClusterGenerate, Namespace: tmNamespace, Spec: spec}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))).To(Succeed()) })

		m := newOpTestMachine(cluster.Name, roleControlPlane, "values-cp-"+cluster.Name)
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, m))).To(Succeed()) })

		return cluster
	}

	setValues := func(cluster *ichorv1alpha1.TalosCluster, user map[string]any) {
		GinkgoHelper()

		got := getCluster(cluster)
		got.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{Values: valuesJSON(user)}
		Expect(k8sClient.Update(ctx, got)).To(Succeed())
	}

	BeforeEach(func() {
		createInstallation("v0.2.0")
		DeferCleanup(func() {
			Expect(k8sClient.DeleteAllOf(ctx, &batchv1.Job{}, client.InNamespace(cniJobNamespace),
				client.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())
		})

		r = &CNIHandoverReconciler{Client: k8sClient, APIReader: k8sClient, Namespace: testIchorNamespace}
	})

	It("starts no Job for bpf.masquerade with forwardKubeDNSToHost, reports InvalidValues, and runs once the values are fixed", func() {
		cluster := newValuesCluster(masquerade)
		ciliumDaemonSet()(true)

		result := reconcileHandoverOK(r, cluster)
		Expect(result.RequeueAfter).To(Equal(handoverRequeueInterval))
		Expect(cniJobCount(cluster)).To(BeZero())

		cond := conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonCNIInvalidValues))
		Expect(cond.Message).To(ContainSubstring("forwardKubeDNSToHost"))

		_, held := cniLockHolder(cluster)
		Expect(held).To(BeFalse(), "a refused Job holds no cluster lock")

		// The controller never edits the spec.
		Expect(string(getCluster(cluster).Spec.CNI.Cilium.Values.Raw)).To(MatchJSON(`{"bpf":{"masquerade":true}}`))

		// The same values stay refused on the next pass.
		reconcileHandoverOK(r, cluster)
		Expect(cniJobCount(cluster)).To(BeZero())

		// A later valid edit proceeds.
		setValues(cluster, debug)
		result = reconcileHandoverOK(r, cluster)
		Expect(result.RequeueAfter).To(Equal(cniJobPollInterval))
		Expect(cniJobCount(cluster)).To(Equal(1))
	})

	It("starts no Job for values patched in after the hand-over, and leaves CNIHandover True", func() {
		cluster := newValuesCluster(nil)
		ciliumDaemonSet()(true)
		helmRelease(1, "deployed", effectiveValues(cluster))

		reconcileHandoverOK(r, cluster)
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionTrue))

		setValues(cluster, masquerade)
		reconcileHandoverOK(r, cluster)

		Expect(cniJobCount(cluster)).To(BeZero())
		Expect(conditionOf(cluster, render.ConditionCNIHandover).Status).To(Equal(metav1.ConditionTrue))

		cond := conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonCNIInvalidValues))
		Expect(cond.Message).To(ContainSubstring("forwardKubeDNSToHost"))
	})

	// Found on hardware (lane H): reverting a refused or failed edit to exactly the applied values left
	// CNIValuesApplied False for good, although Cilium runs those values.
	It("reports the values applied again when a refused edit is reverted to the applied values, and starts no Job", func() {
		cluster := newValuesCluster(nil)
		ciliumDaemonSet()(true)
		helmRelease(1, "deployed", effectiveValues(cluster))

		reconcileHandoverOK(r, cluster)
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Status).To(Equal(metav1.ConditionTrue))

		setValues(cluster, masquerade)
		reconcileHandoverOK(r, cluster)
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Reason).To(Equal(reasonCNIInvalidValues))

		setValues(cluster, nil)
		result := reconcileHandoverOK(r, cluster)
		Expect(result).To(Equal(ctrl.Result{}))

		cond := conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(reasonCNIApplied))
		Expect(cniJobCount(cluster)).To(BeZero())
	})

	It("reports the values applied again and clears the failure count when a failed edit is reverted to the applied values", func() {
		cluster := newValuesCluster(nil)
		ciliumDaemonSet()(true)
		helmRelease(1, "deployed", effectiveValues(cluster))
		r.JobRetryInterval = time.Nanosecond

		reconcileHandoverOK(r, cluster)

		setValues(cluster, debug)
		reconcileHandoverOK(r, cluster)
		failJobJustNow(cniJob(getCluster(cluster)), "pod ichor-cni was OOMKilled")

		reconcileHandoverOK(r, cluster) // retires the failed Job and counts it
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Reason).To(Equal("JobFailed"))
		Expect(getCluster(cluster).Status.CNI.JobFailures).To(Equal(int32(1)))

		setValues(cluster, nil)
		reconcileHandoverOK(r, cluster)

		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Status).To(Equal(metav1.ConditionTrue))
		Expect(getCluster(cluster).Status.CNI.JobFailures).To(BeZero())
		Expect(getCluster(cluster).Status.CNI.JobFailuresHash).To(BeEmpty())
	})

	It("does not touch valid values on a cluster with a control plane", func() {
		cluster := newValuesCluster(debug)
		ciliumDaemonSet()(true)

		result := reconcileHandoverOK(r, cluster)
		Expect(result.RequeueAfter).To(Equal(cniJobPollInterval))
		Expect(cniJobCount(cluster)).To(Equal(1))
	})

	It("starts no Job for gatewayAPI.enabled while the Gateway API CRDs are missing", func() {
		cluster := newValuesCluster(gateway)
		ciliumDaemonSet()(true)

		reconcileHandoverOK(r, cluster)

		Expect(cniJobCount(cluster)).To(BeZero())
		Expect(conditionOf(cluster, ichorv1alpha1.ConditionCNIValuesApplied).Message).To(ContainSubstring("Gateway API"))
	})

	It("pauses a chart upgrade operation instead of starting its Job", func() {
		cluster := newValuesCluster(gateway)
		ciliumDaemonSet()(true)
		helmReleaseAt(1, "deployed", olderMinor, effectiveValues(cluster))

		op := &ichorv1alpha1.TalosOperation{
			GenerateName: "cni-", Namespace: tmNamespace,
			Spec: ichorv1alpha1.TalosOperationSpec{
				ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Kind: ichorv1alpha1.TalosOperationKindCNI,
				CNI: &ichorv1alpha1.TalosOperationCNISpec{Action: ichorv1alpha1.TalosOperationCNIActionUpgrade, ChartVersion: cilium.ChartVersion},
			},
		}
		Expect(k8sClient.Create(ctx, op)).To(Succeed())

		adv := &cniKubeProxyAdvancer{Client: k8sClient, Reader: k8sClient, ControllerNamespace: testIchorNamespace}
		or := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: adv.Advance}

		mustReconcileOperation(or, op)

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).To(ContainSubstring("Gateway API"))
		Expect(cniJobCount(cluster)).To(BeZero())
	})
})

// A check that refuses stops the Job before it exists; an existing Job is not re-checked.
func TestAdvanceCNIHelmJobRunsTheCheckOnlyBeforeCreatingTheJob(t *testing.T) {
	c := cniFakeClient(t)
	cluster := cniTestCluster("")
	problem := errors.New("bpf.masquerade=true needs forwardKubeDNSToHost=false")
	advance := func(check func(context.Context) error) (bool, error) {
		return AdvanceCNIHelmJob(t.Context(), c, "ichor-system", cluster, specMode(cluster), 0, cniTestInstallation(), check, "", time.Time{})
	}

	_, err := advance(func(context.Context) error { return problem })

	var invalid *CNIInvalidValuesError
	if !errors.As(err, &invalid) || !errors.Is(err, problem) || !strings.Contains(err.Error(), "forwardKubeDNSToHost") {
		t.Fatalf("error = %v, want a *CNIInvalidValuesError carrying the problem", err)
	}

	var jobs batchv1.JobList
	if err := c.List(t.Context(), &jobs); err != nil || len(jobs.Items) != 0 {
		t.Fatalf("%d Jobs (%v), want none", len(jobs.Items), err)
	}

	if _, err := advance(func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}

	// The Job exists now: a check that would refuse is not asked.
	if _, err := advance(func(context.Context) error { return problem }); err != nil {
		t.Fatalf("existing Job: %v", err)
	}
}

func jsonMap(text string) map[string]any {
	var m map[string]any

	Expect(json.Unmarshal([]byte(text), &m)).To(Succeed())

	return m
}

func valuesJSON(user map[string]any) *apiextensionsv1.JSON {
	if user == nil {
		return nil
	}

	raw, err := json.Marshal(user)
	Expect(err).NotTo(HaveOccurred())

	return &apiextensionsv1.JSON{Raw: raw}
}

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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
)

// An Ichor self-upgrade is complete only when the installer Job has succeeded and the controller
// and ichor-ui Deployments have finished rolling out (ADR 0059, amendment). The new controller
// resumes the operation as soon as it runs, while the Job is still post-checking and ichor-ui is
// still rolling; it must not report Completed then.
const targetVersion = "v0.2.0"

var _ = Describe("kind: ichor executor, completion", func() {
	const ns = testIchorNamespace

	var (
		advance AdvanceMachineFunc
		op      *ichorv1alpha1.TalosOperation
	)

	// deployment creates a one-replica Deployment; its status says whether the rollout is finished.
	deployment := func(name string, labels map[string]string, rolledOut bool) *appsv1.Deployment {
		d := &appsv1.Deployment{
			Name: name, Namespace: ns, Labels: labels,
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"rollout-test": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"rollout-test": name}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "registry.example/c:v1"}}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, d)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, d))).To(Succeed()) })

		setRollout(d, rolledOut)

		return d
	}

	// The Job is created by the first call, as in production, while the old controller runs.
	startJob := func() batchv1.Job {
		GinkgoHelper()

		withVersion("v0.1.0")
		createInstallation(targetVersion)

		done, err := advance(ctx, nil, nil, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())

		var l batchv1.JobList
		Expect(k8sClient.List(ctx, &l, client.InNamespace(ns),
			client.MatchingLabels{nameLabel: IchorInstallerServiceAccount})).To(Succeed())
		Expect(l.Items).To(HaveLen(1))

		DeferCleanup(func() {
			Expect(k8sClient.DeleteAllOf(ctx, &batchv1.Job{}, client.InNamespace(ns),
				client.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())
		})

		// The installer has replaced the controller: the new one resumes the operation.
		version.Version = targetVersion

		return l.Items[0]
	}

	controllerLabels := map[string]string{controlPlaneLabel: valueControllerMgr}
	uiLabels := map[string]string{"app.kubernetes.io/component": "ui"}

	BeforeEach(func() {
		advance = NewIchorUpgradeAdvancer(k8sClient, k8sClient, ns, false)
		op = &ichorv1alpha1.TalosOperation{Namespace: "ns", Name: "upgrade-rollout"}
	})

	It("is not done while the installer Job is still running, though the controller already runs the target", func() {
		startJob()
		deployment("rollout-controller", controllerLabels, true)
		deployment("rollout-ui", uiLabels, true)

		done, err := advance(ctx, nil, nil, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
	})

	It("is not done while ichor-ui is still rolling out, though the Job has succeeded", func() {
		job := startJob()
		deployment("rollout-controller", controllerLabels, true)
		ui := deployment("rollout-ui", uiLabels, false)
		setJobCondition(&job, batchv1.JobComplete, "")

		done, err := advance(ctx, nil, nil, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())

		setRollout(ui, true)

		done, err = advance(ctx, nil, nil, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
	})

	It("is not done while the controller Deployment is still rolling out", func() {
		job := startJob()
		deployment("rollout-controller", controllerLabels, false)
		deployment("rollout-ui", uiLabels, true)
		setJobCondition(&job, batchv1.JobComplete, "")

		done, err := advance(ctx, nil, nil, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
	})

	It("is not done while a Deployment has not observed its latest generation", func() {
		job := startJob()
		deployment("rollout-controller", controllerLabels, true)
		ui := deployment("rollout-ui", uiLabels, true)
		setJobCondition(&job, batchv1.JobComplete, "")

		ui.Status.ObservedGeneration = ui.Generation - 1
		Expect(k8sClient.Status().Update(ctx, ui)).To(Succeed())

		done, err := advance(ctx, nil, nil, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
	})

	It("says which rollout it is waiting for, for the operation's status", func() {
		job := startJob()
		controller := deployment("rollout-controller", controllerLabels, true)
		ui := deployment("rollout-ui", uiLabels, true)

		waiting := func() string {
			GinkgoHelper()

			w, err := ichorWaitingFor(ctx, k8sClient, k8sClient, ns, op)
			Expect(err).NotTo(HaveOccurred())

			return w
		}

		Expect(waiting()).To(Equal("the installer Job is running"))

		setJobCondition(&job, batchv1.JobComplete, "")
		Expect(waiting()).To(Equal("finishing the upgrade"))

		setRollout(ui, false)
		Expect(waiting()).To(Equal("waiting for the ichor-ui Deployment to finish rolling out (0 of 1 updated replicas are available)"))

		setRollout(ui, true)
		setRollout(controller, false)
		Expect(waiting()).To(Equal("waiting for the controller Deployment to finish rolling out (0 of 1 updated replicas are available)"))
	})

	It("is done once the Job has succeeded and both Deployments are rolled out", func() {
		job := startJob()
		deployment("rollout-controller", controllerLabels, true)
		deployment("rollout-ui", uiLabels, true)
		setJobCondition(&job, batchv1.JobComplete, "")

		done, err := advance(ctx, nil, nil, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
	})

	It("fails with an actionable message when the Job failed, even once the controller runs the target", func() {
		job := startJob()
		deployment("rollout-controller", controllerLabels, true)
		deployment("rollout-ui", uiLabels, true)
		setJobCondition(&job, batchv1.JobFailed, "post-check failed")

		_, err := advance(ctx, nil, nil, op)
		Expect(err).To(MatchError(ContainSubstring("installer Job")))
		Expect(err).To(MatchError(ContainSubstring("pod log")))
	})
})

// setRollout writes the status the Deployment controller would (envtest runs none): finished, or
// a rollout that has made no progress.
func setRollout(d *appsv1.Deployment, rolledOut bool) {
	GinkgoHelper()

	d.Status = appsv1.DeploymentStatus{ObservedGeneration: d.Generation}
	if rolledOut {
		d.Status.Replicas, d.Status.UpdatedReplicas = 1, 1
		d.Status.ReadyReplicas, d.Status.AvailableReplicas = 1, 1
	} else {
		d.Status.Replicas = 1
		d.Status.UnavailableReplicas = 1
	}

	Expect(k8sClient.Status().Update(ctx, d)).To(Succeed())
}

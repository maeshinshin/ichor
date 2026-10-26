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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
	"github.com/maeshinshin/ichor/pkg/gitopstracking"
)

const testIchorNamespace = "default"

// withVersion runs a spec with the controller's own version set, restoring it afterwards.
func withVersion(v string) {
	old := version.Version
	version.Version = v

	DeferCleanup(func() { version.Version = old })
}

func createInstallation(mode, ver string) *ichorv1alpha1.IchorInstallation {
	inst := &ichorv1alpha1.IchorInstallation{
		ObjectMeta: metav1.ObjectMeta{Name: ichorv1alpha1.IchorInstallationName},
		Spec: ichorv1alpha1.IchorInstallationSpec{
			ManagementMode: mode, Version: ver, InstallerRepository: "registry.example/ichor-installer",
		},
	}
	Expect(k8sClient.Create(ctx, inst)).To(Succeed())
	DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, inst))).To(Succeed()) })

	return inst
}

var _ = Describe("IchorInstallation", func() {
	It("is a singleton named ichor", func() {
		bad := &ichorv1alpha1.IchorInstallation{
			ObjectMeta: metav1.ObjectMeta{Name: "second"},
			Spec:       ichorv1alpha1.IchorInstallationSpec{Version: "v0.1.0"},
		}
		Expect(k8sClient.Create(ctx, bad)).To(MatchError(ContainSubstring("must be named ichor")))
	})

	It("defaults to ichor mode and rejects a version that is not semver", func() {
		inst := createInstallation("", "v0.1.0")
		Expect(inst.Spec.ManagementMode).To(Equal(ichorv1alpha1.IchorManagementModeIchor))

		inst.Spec.Version = "0.1"
		Expect(k8sClient.Update(ctx, inst)).NotTo(Succeed())
	})

	Context("controller", func() {
		reconcileOnce := func(r *IchorInstallationReconciler) *ichorv1alpha1.IchorInstallation {
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}})
			Expect(err).NotTo(HaveOccurred())

			var got ichorv1alpha1.IchorInstallation
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &got)).To(Succeed())

			return &got
		}

		newReconciler := func() *IchorInstallationReconciler {
			return &IchorInstallationReconciler{
				Client: k8sClient, Direct: k8sClient, ControllerNamespace: testIchorNamespace, ArgoCDNamespace: "argocd-absent",
			}
		}

		It("reports the running version and whether it matches spec.version", func() {
			withVersion("v0.1.0")
			createInstallation(ichorv1alpha1.IchorManagementModeIchor, "v0.2.0")

			got := reconcileOnce(newReconciler())
			Expect(got.Status.InstalledVersion).To(Equal("v0.1.0"))
			Expect(apimeta.IsStatusConditionFalse(got.Status.Conditions, conditionUpToDate)).To(BeTrue())
			Expect(apimeta.IsStatusConditionFalse(got.Status.Conditions, conditionExternalSuggested)).To(BeTrue())
		})

		It("suggests external when its own Deployment is tracked by Argo CD", func() {
			withVersion("v0.1.0")
			createInstallation(ichorv1alpha1.IchorManagementModeIchor, "v0.1.0")

			dep := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testIchorNamespace, Name: controllerDeploymentName,
					Annotations: map[string]string{gitopstracking.ArgoTrackingAnnotation: "ichor:apps/Deployment:default/x"},
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"a": "b"}},
						Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "m", Image: "x"}}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, dep)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, dep)).To(Succeed()) })

			// argocd-cm is what says the tracking method; without it nothing is "tracked".
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: testIchorNamespace, Name: gitopstracking.ArgoConfigMapName}}
			Expect(k8sClient.Create(ctx, cm)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, cm)).To(Succeed()) })

			r := newReconciler()
			r.ArgoCDNamespace = testIchorNamespace
			got := reconcileOnce(r)
			Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, conditionUpToDate)).To(BeTrue())
			Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, conditionExternalSuggested)).To(BeTrue())
		})
	})

	Context("kind: ichor executor", func() {
		const jobNs = testIchorNamespace

		op := func() *ichorv1alpha1.TalosOperation {
			return &ichorv1alpha1.TalosOperation{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "upgrade"}}
		}

		jobs := func() []batchv1.Job {
			var l batchv1.JobList
			Expect(k8sClient.List(ctx, &l, client.InNamespace(jobNs))).To(Succeed())

			return l.Items
		}

		AfterEach(func() {
			Expect(k8sClient.DeleteAllOf(ctx, &batchv1.Job{}, client.InNamespace(jobNs),
				client.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())
		})

		It("launches the installer Job for spec.version once, and reports its progress", func() {
			withVersion("v0.1.0")
			createInstallation(ichorv1alpha1.IchorManagementModeIchor, "v0.2.0")
			advance := NewIchorUpgradeAdvancer(k8sClient, jobNs)
			o := op()

			done, err := advance(ctx, nil, nil, o)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			Expect(jobs()).To(HaveLen(1))

			job := jobs()[0]
			Expect(job.Spec.Template.Spec.Containers[0].Image).To(Equal("registry.example/ichor-installer:v0.2.0"))
			Expect(job.Spec.Template.Spec.ServiceAccountName).To(Equal(IchorInstallerServiceAccount))

			// Called again (the next machine, the next reconcile): same Job, still running.
			done, err = advance(ctx, nil, nil, o)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			Expect(jobs()).To(HaveLen(1))

			setJobCondition(&job, batchv1.JobComplete, "")
			done, err = advance(ctx, nil, nil, o)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
		})

		It("turns a failed Job into an error", func() {
			withVersion("v0.1.0")
			createInstallation(ichorv1alpha1.IchorManagementModeIchor, "v0.2.0")
			advance := NewIchorUpgradeAdvancer(k8sClient, jobNs)
			o := op()

			_, err := advance(ctx, nil, nil, o)
			Expect(err).NotTo(HaveOccurred())

			job := jobs()[0]
			setJobCondition(&job, batchv1.JobFailed, "post-check failed")

			_, err = advance(ctx, nil, nil, o)
			Expect(err).To(MatchError(ContainSubstring("rolls back")))
		})

		It("launches no Job in external mode", func() {
			withVersion("v0.1.0")
			createInstallation(ichorv1alpha1.IchorManagementModeExternal, "v0.2.0")

			_, err := NewIchorUpgradeAdvancer(k8sClient, jobNs)(ctx, nil, nil, op())
			Expect(err).To(MatchError(ContainSubstring("pull request")))
			Expect(jobs()).To(BeEmpty())
		})

		It("refuses a downgrade and launches no Job", func() {
			withVersion("v0.3.0")
			createInstallation(ichorv1alpha1.IchorManagementModeIchor, "v0.2.0")

			_, err := NewIchorUpgradeAdvancer(k8sClient, jobNs)(ctx, nil, nil, op())
			Expect(err).To(MatchError(ContainSubstring("does not downgrade")))
			Expect(jobs()).To(BeEmpty())
		})

		It("is done, with no Job, once the running version is the target", func() {
			withVersion("v0.2.0")
			createInstallation(ichorv1alpha1.IchorManagementModeIchor, "v0.2.0")

			done, err := NewIchorUpgradeAdvancer(k8sClient, jobNs)(ctx, nil, nil, op())
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
			Expect(jobs()).To(BeEmpty())
		})
	})
})

// setJobCondition marks job finished the way the Job controller would (envtest runs none).
func setJobCondition(job *batchv1.Job, condType batchv1.JobConditionType, msg string) {
	GinkgoHelper()

	now := metav1.Now()
	job.Status.StartTime = &now
	job.Status.Conditions = append(job.Status.Conditions,
		batchv1.JobCondition{Type: batchv1.JobSuccessCriteriaMet, Status: corev1.ConditionTrue},
		batchv1.JobCondition{Type: condType, Status: corev1.ConditionTrue, Message: msg},
	)

	if condType == batchv1.JobFailed {
		job.Status.Failed = 1
		job.Status.Conditions[0] = batchv1.JobCondition{Type: batchv1.JobFailureTarget, Status: corev1.ConditionTrue}
	} else {
		job.Status.Succeeded = 1
		job.Status.CompletionTime = &now
	}

	Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())
}

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

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
)

const testIchorNamespace = "default"

// withVersion runs a spec with the controller's own version set, restoring it afterwards.
func withVersion(v string) {
	old := version.Version
	version.Version = v

	DeferCleanup(func() { version.Version = old })
}

func createInstallation(ver string) *ichorv1alpha1.IchorInstallation {
	inst := &ichorv1alpha1.IchorInstallation{
		Name: ichorv1alpha1.IchorInstallationName,
		Spec: ichorv1alpha1.IchorInstallationSpec{
			Version: ver, InstallerRepository: testInstallerRepo,
		},
	}
	Expect(k8sClient.Create(ctx, inst)).To(Succeed())
	DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, inst))).To(Succeed()) })

	return inst
}

var _ = Describe("IchorInstallation", func() {
	It("is a singleton named ichor", func() {
		bad := &ichorv1alpha1.IchorInstallation{
			Name: "second",
			Spec: ichorv1alpha1.IchorInstallationSpec{Version: "v0.1.0"},
		}
		Expect(k8sClient.Create(ctx, bad)).To(MatchError(ContainSubstring("must be named ichor")))
	})

	It("rejects a version that is not semver", func() {
		inst := createInstallation("v0.1.0")

		inst.Spec.Version = "0.1"
		Expect(k8sClient.Update(ctx, inst)).NotTo(Succeed())
	})

	Context("controller", func() {
		reconcileOnce := func(r *IchorInstallationReconciler) *ichorv1alpha1.IchorInstallation {
			_, err := r.Reconcile(ctx, reconcile.Request{Name: ichorv1alpha1.IchorInstallationName})
			Expect(err).NotTo(HaveOccurred())

			var got ichorv1alpha1.IchorInstallation
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &got)).To(Succeed())

			return &got
		}

		newReconciler := func() *IchorInstallationReconciler {
			return &IchorInstallationReconciler{
				Client: k8sClient,
			}
		}

		It("reports the running version and whether it matches spec.version", func() {
			withVersion("v0.1.0")
			createInstallation("v0.2.0")

			got := reconcileOnce(newReconciler())
			Expect(got.Status.InstalledVersion).To(Equal("v0.1.0"))
			Expect(apimeta.IsStatusConditionFalse(got.Status.Conditions, conditionUpToDate)).To(BeTrue())
			Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionExternalSuggested)).To(BeNil())
		})

		It("seeds an absent singleton with its version and --deployed-by, and never touches an existing one", func() {
			withVersion("v0.1.0")
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &ichorv1alpha1.IchorInstallation{Name: ichorv1alpha1.IchorInstallationName}))).To(Succeed())
			})

			key := types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}
			r := newReconciler()
			r.DeployedBy = ichorv1alpha1.IchorManagerHelm
			Expect(r.Seed(ctx)).To(Succeed())

			var got ichorv1alpha1.IchorInstallation
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
			Expect(got.Spec.Version).To(Equal("v0.1.0"))
			Expect(got.HelmManaged()).To(BeTrue())

			// A later seed by a controller of another version and manager changes nothing.
			withVersion("v0.9.0")
			r.DeployedBy = ichorv1alpha1.IchorManagerInstaller
			Expect(r.Seed(ctx)).To(Succeed())
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
			Expect(got.Spec.Version).To(Equal("v0.1.0"))
			Expect(got.HelmManaged()).To(BeTrue())
		})

		It("seeds manager installer by default and nothing for a dev build", func() {
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &ichorv1alpha1.IchorInstallation{Name: ichorv1alpha1.IchorInstallationName}))).To(Succeed())
			})

			key := types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}

			withVersion("dev")
			Expect(newReconciler().Seed(ctx)).To(Succeed())

			var got ichorv1alpha1.IchorInstallation
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, &got))).To(BeTrue())

			withVersion("v0.1.0")
			Expect(newReconciler().Seed(ctx)).To(Succeed())
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
			Expect(got.Spec.Deployment.Manager).To(Equal(ichorv1alpha1.IchorManagerInstaller))
		})

		It("reports UpToDate True with reason ManagedByHelm whatever spec.version says", func() {
			withVersion("v0.2.0")

			inst := &ichorv1alpha1.IchorInstallation{
				Name: ichorv1alpha1.IchorInstallationName,
				Spec: ichorv1alpha1.IchorInstallationSpec{
					Version: "v0.1.0", Deployment: &ichorv1alpha1.IchorDeploymentSpec{Manager: ichorv1alpha1.IchorManagerHelm},
				},
			}
			Expect(k8sClient.Create(ctx, inst)).To(Succeed())
			DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, inst))).To(Succeed()) })

			got := reconcileOnce(newReconciler())
			cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionUpToDate)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(ichorv1alpha1.ReasonManagedByHelm))
		})

		It("removes the ExternalSuggested condition an older build set", func() {
			withVersion("v0.1.0")
			inst := createInstallation("v0.1.0")

			apimeta.SetStatusCondition(&inst.Status.Conditions, metav1.Condition{
				Type: conditionExternalSuggested, Status: metav1.ConditionTrue, Reason: "TrackingChecked",
			})
			Expect(k8sClient.Status().Update(ctx, inst)).To(Succeed())

			got := reconcileOnce(newReconciler())
			Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionExternalSuggested)).To(BeNil())
		})
	})

	Context("kind: ichor executor", func() {
		const jobNs = testIchorNamespace

		op := func() *ichorv1alpha1.TalosOperation {
			return &ichorv1alpha1.TalosOperation{Namespace: "ns", Name: "upgrade"}
		}

		jobs := func() []batchv1.Job {
			var l batchv1.JobList
			// Only installer Jobs: other reconcilers in this suite create Jobs in the same namespace.
			Expect(k8sClient.List(ctx, &l, client.InNamespace(jobNs),
				client.MatchingLabels{nameLabel: IchorInstallerServiceAccount})).To(Succeed())

			return l.Items
		}

		AfterEach(func() {
			Expect(k8sClient.DeleteAllOf(ctx, &batchv1.Job{}, client.InNamespace(jobNs),
				client.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())
		})

		It("launches the installer Job for spec.version once, and reports its progress", func() {
			withVersion("v0.1.0")
			createInstallation("v0.2.0")
			advance := NewIchorUpgradeAdvancer(k8sClient, k8sClient, jobNs, false, &fakeInstallerImages{digest: testInstallerDigest}, nil)
			o := op()

			done, err := advance(ctx, nil, nil, o)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			Expect(jobs()).To(HaveLen(1))

			job := jobs()[0]
			Expect(job.Spec.Template.Spec.Containers[0].Image).To(Equal(testInstallerRepo + "@" + testInstallerDigest))
			Expect(job.Spec.Template.Spec.ServiceAccountName).To(Equal(IchorInstallerServiceAccount))
			// A finished Job is removed after a day: long enough to read its log, and the
			// operation never needs it afterwards (the next test).
			Expect(job.Spec.TTLSecondsAfterFinished).To(HaveValue(Equal(int32(24 * 60 * 60))))

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
			createInstallation("v0.2.0")
			advance := NewIchorUpgradeAdvancer(k8sClient, k8sClient, jobNs, false, &fakeInstallerImages{digest: testInstallerDigest}, nil)
			o := op()

			_, err := advance(ctx, nil, nil, o)
			Expect(err).NotTo(HaveOccurred())

			job := jobs()[0]
			setJobCondition(&job, batchv1.JobFailed, "post-check failed")

			_, err = advance(ctx, nil, nil, o)
			Expect(err).To(MatchError(ContainSubstring("rolls back")))
		})

		It("refuses on a Helm-managed install and launches no Job", func() {
			withVersion("v0.1.0")

			inst := &ichorv1alpha1.IchorInstallation{
				Name: ichorv1alpha1.IchorInstallationName,
				Spec: ichorv1alpha1.IchorInstallationSpec{
					Version: "v0.3.0", Deployment: &ichorv1alpha1.IchorDeploymentSpec{Manager: ichorv1alpha1.IchorManagerHelm},
				},
			}
			Expect(k8sClient.Create(ctx, inst)).To(Succeed())
			DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, inst))).To(Succeed()) })

			_, err := NewIchorUpgradeAdvancer(k8sClient, k8sClient, jobNs, false, &fakeInstallerImages{digest: testInstallerDigest}, nil)(ctx, nil, nil, op())
			Expect(err).To(MatchError(ContainSubstring("helm upgrade")))
			Expect(jobs()).To(BeEmpty())
		})

		It("refuses a downgrade and launches no Job", func() {
			withVersion("v0.3.0")
			createInstallation("v0.2.0")

			_, err := NewIchorUpgradeAdvancer(k8sClient, k8sClient, jobNs, false, &fakeInstallerImages{digest: testInstallerDigest}, nil)(ctx, nil, nil, op())
			Expect(err).To(MatchError(ContainSubstring("does not downgrade")))
			Expect(jobs()).To(BeEmpty())
		})

		It("is done, with no Job, once the running version is the target (the Job may be long gone)", func() {
			withVersion("v0.2.0")
			createInstallation("v0.2.0")

			done, err := NewIchorUpgradeAdvancer(k8sClient, k8sClient, jobNs, false, &fakeInstallerImages{digest: testInstallerDigest}, nil)(ctx, nil, nil, op())
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

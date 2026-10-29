package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

var _ = Describe("IchorInstallation settings (ADR 0105)", func() {
	reconcileOnce := func(r *IchorInstallationReconciler) *ichorv1alpha1.IchorInstallation {
		_, err := r.Reconcile(ctx, reconcile.Request{Name: ichorv1alpha1.IchorInstallationName})
		Expect(err).NotTo(HaveOccurred())

		var got ichorv1alpha1.IchorInstallation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &got)).To(Succeed())

		return &got
	}

	setSpec := func() {
		inst := createInstallation("v0.1.0")
		inst.Spec.Users = &ichorv1alpha1.IchorUserSettings{TokenTTL: &metav1.Duration{Duration: 10 * time.Minute}}
		inst.Spec.Defaults = &ichorv1alpha1.IchorDefaultSettings{DriftPolicy: driftPolicyAutoCorrect}
		Expect(k8sClient.Update(ctx, inst)).To(Succeed())
	}

	It("reports each value and its source, and the SettingsApplied condition", func() {
		withVersion("v0.1.0")
		setSpec()

		got := reconcileOnce(&IchorInstallationReconciler{Client: k8sClient})
		eff := got.Status.Effective
		Expect(eff).NotTo(BeNil())
		Expect(*eff.DriftPolicy).To(Equal(ichorv1alpha1.IchorEffectiveValue{Value: driftPolicyAutoCorrect, Source: ichorv1alpha1.IchorSourceSpec}))
		Expect(*eff.TokenTTL).To(Equal(ichorv1alpha1.IchorEffectiveValue{Value: "10m0s", Source: ichorv1alpha1.IchorSourceSpec}))
		Expect(eff.RefreshLifetime).To(BeNil(), "ichor-ui reports a value the spec does not set")

		c := apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.ConditionSettingsApplied)
		Expect(c).NotTo(BeNil())
		Expect(c.ObservedGeneration).To(Equal(got.Generation))
	})

	It("falls back to the deployment for unset fields", func() {
		withVersion("v0.1.0")
		createInstallation("v0.1.0")

		got := reconcileOnce(&IchorInstallationReconciler{
			Client: k8sClient, SettingsFallback: settings.Fallback{},
		})
		Expect(*got.Status.Effective.DriftPolicy).To(Equal(ichorv1alpha1.IchorEffectiveValue{Value: settings.DriftPolicyDefault, Source: ichorv1alpha1.IchorSourceDeployment}))
	})

	It("ignores the spec settings under --settings-read-only", func() {
		withVersion("v0.1.0")
		setSpec()

		got := reconcileOnce(&IchorInstallationReconciler{Client: k8sClient, SettingsReadOnly: true})
		Expect(got.Status.Effective.TokenTTL).To(BeNil())
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.ConditionSettingsApplied).Reason).To(Equal("ReadOnly"))
	})
})

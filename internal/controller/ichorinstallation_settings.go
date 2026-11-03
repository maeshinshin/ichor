package controller

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

// applySettings writes status.effective and the SettingsApplied condition (ADR 0105). The
// user lifetimes are listed only when spec sets them: otherwise their fallback is an ichor-ui
// flag the controller does not know, and ichor-ui reports it itself.
func (r *IchorInstallationReconciler) applySettings(inst *ichorv1alpha1.IchorInstallation) {
	e := settings.Resolve(inst, r.SettingsFallback, r.SettingsReadOnly)

	val := func(v string, source string) *ichorv1alpha1.IchorEffectiveValue {
		return &ichorv1alpha1.IchorEffectiveValue{Value: v, Source: source}
	}

	eff := &ichorv1alpha1.IchorEffectiveSettings{
		DriftPolicy: val(e.DriftPolicy.V, e.DriftPolicy.Source),
		UpdateCheck: val(fmt.Sprint(e.UpdateCheck.V), e.UpdateCheck.Source),
	}

	manager, source := ichorv1alpha1.IchorManagerInstaller, ichorv1alpha1.IchorSourceDeployment
	if d := inst.Spec.Deployment; d != nil && d.Manager != "" {
		manager, source = d.Manager, ichorv1alpha1.IchorSourceSpec
	}

	eff.Manager = val(manager, source)

	if e.TokenTTL.Source == ichorv1alpha1.IchorSourceSpec {
		eff.TokenTTL = val(e.TokenTTL.V.String(), e.TokenTTL.Source)
	}

	if e.RefreshLifetime.Source == ichorv1alpha1.IchorSourceSpec {
		eff.RefreshLifetime = val(e.RefreshLifetime.V.String(), e.RefreshLifetime.Source)
	}

	inst.Status.Effective = eff

	reason, msg := "Applied", "settings are read live from spec; unset fields use the deployment's values"
	if r.SettingsReadOnly {
		reason, msg = "ReadOnly", "--settings-read-only: the spec settings are ignored, the deployment's values apply"
	}

	meta.SetStatusCondition(&inst.Status.Conditions, metav1.Condition{
		Type: ichorv1alpha1.ConditionSettingsApplied, Status: metav1.ConditionTrue, Reason: reason,
		Message: fmt.Sprintf("%s (generation %d)", msg, inst.Generation), ObservedGeneration: inst.Generation,
	})
}

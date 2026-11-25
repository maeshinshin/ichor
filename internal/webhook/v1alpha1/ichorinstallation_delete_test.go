package v1alpha1

import (
	"context"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// Deleting the IchorInstallation drops the installation's settings and its upgrade
// state, so it takes the same deliberate step as a TalosCluster: the allow-delete annotation.
func TestIchorInstallationDeleteNeedsTheAllowDeleteAnnotation(t *testing.T) {
	t.Parallel()

	v := &IchorInstallationCustomValidator{}
	inst := &ichorv1alpha1.IchorInstallation{Name: ichorv1alpha1.IchorInstallationName}

	if _, err := v.ValidateDelete(context.Background(), inst); err == nil {
		t.Fatal("deleting the IchorInstallation without the annotation was admitted")
	}

	inst.Annotations = map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue}

	if _, err := v.ValidateDelete(context.Background(), inst); err != nil {
		t.Fatalf("deleting with the annotation was refused: %v", err)
	}
}

// A window of equal start and end passes the CRD pattern but is empty, and would switch automatic patching off.
func TestIchorInstallationRefusesAnEmptyAutoPatchWindow(t *testing.T) {
	t.Parallel()

	v := &IchorInstallationCustomValidator{}
	ctx := context.Background()
	withWindow := func(w string) *ichorv1alpha1.IchorInstallation {
		return &ichorv1alpha1.IchorInstallation{
			Name: ichorv1alpha1.IchorInstallationName,
			Spec: ichorv1alpha1.IchorInstallationSpec{Version: "v0.4.0", AutoPatch: &ichorv1alpha1.IchorAutoPatchSettings{Window: w}},
		}
	}

	if _, err := v.ValidateUpdate(ctx, withWindow("01:00-02:00"), withWindow("02:00-02:00")); err == nil {
		t.Error("update to 02:00-02:00 admitted")
	}

	if _, err := v.ValidateCreate(ctx, withWindow("02:00-02:00")); err == nil {
		t.Error("create with 02:00-02:00 admitted")
	}

	if _, err := v.ValidateUpdate(ctx, withWindow("01:00-02:00"), withWindow("22:00-02:00")); err != nil {
		t.Errorf("a wrapping window is refused: %v", err)
	}
}

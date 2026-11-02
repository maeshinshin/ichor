package v1alpha1

import (
	"context"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// ADR 0108: deleting the IchorInstallation drops the installation's settings and its upgrade
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

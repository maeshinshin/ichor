package settings

import (
	"testing"
	"time"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func TestChangesApply(t *testing.T) {
	yes, no := true, false
	ttl, bad := 30*time.Minute, 2*time.Hour
	policy := "autoCorrect"

	inst := &ichorv1alpha1.IchorInstallation{}
	if err := (Changes{Reclaim: &yes, TokenTTL: &ttl, DriftPolicy: &policy}).Apply(inst); err != nil {
		t.Fatal(err)
	}

	if !*inst.Spec.Reclaim.Enabled || inst.Spec.Users.TokenTTL.Duration != ttl || inst.Spec.Defaults.DriftPolicy != policy {
		t.Fatalf("spec not written: %+v", inst.Spec)
	}

	if inst.Spec.Users.RefreshLifetime != nil {
		t.Fatal("an unchanged field was written")
	}

	if inst.Annotations[ichorv1alpha1.ConfirmReclaimAnnotation] != ichorv1alpha1.ConfirmReclaimValue {
		t.Fatal("enabling reclaim must set the confirm annotation")
	}

	if err := (Changes{Reclaim: &no}).Apply(inst); err != nil {
		t.Fatal(err)
	}

	if _, ok := inst.Annotations[ichorv1alpha1.ConfirmReclaimAnnotation]; ok {
		t.Fatal("the annotation should go when reclaim is not being enabled")
	}

	if (Changes{TokenTTL: &bad}).Validate() == nil {
		t.Fatal("2h tokenTTL is out of bounds")
	}
}

func TestParseAssignments(t *testing.T) {
	c, err := ParseAssignments([]string{"reclaim.enabled=true", "users.tokenTTL=15m", "defaults.driftPolicy=warnOnly"})
	if err != nil || !*c.Reclaim || *c.TokenTTL != 15*time.Minute || *c.DriftPolicy != "warnOnly" {
		t.Fatalf("%+v %v", c, err)
	}

	for _, bad := range []string{"nope=1", "reclaim.enabled=maybe", "users.tokenTTL=5h", "users.tokenTTL", "defaults.driftPolicy=enforce"} {
		if _, err := ParseAssignments([]string{bad}); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

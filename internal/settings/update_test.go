package settings

import (
	"strings"
	"testing"
	"time"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func TestChangesApply(t *testing.T) {
	ttl, bad := 30*time.Minute, 2*time.Hour
	policy := DriftAutoCorrect

	inst := &ichorv1alpha1.IchorInstallation{}
	(Changes{TokenTTL: &ttl, DriftPolicy: &policy}).Apply(inst)

	if inst.Spec.Reclaim != nil || inst.Spec.Users.TokenTTL.Duration != ttl || inst.Spec.Defaults.DriftPolicy != policy {
		t.Fatalf("spec not written: %+v", inst.Spec)
	}

	if inst.Spec.Users.RefreshLifetime != nil {
		t.Fatal("an unchanged field was written")
	}

	if (Changes{TokenTTL: &bad}).Validate() == nil {
		t.Fatal("2h tokenTTL is out of bounds")
	}
}

func TestParseAssignments(t *testing.T) {
	c, err := ParseAssignments([]string{"users.tokenTTL=15m", "defaults.driftPolicy=" + DriftWarnOnly})
	if err != nil || *c.TokenTTL != 15*time.Minute || *c.DriftPolicy != DriftWarnOnly {
		t.Fatalf("%+v %v", c, err)
	}

	for _, bad := range []string{"nope=1", "users.tokenTTL=5h", "users.tokenTTL", "defaults.driftPolicy=enforce"} {
		if _, err := ParseAssignments([]string{bad}); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}

	_, err = ParseAssignments([]string{"reclaim.enabled=true"})
	if err == nil || !strings.Contains(err.Error(), "0107") {
		t.Fatalf("reclaim.enabled must be refused with a pointer to ADR 0107: %v", err)
	}
}

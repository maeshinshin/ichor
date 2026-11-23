package settings

import (
	"strings"
	"testing"
	"time"

		metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	if err == nil || !strings.Contains(err.Error(), "RBAC") {
		t.Fatalf("reclaim.enabled must be refused with a pointer: %v", err)
	}
}

func TestUnsetFollowsTheDeploymentDefault(t *testing.T) {
	in := &ichorv1alpha1.IchorInstallation{}
	ttl, drift, window := 30*time.Minute, DriftAutoCorrect, "02:00-05:00"
	(Changes{TokenTTL: &ttl, DriftPolicy: &drift, AutoPatchWindow: &window}).Apply(in)

	if e := Resolve(in, fb, false); e.TokenTTL.V != ttl || e.TokenTTL.Source != ichorv1alpha1.IchorSourceSpec {
		t.Fatalf("set value must win: %+v", e.TokenTTL)
	}

	c := Changes{Unset: []string{KeyTokenTTL, KeyDriftPolicy, KeyAutoPatchWindow}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	c.Apply(in)

	if in.Spec.Users != nil || in.Spec.Defaults != nil || in.Spec.AutoPatch != nil {
		t.Fatalf("an unset must clear the fields, not write the default: %+v", in.Spec)
	}

	e := Resolve(in, fb, false)
	if e.TokenTTL.V != fb.TokenTTL || e.TokenTTL.Source != ichorv1alpha1.IchorSourceDeployment || e.DriftPolicy.Source != ichorv1alpha1.IchorSourceDeployment {
		t.Fatalf("after an unset the deployment value must apply: %+v", e)
	}
}

func TestUnsetThenChangedDeploymentDefaultIsSeen(t *testing.T) {
	in := inst()
	Changes{Unset: []string{KeyTokenTTL}}.Apply(in)

	moved := Fallback{TokenTTL: 20 * time.Minute, RefreshLifetime: fb.RefreshLifetime}
	if e := Resolve(in, moved, false); e.TokenTTL.V != 20*time.Minute {
		t.Fatalf("a changed Helm value must show after an unset: %+v", e.TokenTTL)
	}
}

func TestUnsetKeepsOtherFieldsAndValidates(t *testing.T) {
	in := inst()
	in.Spec.Users.RefreshLifetime = &metav1.Duration{Duration: 24 * time.Hour}
	Changes{Unset: []string{KeyTokenTTL}}.Apply(in)

	if in.Spec.Users == nil || in.Spec.Users.TokenTTL != nil || in.Spec.Users.RefreshLifetime == nil {
		t.Fatalf("only the named key goes: %+v", in.Spec.Users)
	}

	ttl := time.Hour
	for _, bad := range []Changes{
		{Unset: []string{"nope"}},
		{Unset: []string{KeyAutoPatchAckHalt}},
		{Unset: []string{KeyTokenTTL}, TokenTTL: &ttl},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v should fail", bad)
		}
	}

	if (Changes{Unset: []string{KeyUpdateCheck}}).Empty() {
		t.Fatal("an unset is a change")
	}
}

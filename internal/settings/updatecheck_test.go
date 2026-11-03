package settings

import (
	"testing"

	"k8s.io/utils/ptr"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func TestUpdateCheckResolve(t *testing.T) {
	fb := Fallback{UpdateCheck: true}

	if got := Resolve(nil, fb, false).UpdateCheck; !got.V || got.Source != ichorv1alpha1.IchorSourceDeployment {
		t.Fatalf("no IchorInstallation: got %+v, want the flag (true, deployment)", got)
	}

	inst := &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{
		UpdateCheck: &ichorv1alpha1.IchorUpdateCheckSettings{Enabled: ptr.To(false)},
	}}
	if got := Resolve(inst, fb, false).UpdateCheck; got.V || got.Source != ichorv1alpha1.IchorSourceSpec {
		t.Fatalf("spec false: got %+v, want (false, spec)", got)
	}

	if got := Resolve(inst, fb, true).UpdateCheck; !got.V || got.Source != ichorv1alpha1.IchorSourceDeployment {
		t.Fatalf("read-only ignores the spec: got %+v, want (true, deployment)", got)
	}

	unset := &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{
		UpdateCheck: &ichorv1alpha1.IchorUpdateCheckSettings{},
	}}
	if got := Resolve(unset, fb, false).UpdateCheck; !got.V || got.Source != ichorv1alpha1.IchorSourceDeployment {
		t.Fatalf("switch without enabled falls back to the flag: got %+v", got)
	}
}

func TestUpdateCheckAssignmentsAndApply(t *testing.T) {
	c, err := ParseAssignments([]string{"updateCheck.enabled=false"})
	if err != nil {
		t.Fatal(err)
	}

	if c.UpdateCheck == nil || *c.UpdateCheck {
		t.Fatalf("parsed %+v, want UpdateCheck=false", c)
	}

	if _, err := ParseAssignments([]string{"updateCheck.enabled=maybe"}); err == nil {
		t.Fatal("a value that is not a bool must be refused")
	}

	var inst ichorv1alpha1.IchorInstallation
	c.Apply(&inst)

	if inst.Spec.UpdateCheck == nil || inst.Spec.UpdateCheck.Enabled == nil || *inst.Spec.UpdateCheck.Enabled {
		t.Fatalf("Apply wrote %+v", inst.Spec.UpdateCheck)
	}

	if !(Changes{}).Empty() || (Changes{UpdateCheck: ptr.To(true)}).Empty() {
		t.Fatal("Empty must count the update check switch")
	}
}

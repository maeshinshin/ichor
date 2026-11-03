//nolint:goconst // the fixtures repeat the same version strings on purpose
package settings

import (
	"testing"
	"time"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func TestAutoPatchResolve(t *testing.T) {
	fb := Fallback{AutoPatch: AutoPatch{Window: "01:00-02:00"}}

	if got := Resolve(nil, fb, false).AutoPatch; got.V.Enabled || got.Source != ichorv1alpha1.IchorSourceDeployment {
		t.Fatalf("default is off from the deployment, got %+v", got)
	}

	inst := &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{
		AutoPatch: &ichorv1alpha1.IchorAutoPatchSettings{Enabled: boolPtr(true), Components: []string{"talos"}},
	}}

	got := Resolve(inst, fb, false).AutoPatch
	if !got.V.Enabled || got.Source != ichorv1alpha1.IchorSourceSpec || got.V.Window != "01:00-02:00" || !got.V.Has("talos") || got.V.Has("cilium") {
		t.Fatalf("spec on, talos only, window from the deployment: got %+v", got)
	}

	if ro := Resolve(inst, fb, true).AutoPatch; ro.V.Enabled || ro.Source != ichorv1alpha1.IchorSourceDeployment {
		t.Fatalf("read-only ignores the spec: got %+v", ro)
	}

	if !(AutoPatch{}).Has("kubernetes") {
		t.Fatal("no components means all")
	}
}

func TestAutoPatchAssignments(t *testing.T) {
	c, err := ParseAssignments([]string{"autoPatch.enabled=true", "autoPatch.components=talos, cilium", "autoPatch.window=22:00-02:00", "autoPatch.acknowledgeHalt=ns/op"})
	if err != nil {
		t.Fatal(err)
	}

	var inst ichorv1alpha1.IchorInstallation
	c.Apply(&inst)

	a := inst.Spec.AutoPatch
	if a == nil || a.Enabled == nil || !*a.Enabled || len(a.Components) != 2 || a.Window != "22:00-02:00" || a.AcknowledgedHalt != "ns/op" {
		t.Fatalf("Apply wrote %+v", a)
	}

	for _, bad := range []string{"autoPatch.components=etcd", "autoPatch.window=25:00-02:00", "autoPatch.window=03:00-03:00", "autoPatch.window=night", "autoPatch.enabled=x"} {
		if _, err := ParseAssignments([]string{bad}); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}

	if (Changes{AutoPatchAcknowledge: new(string)}).Empty() {
		t.Fatal("Empty must count the acknowledgement")
	}
}

func TestInWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 4, h, m, 0, 0, time.UTC) }

	for _, c := range []struct {
		window string
		now    time.Time
		want   bool
	}{
		{"", at(12, 0), true},
		{"02:00-05:00", at(2, 0), true},
		{"02:00-05:00", at(4, 59), true},
		{"02:00-05:00", at(5, 0), false},
		{"02:00-05:00", at(1, 59), false},
		{"22:00-02:00", at(23, 30), true},
		{"22:00-02:00", at(1, 0), true},
		{"22:00-02:00", at(12, 0), false},
		{"broken", at(12, 0), false},
	} {
		if got := InWindow(c.window, c.now); got != c.want {
			t.Errorf("InWindow(%q, %s) = %v, want %v", c.window, c.now.Format("15:04"), got, c.want)
		}
	}
}

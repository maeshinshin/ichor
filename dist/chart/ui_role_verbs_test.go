package chart

import (
	"slices"
	"testing"
)

// uiRoleVerbs returns the verbs ichor-ui-role grants on each ichor.maesh.dev resource, as the
// chart renders them.
func uiRoleVerbs(t *testing.T) map[string][]string {
	t.Helper()

	rules, _ := find(renderChartWithValues(t, "ichor"), kindClusterRole, ichorUIRoleName)["rules"].([]any)
	out := map[string][]string{}

	for _, r := range rules {
		rm, _ := r.(map[string]any)
		if !containsStr(rm["apiGroups"], "ichor.maesh.dev") {
			continue
		}

		for _, res := range stringsOf(rm["resources"]) {
			out[res] = append(out[res], stringsOf(rm["verbs"])...)
		}
	}

	return out
}

// TestUIRoleOpenModeVerbs pins what ichor-ui's ServiceAccount holds on TalosOperations and
// TalosMachines. It is the caller only with --allow-unauthenticated; the markers in
// internal/uiapi/rbac_markers.go give the reason for each verb.
func TestUIRoleOpenModeVerbs(t *testing.T) {
	got := uiRoleVerbs(t)

	// The start verb on all six subresources. The webhook lets the ServiceAccount through for four of
	// them; node-reset and etcd-restore also need the override verb (TestOpenModeServiceAccountStarts).
	for _, sub := range []string{
		"shutdown", "ca-rotation", "key-rotation", "etcd-restore", "node-reset", "node-shutdown",
	} {
		if !slices.Contains(got["talosoperations/"+sub], "start") {
			t.Errorf("ichor-ui-role has no start on talosoperations/%s: %v", sub, got["talosoperations/"+sub])
		}
	}

	// RemoveMachine is the only delete of a TalosMachine and asks for the override verb, which the
	// ServiceAccount does not hold, so no mode reaches it as the ServiceAccount.
	if slices.Contains(got["talosmachines"], verbDelete) {
		t.Errorf("ichor-ui-role holds delete on talosmachines: %v", got["talosmachines"])
	}

	for res, verbs := range map[string][]string{
		"talosoperations": {verbCreateController, verbDelete},
		"talosmachines":   {verbUpdate},
	} {
		for _, v := range verbs {
			if !slices.Contains(got[res], v) {
				t.Errorf("ichor-ui-role has no %s on %s: %v", v, res, got[res])
			}
		}
	}
}

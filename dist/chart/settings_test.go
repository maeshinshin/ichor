package chart

import (
	"fmt"
	"strings"
	"testing"
)

// TestSettingsReadOnly: settings.readOnly reaches both the controller and ichor-ui, and is
// off by default.
func TestSettingsReadOnly(t *testing.T) {
	has := func(docs []map[string]any) (ctrlArg, uiEnv bool) {
		for _, a := range containerArgs(find(docs, kindDeployment, "ichor-controller-manager")) {
			ctrlArg = ctrlArg || a == "--settings-read-only"
		}

		ui := fmt.Sprint(find(docs, kindDeployment, ichorUIDeploymentName))

		return ctrlArg, strings.Contains(ui, "ICHOR_SETTINGS_READ_ONLY")
	}

	if c, u := has(renderChartWithValues(t, "ichor")); c || u {
		t.Errorf("read-only rendered by default: controller %v, ui %v", c, u)
	}

	if c, u := has(renderChartWithValues(t, "ichor", "settings.readOnly=true")); !c || !u {
		t.Errorf("settings.readOnly=true: controller arg %v, ui env %v, want both", c, u)
	}
}

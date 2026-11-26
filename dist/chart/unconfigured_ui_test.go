package chart

import (
	"os/exec"
	"strings"
	"testing"
)

const noUsers = "ichorUI.users.enabled=false"

// TestChartRefusesAnUnconfiguredUI: ichor-ui does not start without a sign-in, so a render that would install
// it with none fails and says what to set, whatever else the values change. The default values are the first
// case, run without the sign-in the other chart tests add for themselves.
func TestChartRefusesAnUnconfiguredUI(t *testing.T) {
	check := func(name string, out []byte, err error) {
		t.Helper()

		if err == nil {
			t.Errorf("%s: the chart rendered an ichor-ui with no sign-in", name)

			return
		}

		wants := []string{
			"ichorUI.enabled needs a sign-in", "Sign-in cannot be turned off", usersOn,
			"ichorUI.oidc.enabled=true", "--bootstrap", uiOffSet,
		}
		for _, want := range wants {
			if !strings.Contains(string(out), want) {
				t.Errorf("%s: the refusal does not say %q: %s", name, want, out)
			}
		}
	}

	out, err := exec.Command("helm", kTemplate, "ichor", ".").CombinedOutput()
	check("default values", out, err)

	for name, sets := range map[string][]string{
		"users off":             {noUsers},
		"both off":              {noUsers, "ichorUI.oidc.enabled=false"},
		"enabled, nothing else": {"ichorUI.enabled=true", noUsers},
		"an URL but no switch":  {noUsers, usersURLShort},
		"with TLS and replicas": {noUsers, "ichorUI.tls.secretName=x", "ichorUI.replicas=1"},
		"with the installer":    {noUsers, installerOn},
	} {
		out, err := renderChartWithValuesErr("ichor", sets...)
		check(name, out, err)
	}

	// What the refusal asks for, and the way to install without ichor-ui, render.
	for name, sets := range map[string][]string{
		"users": {usersOn, usersURL},
		"oidc": {
			oidcEnabledSet, oidcIssuerSet, oidcClientID, impGroupsSet, impUsersSet,
		},
		"no ichor-ui": {uiOffSet},
	} {
		if out, err := renderChartWithValuesErr("ichor", sets...); err != nil {
			t.Errorf("%s: %v\n%s", name, err, out)
		}
	}
}

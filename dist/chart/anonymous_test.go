package chart

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestAllowUnauthenticatedIsRefused. The open mode is gone, and a values file that still asks for it is
// refused with what to do instead, whatever else it sets: a value that rendered and did nothing would let
// the operator believe ichor-ui writes without a sign-in, or believe it is open when it is not.
func TestAllowUnauthenticatedIsRefused(t *testing.T) {
	ui := func(sets ...string) string {
		return fmt.Sprint(find(renderChartWithValues(t, "ichor", sets...), kindDeployment, ichorUIDeploymentName)["spec"])
	}

	const flag = "--allow-unauthenticated"

	if strings.Contains(ui(), flag) {
		t.Error("the removed flag renders by default")
	}

	users := []string{usersOn, usersURLShort}

	for name, sets := range map[string][]string{
		"alone":              {allowOpenSet},
		"beside Ichor users": slices.Concat([]string{allowOpenSet}, users),
		"beside OIDC": {
			allowOpenSet, oidcEnabledSet, oidcIssuerSet, oidcClientID,
			impGroupsSet, "ichorUI.oidc.impersonate.users={alice}",
		},
		"with ichor-ui off": {uiOffSet, allowOpenSet},
		"as a string":       {"ichorUI.allowUnauthenticated=\"true\""},
	} {
		out, err := renderChartWithValuesErr("ichor", sets...)
		if err == nil {
			t.Errorf("%s: the chart rendered with ichorUI.allowUnauthenticated set: %s", name, out)

			continue
		}

		wants := []string{"ichorUI.allowUnauthenticated was removed", usersOn, "--set ichorUI.allowUnauthenticated=null"}
		for _, want := range wants {
			if !strings.Contains(string(out)+err.Error(), want) {
				t.Errorf("%s: the refusal does not say %q: %s %v", name, want, out, err)
			}
		}
	}

	// The explicit false of an old values file, and the null that clears --reuse-values, still render and
	// still leave ichor-ui without the flag.
	for _, set := range []string{"ichorUI.allowUnauthenticated=false", "ichorUI.allowUnauthenticated=null"} {
		if strings.Contains(ui(set), flag) {
			t.Errorf("%s renders the removed flag", set)
		}
	}

	proxies := ui(slices.Concat(users, []string{"ichorUI.users.trustedProxyCIDRs={10.0.0.0/8,192.168.0.0/16}"})...)
	if !strings.Contains(proxies, "--trusted-proxy-cidrs=10.0.0.0/8,192.168.0.0/16") {
		t.Error("trustedProxyCIDRs does not reach ichor-ui")
	}
}

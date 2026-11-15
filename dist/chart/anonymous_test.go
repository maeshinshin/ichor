package chart

import (
	"fmt"
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

	users := []string{usersOn, "ichorUI.users.externalURL=https://x"}

	for name, sets := range map[string][]string{
		"alone":              {"ichorUI.allowUnauthenticated=true"},
		"beside Ichor users": append([]string{"ichorUI.allowUnauthenticated=true"}, users...),
		"beside OIDC": {
			"ichorUI.allowUnauthenticated=true", "ichorUI.oidc.enabled=true", "ichorUI.oidc.issuer=https://idp.example.test",
			"ichorUI.oidc.clientId=ichor", "ichorUI.oidc.impersonate.groups={dev}", "ichorUI.oidc.impersonate.users={alice}",
		},
		"with ichor-ui off": {"ichorUI.enabled=false", "ichorUI.allowUnauthenticated=true"},
		"as a string":       {"ichorUI.allowUnauthenticated=\"true\""},
	} {
		out, err := renderChartWithValuesErr("ichor", sets...)
		if err == nil {
			t.Errorf("%s: the chart rendered with ichorUI.allowUnauthenticated set: %s", name, out)

			continue
		}

		for _, want := range []string{"ichorUI.allowUnauthenticated was removed", "ichorUI.users.enabled=true", "--set ichorUI.allowUnauthenticated=null"} {
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

	proxies := ui(append(users, "ichorUI.users.trustedProxyCIDRs={10.0.0.0/8,192.168.0.0/16}")...)
	if !strings.Contains(proxies, "--trusted-proxy-cidrs=10.0.0.0/8,192.168.0.0/16") {
		t.Error("trustedProxyCIDRs does not reach ichor-ui")
	}
}

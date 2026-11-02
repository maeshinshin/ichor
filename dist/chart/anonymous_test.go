package chart

import (
	"fmt"
	"strings"
	"testing"
)

// TestAllowUnauthenticated. The open mode is explicit, off by default, and meaningless
// once a sign-in exists.
func TestAllowUnauthenticated(t *testing.T) {
	ui := func(sets ...string) string {
		return fmt.Sprint(find(renderChartWithValues(t, "ichor", sets...), kindDeployment, ichorUIDeploymentName)["spec"])
	}

	const flag = "--allow-unauthenticated=true"

	if strings.Contains(ui(), flag) {
		t.Error("the open mode renders by default")
	}

	if !strings.Contains(ui("ichorUI.allowUnauthenticated=true"), flag) {
		t.Error("ichorUI.allowUnauthenticated=true does not reach ichor-ui")
	}

	users := []string{usersOn, "ichorUI.users.externalURL=https://x"}

	if strings.Contains(ui(append(users, "ichorUI.allowUnauthenticated=true")...), flag) {
		t.Error("the open flag renders next to Ichor users")
	}

	proxies := ui(append(users, "ichorUI.users.trustedProxyCIDRs={10.0.0.0/8,192.168.0.0/16}")...)
	if !strings.Contains(proxies, "--trusted-proxy-cidrs=10.0.0.0/8,192.168.0.0/16") {
		t.Error("trustedProxyCIDRs does not reach ichor-ui")
	}
}

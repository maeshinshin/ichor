package chart

import (
	"fmt"
	"strings"
	"testing"
)

// TestAllowUnauthenticated: ADR 0108. The open mode is explicit, off by default, and meaningless
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

	if strings.Contains(ui("ichorUI.allowUnauthenticated=true", "ichorUI.users.enabled=true", "ichorUI.users.externalURL=https://x"), flag) {
		t.Error("the open flag renders next to Ichor users")
	}

	if !strings.Contains(ui("ichorUI.users.enabled=true", "ichorUI.users.externalURL=https://x", "ichorUI.users.trustedProxyCIDRs={10.0.0.0/8,192.168.0.0/16}"),
		"--trusted-proxy-cidrs=10.0.0.0/8,192.168.0.0/16") {
		t.Error("trustedProxyCIDRs does not reach ichor-ui")
	}
}

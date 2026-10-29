//go:build e2e
// +build e2e

/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package foreign

import (
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/test/internal/foreign"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
)

// TestB5Plan (B5): `adopt --plan --cni-unmanaged` logs the probed version contract and the per-contract class C counts.
// Cluster: `foreign up --talos v1.13.0 --workers 0` (ICHOR_E2E_F_TALOS=v1.13.0), optionally upgraded with TestB5Upgrade.
func TestB5Plan(t *testing.T) {
	c := build(t, foreign.Options{Workers: 0})
	out, err := adoptArgs(t, c, append(recoverFlags(c), "--plan", "--cni-unmanaged")...)
	if err != nil {
		t.Logf("plan exited with %v", err)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "version contract") {
			t.Logf("B5 contract line: %s", strings.TrimSpace(l))
		}
	}
	nothingWritten(t, c)
}

// TestB5Upgrade upgrades the node in place to ICHOR_E2E_F_UPGRADE_TO (default v1.14.3): the machine config stays at its old contract.
func TestB5Upgrade(t *testing.T) {
	c := build(t, foreign.Options{Workers: 0})
	to := env("ICHOR_E2E_F_UPGRADE_TO", "v1.14.3")
	tc := ftc(t, c)
	out, err := talosctl.Upgrade(ns, c.CPs[0], "factory.talos.dev/metal-installer/376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba:"+to, tc)
	t.Logf("talosctl upgrade: %v\n%s", err, out)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := talosctl.Version(ns, c.CPs[0], tc)
	t.Logf("version after upgrade:\n%s", v)
}

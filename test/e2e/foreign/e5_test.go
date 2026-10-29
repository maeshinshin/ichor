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
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
)

// e5 kills `ichor adopt` killAfter after its output shows trigger, then reruns it with the same flags
// (--recover-secrets, --tunnel geneve). It records the Helm release state the kill left.
func e5(t *testing.T, trigger string, killAfter time.Duration) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNINone})
	before := cniPaths(t, c)
	start := time.Now()
	out, killed := adoptKilled(t, c, trigger, killAfter, geneve...)
	t.Logf("run 1 lasted %s, killed=%v", time.Since(start), killed)
	if !killed {
		t.Fatalf("%q never appeared:\n%s", trigger, out)
	}
	t.Logf("Helm release records after the kill:\n%s", releaseState(c))
	ds, _ := inner(c, "-n", "kube-system", "get", "ds,deploy", "--no-headers")
	t.Logf("workloads after the kill:\n%s", ds)

	// The rerun passes no --secrets-bundle: --recover-secrets is accepted because the stored bundle equals the
	// recovered one, and --tunnel geneve is accepted although Ichor's Cilium is already there.
	start = time.Now()
	out, err := adopt(t, c, geneve...)
	t.Logf("rerun took %s", time.Since(start))
	if err != nil {
		t.Fatalf("the rerun failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "a secrets bundle already exists") {
		t.Errorf("the rerun was refused for the existing bundle")
	}
	if rel := releaseState(c); strings.Contains(rel, "status=pending") {
		t.Errorf("a Helm release record is still pending:\n%s", rel)
	}
	t.Logf("Helm release records after the rerun:\n%s", releaseState(c))
	managedAdopted(t, c, before)
}

// TestE5KillDuringHelmInstall: the CLI is killed 20 s into the Helm install of Cilium.
func TestE5KillDuringHelmInstall(t *testing.T) { e5(t, "adopt: installing Cilium", 20*time.Second) }

// TestE5KillAfterCiliumWait: the CLI is killed right after the Cilium wait.
func TestE5KillAfterCiliumWait(t *testing.T) {
	e5(t, "Cilium is installed and the nodes are Ready", time.Second)
}

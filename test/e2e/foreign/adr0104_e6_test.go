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

// TestE6BadImageBoundedWait (ADR 0104 E6, first half): the CLI's install manifest names an image tag that is not
// in the registry (ICHOR_E2E_F_ICHOR_ALT_BIN). Cilium goes in, the Ichor pods cannot pull, and adopt exits within
// --wait-timeout printing why the pods are Pending.
func TestE6BadImageBoundedWait(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNINone})
	stageAlt(t, c)
	const timeout = 4 * time.Minute
	start := time.Now()
	out, err := adoptBin(t, c, "/tmp/ichor-alt", append([]string{"--wait-timeout", timeout.String()}, geneve...)...)
	took := time.Since(start)
	t.Logf("adopt took %s", took)
	if err == nil {
		t.Fatalf("adopt succeeded with an unpullable image:\n%s", out)
	}
	// A fresh cluster gets Cilium from adopt; a reused one already runs Ichor's own.
	if !strings.Contains(out, "Cilium is installed") && !strings.Contains(out, "Cilium is already installed") {
		t.Errorf("adopt never got past the Cilium step")
	}
	if !strings.Contains(out, "did not finish within "+timeout.String()) {
		t.Errorf("the error does not name the timeout")
	}
	if !strings.Contains(out, "ImagePullBackOff") && !strings.Contains(out, "ErrImagePull") {
		t.Errorf("the output does not say why the pods are not running")
	}
	pods, _ := inner(c, "-n", crNS, "get", "pods", "--no-headers")
	t.Logf("pods in %s:\n%s", crNS, pods)
}

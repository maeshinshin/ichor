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
	"testing"

	"github.com/maeshinshin/ichor/test/internal/foreign"
)

// TestB2ReplacedAdopt (B2): kube-proxy disabled in Talos and replaced by Cilium is adopted as spec.cni.kubeProxy: replaced.
// Cluster: cilium-helm, --no-kube-proxy, then TestHelmSet with kubeProxyReplacement=true.
func TestB2ReplacedAdopt(t *testing.T) {
	c := build(t, bOpts(foreign.CNICiliumHelm, true, false, "kubeProxyReplacement=false"))
	takeoverAdopt(t, c)
	waitHandover(t, c, "")
	membership(t, c)
	if got := kubeProxyMode(c); got != "replaced" {
		t.Errorf("B2: spec.cni.kubeProxy = %q, want replaced", got)
	}
}

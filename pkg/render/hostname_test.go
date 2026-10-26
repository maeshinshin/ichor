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

package render

import (
	"strings"
	"testing"
)

// TestRenderHostname: TalosMachine.spec.hostname reaches the rendered config -- the controller
// uses it as the Kubernetes node and etcd member name, so the node must carry it. From the
// v1.14 contract it is the HostnameConfig document (replacing the generator's `auto: stable`,
// which cannot coexist with a hostname); before it, machine.network.hostname.
func TestRenderHostname(t *testing.T) {
	bundle := testBundle(t)

	for _, tc := range []struct{ name, contract string }{
		{"v1.14", ""},
		{"v1.13", "v1.13"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster := testCluster("true")
			if tc.contract != "" {
				cluster.Spec.VersionContract = tc.contract
			}

			m := testMachine("node-a", roleWorker, nil)
			m.Spec.Hostname = "cp-1"

			out, _, err := Render(cluster, m, bundle, nil, nil)
			if err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(string(out), "hostname: cp-1") {
				t.Fatalf("spec.hostname not rendered:\n%s", out)
			}

			if strings.Contains(string(out), "auto: stable") {
				t.Fatalf("auto: stable must not coexist with a static hostname:\n%s", out)
			}

			m.Spec.Hostname = ""

			out, _, err = Render(cluster, m, bundle, nil, nil)
			if err != nil {
				t.Fatal(err)
			}

			if strings.Contains(string(out), "hostname: cp-1") {
				t.Fatalf("empty spec.hostname must render no static hostname:\n%s", out)
			}
		})
	}
}

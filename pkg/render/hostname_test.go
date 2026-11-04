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

import "testing"

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

			out := mustRender(t, cluster, m, bundle, nil)
			requireAll(t, out, "hostname: cp-1")
			requireNone(t, out, "auto: stable")

			m.Spec.Hostname = ""

			requireNone(t, mustRender(t, cluster, m, bundle, nil), "hostname: cp-1")
		})
	}
}

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

const (
	roleLabel    = "node-role.kubernetes.io/control-plane: \"\""
	excludeLabel = "node.kubernetes.io/exclude-from-external-load-balancers"
)

// spec.controlPlane.omitDefaultNodeLabels drops what the generator adds to a control plane, in
// both contract shapes, and leaves the default, a worker and every other label alone (ADR 0144).
func TestOmitDefaultNodeLabels(t *testing.T) {
	bundle := testBundle(t)

	for _, contract := range []string{contractV113, versionContract} {
		t.Run(contract, func(t *testing.T) {
			render := func(omit bool, role string) string {
				c := testCluster("false")
				c.Spec.VersionContract = contract
				c.Spec.ControlPlane.OmitDefaultNodeLabels = omit

				m := testMachine("cp-a", role, nil)
				m.Spec.NodeLabels = map[string]string{"example.com/mine": "x"}

				return mustRender(t, c, m, bundle, nil)
			}

			requireAll(t, render(false, roleControlPlane), excludeLabel, "example.com/mine")

			out := render(true, roleControlPlane)
			requireNone(t, out, excludeLabel, roleLabel)
			requireAll(t, out, "example.com/mine")
		})
	}

	t.Run("a worker is unchanged", func(t *testing.T) {
		c := testCluster("false")
		c.Spec.ControlPlane.OmitDefaultNodeLabels = true

		requireNone(t, mustRender(t, c, testMachine("w-a", roleWorker, nil), bundle, nil), excludeLabel)
	})
}

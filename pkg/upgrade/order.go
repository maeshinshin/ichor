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

package upgrade

import (
	"cmp"
	"slices"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// SortByOrder sorts machines in place by
// ichorv1alpha1.TalosMachineUpgradeOrderLabel's value, machines without the
// label last, breaking ties by name for a deterministic order every
// reconcile agrees on. It is the one ordering the operation executor runs in
// and the plan shown for confirmation lists (ADR 0068): both call this.
func SortByOrder(machines []*ichorv1alpha1.TalosMachine) {
	slices.SortFunc(machines, func(a, b *ichorv1alpha1.TalosMachine) int {
		av, aok := a.Labels[ichorv1alpha1.TalosMachineUpgradeOrderLabel]
		bv, bok := b.Labels[ichorv1alpha1.TalosMachineUpgradeOrderLabel]
		if aok != bok {
			if aok {
				return -1
			}

			return 1
		}

		return cmp.Or(cmp.Compare(av, bv), cmp.Compare(a.Name, b.Name))
	})
}

// ConfigPending reports whether m waits for a config operation: a pending change that needs a reboot
// (status.rebootPaths) or an API server restart (status.apiServerRestartPaths). The plan shown for
// confirmation and the operation both select machines with it.
func ConfigPending(m *ichorv1alpha1.TalosMachine) bool {
	return len(m.Status.RebootPaths) > 0 || len(m.Status.APIServerRestartPaths) > 0
}

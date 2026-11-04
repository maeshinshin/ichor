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

package cli

import (
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// The kind column of the upgrade targets.
const (
	channelRecommended = "recommended"
	channelPatch       = "patch"
	channelNextMinor   = "next-minor"
)

// renderUpgradeTargets is the versions a cluster can move to as a table, newest first within each
// kind; the one an upgrade without a VERSION uses is marked. A list that cannot be given says why.
func renderUpgradeTargets(h *human, kind pb.OperationKind, m *pb.ListUpgradeTargetsResponse) error {
	if !m.GetAvailable() {
		h.Note(h.Mark(markWarning) + " no list of " + kindName(kind) + " versions: " + m.GetUnavailableReason())
	}

	var rows [][]string

	group := func(channel string, versions []string) {
		for _, v := range versions {
			def := ""
			if v == m.GetDefault() {
				def = h.Mark(markOK)
			}

			rows = append(rows, []string{v, channel, def})
		}
	}

	group(channelRecommended, m.GetRecommended())
	group(channelPatch, m.GetPatch())
	group(channelNextMinor, m.GetNextMinor())

	if len(rows) == 0 {
		h.Note("no version to move to")

		return h.Err()
	}

	h.Table([]string{colVersion, colKind, "default"}, rows)

	return h.Err()
}

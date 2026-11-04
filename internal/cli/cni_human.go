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
	"strings"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// noValueChange is said when a proposed layer leaves Cilium's effective values as they are.
const noValueChange = "no change to the effective values"

// renderValuesDiff prints the diff the server computed, line by line: added lines green, removed
// lines red, on a colour terminal only (Paint is the identity elsewhere). An empty diff is a ✓ line.
func renderValuesDiff(h *human, diff string) error {
	if diff == "" {
		h.Note(h.Mark(markOK) + " " + noValueChange)

		return h.Err()
	}

	for line := range strings.SplitSeq(strings.TrimSuffix(diff, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "+"):
			line = h.Paint(sgrGreen, line)
		case strings.HasPrefix(line, "-"):
			line = h.Paint(sgrRed, line)
		}

		h.Note(line)
	}

	return h.Err()
}

func renderValuesSet(h *human, cluster *pb.ObjectRef) error {
	return doneLine(h, "%s the Cilium values of %s", verbUpdate, nsName(cluster))
}

func renderCniUpgradeStarted(h *human, resp *pb.StartCniUpgradeResponse) error {
	versions := ""
	if resp.GetFromVersion() != "" || resp.GetToVersion() != "" {
		versions = " (" + resp.GetFromVersion() + " → " + resp.GetToVersion() + ")"
	}

	return doneLine(h, "started cni upgrade operation %s%s", nsName(resp.GetOperation()), versions)
}

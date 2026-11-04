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
	"fmt"
)

// The age column and the created row, shared by the tables and blocks of the human output.
const (
	nameColumn   = "name"
	ageColumn    = "age"
	createdLabel = "Created"
)

// doneLine prints the one result line of a command that changed something: "✓ created patch
// sysctl · ichor-system". The mark is green on a colour terminal.
func doneLine(h *human, format string, args ...any) error {
	h.Note(h.Mark(markOK) + " " + fmt.Sprintf(format, args...))

	return h.Err()
}

// byteSize is a size as B, KiB, MiB or GiB, one decimal above KiB.
func byteSize(n int64) string {
	const unit = 1024

	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	size, suffix := float64(n), ""

	for _, s := range []string{"KiB", "MiB", "GiB", "TiB"} {
		size /= unit
		suffix = s

		if size < unit {
			break
		}
	}

	return fmt.Sprintf("%.1f %s", size, suffix)
}

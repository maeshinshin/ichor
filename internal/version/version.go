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

// Package version carries the version every Ichor binary was built as, and the one
// comparison rule built on it (ADR 0059).
package version

import (
	"fmt"

	"golang.org/x/mod/semver"
)

// Version is set at link time:
// -ldflags "-X github.com/maeshinshin/ichor/internal/version.Version=v0.1.0".
// "dev" means an unversioned build, which every check treats as "cannot judge".
var Version = "dev"

// MinorDistance reports how many minor versions apart a and b are. ok is false when either
// is not a semantic version (a "dev" build, an empty status) or the majors differ: the
// caller must then not claim a distance.
func MinorDistance(a, b string) (distance int, ok bool) {
	if !semver.IsValid(a) || !semver.IsValid(b) {
		return 0, false
	}

	var amaj, amin, bmaj, bmin int

	if _, err := fmt.Sscanf(semver.MajorMinor(a)+semver.MajorMinor(b), "v%d.%dv%d.%d", &amaj, &amin, &bmaj, &bmin); err != nil {
		return 0, false
	}

	if amaj != bmaj {
		return 0, false
	}

	return max(amin-bmin, bmin-amin), true
}

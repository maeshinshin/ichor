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

package version

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// CheckSemver reports whether v is a release version in the form every Ichor check compares: a
// leading "v" and MAJOR.MINOR.PATCH. The parser is golang.org/x/mod/semver, the one the
// controller's downgrade check uses, so a version the controller would misorder (a numeric
// pre-release identifier with a leading zero, for one) is refused before it reaches a spec.
func CheckSemver(v string) error {
	if semver.IsValid(v) && patchPresent(v) {
		return nil
	}

	return fmt.Errorf("%s is not a valid version (semver): %s", v, semverProblem(v))
}

// patchPresent reports that v spells all three numbers. semver.IsValid also takes vMAJOR and
// vMAJOR.MINOR, which Ichor does not accept as a target.
func patchPresent(v string) bool {
	core, _, _ := strings.Cut(v, "+")
	core, _, _ = strings.Cut(core, "-")

	return strings.Count(core, ".") == 2
}

// semverProblem names the first thing wrong with v, for CheckSemver's message.
func semverProblem(v string) string {
	if !strings.HasPrefix(v, "v") {
		return `it needs a leading "v", like v1.2.3`
	}

	rest, build, hasBuild := strings.Cut(v[1:], "+")
	core, pre, hasPre := strings.Cut(rest, "-")

	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return "want MAJOR.MINOR.PATCH"
	}

	for _, p := range parts {
		if msg := numberProblem(p); msg != "" {
			return msg
		}
	}

	if hasPre {
		if msg := identifiersProblem("pre-release", pre, true); msg != "" {
			return msg
		}
	}

	if hasBuild {
		if msg := identifiersProblem("build", build, false); msg != "" {
			return msg
		}
	}

	return "not a semantic version"
}

// numberProblem is a message when a core number is not a decimal without leading zeros.
func numberProblem(s string) string {
	if !isDigits(s) {
		return fmt.Sprintf("%q is not a number", s)
	}

	if len(s) > 1 && s[0] == '0' {
		return fmt.Sprintf("%q has a leading zero", s)
	}

	return ""
}

// identifiersProblem checks dot-separated pre-release or build identifiers. Only pre-release
// identifiers forbid leading zeros in their numeric form.
func identifiersProblem(kind, s string, noLeadingZero bool) string {
	for id := range strings.SplitSeq(s, ".") {
		switch {
		case id == "":
			return fmt.Sprintf("empty %s identifier", kind)
		case isDigits(id) && noLeadingZero && len(id) > 1 && id[0] == '0':
			return fmt.Sprintf("numeric %s identifier %q has a leading zero", kind, id)
		case !isAlnumHyphen(id):
			return fmt.Sprintf("%s identifier %q has characters other than letters, digits and hyphens", kind, id)
		}
	}

	return ""
}

func isDigits(s string) bool { return s != "" && strings.Trim(s, "0123456789") == "" }

// isAlnumHyphen is only called with a non-empty identifier.
func isAlnumHyphen(s string) bool {
	return strings.Trim(s, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-") == ""
}

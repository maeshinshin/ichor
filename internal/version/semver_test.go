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
	"strings"
	"testing"
)

func TestCheckSemver(t *testing.T) {
	t.Parallel()

	valid := []string{"v1.2.3", "v0.0.1", "v1.2.3-rc.1", "v1.2.3-0.3.7", "v1.2.3+build.5", "v0.0.1814000000-alpha"}
	for _, v := range valid {
		if err := CheckSemver(v); err != nil {
			t.Errorf("CheckSemver(%q) = %v, want nil", v, err)
		}
	}

	cases := map[string]string{
		"v0.0.1814000000-02357186": `numeric pre-release identifier "02357186" has a leading zero`,
		"v1.2.3-rc.01":             `numeric pre-release identifier "01" has a leading zero`,
		"v01.2.3":                  `"01" has a leading zero`,
		"1.2.3":                    `it needs a leading "v", like v1.2.3`,
		"v1.2":                     "want MAJOR.MINOR.PATCH",
		"v1":                       "want MAJOR.MINOR.PATCH",
		"v1.2.x":                   `"x" is not a number`,
		"v1.2.3-":                  "empty pre-release identifier",
		"v1.2.3-rc_1":              `pre-release identifier "rc_1" has characters other than letters, digits and hyphens`,
		"v1.2.3+":                  "empty build identifier",
		"":                         `it needs a leading "v", like v1.2.3`,
	}

	for v, reason := range cases {
		err := CheckSemver(v)
		if err == nil {
			t.Errorf("CheckSemver(%q) = nil, want an error", v)

			continue
		}

		want := v + " is not a valid version (semver): " + reason
		if err.Error() != want {
			t.Errorf("CheckSemver(%q) = %q, want %q", v, err.Error(), want)
		}
	}

	if !strings.Contains(CheckSemver("v1.2").Error(), "(semver)") {
		t.Error("the message must name the rule it failed")
	}
}

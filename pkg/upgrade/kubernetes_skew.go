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
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// KubernetesSkew enforces Kubernetes' own version skew policy
// (kubernetes.io/releases/version-skew-policy): an upgrade moves at most
// one minor version at a time, within the same major version, and never
// backwards. from and to are "vX.Y.Z".
//
// This is written generically rather than against a table of allowed
// version pairs because the rule itself has no
// exceptions to encode: every Kubernetes minor since 1.19 allows the exact
// same "N or N+1" move, so a table only adds upkeep (bumping it for every
// new Kubernetes minor) without adding information.
func KubernetesSkew(from, to string) error {
	if !semver.IsValid(from) {
		return fmt.Errorf("invalid current Kubernetes version %q", from)
	}

	if !semver.IsValid(to) {
		return fmt.Errorf("invalid target Kubernetes version %q", to)
	}

	if semver.Compare(to, from) < 0 {
		return fmt.Errorf("cannot downgrade Kubernetes from %s to %s", from, to)
	}

	if semver.Major(from) != semver.Major(to) {
		return fmt.Errorf("cannot change Kubernetes major version from %s to %s", from, to)
	}

	fromMinor, err := minor(from)
	if err != nil {
		return err
	}

	toMinor, err := minor(to)
	if err != nil {
		return err
	}

	if toMinor-fromMinor > 1 {
		return fmt.Errorf("version skew policy allows at most one minor version at a time: "+
			"%s to %s skips a minor version", from, to)
	}

	return nil
}

// minor extracts the minor version number from a "vX.Y..." string already
// confirmed valid by semver.IsValid.
func minor(v string) (int, error) {
	_, m, err := splitMinor(strings.TrimPrefix(semver.MajorMinor(v), "v"))

	return m, err
}

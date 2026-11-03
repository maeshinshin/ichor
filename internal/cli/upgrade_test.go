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
	"bytes"
	"strings"
	"testing"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// The choices --list and the no-default error print: each group newest first, the default marked, and
// a list that cannot be given says why.
func TestPrintUpgradeTargetsMarksTheDefault(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer

	printUpgradeTargets(&b, pb.OperationKind_OPERATION_KIND_TALOS, &pb.ListUpgradeTargetsResponse{
		Available: true, Recommended: []string{ver1143}, Patch: []string{"v1.12.4"}, NextMinor: []string{"v1.13.2"},
		Default: ver1143,
	})

	got := b.String()
	for _, want := range []string{"recommended  v1.14.3  (default)", "patch        v1.12.4", "next minor   v1.13.2"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q lacks %q", got, want)
		}
	}

	b.Reset()
	printUpgradeTargets(&b, pb.OperationKind_OPERATION_KIND_KUBERNETES, &pb.ListUpgradeTargetsResponse{
		UnavailableReason: "The release check is off: turn on the update check in Settings to list versions.",
	})

	if got := b.String(); !strings.Contains(got, "turn on the update check") || !strings.Contains(got, "no version to move to") {
		t.Errorf("output %q does not say why", got)
	}
}

// The version reported in the bug (ichor upgrade ichor v0.0.1814000000-02357186) was accepted
// and turned into an operation the controller then paused. It must fail here, before any
// cluster is reached.
func TestCheckUpgradeTargetRefusesANonSemver(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"v0.0.1814000000-02357186", "0.0.1814000000-02357186", "latest", "v1.34", "v1.2.3-rc_1"} {
		if err := checkUpgradeTarget(target); err == nil {
			t.Errorf("checkUpgradeTarget(%q) = nil, want an error", target)
		}
	}

	err := checkUpgradeTarget("v0.0.1814000000-02357186")
	if err == nil || err.Error() != "v0.0.1814000000-02357186 is not a valid version (semver): numeric pre-release identifier \"02357186\" has a leading zero" {
		t.Errorf("message = %v", err)
	}
}

func TestCheckUpgradeTargetAcceptsAReleaseWithOrWithoutTheV(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"v1.13.2", "1.13.2", "v0.1.0-rc.1"} {
		if err := checkUpgradeTarget(target); err != nil {
			t.Errorf("checkUpgradeTarget(%q) = %v, want nil", target, err)
		}
	}
}

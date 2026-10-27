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
	"testing"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

func TestPullRequestTextSaysNothingWasWrittenAndWhatToDoNext(t *testing.T) {
	got := pullRequestText("https://git.example/acme/infra/pulls/3")

	for _, want := range []string{"https://git.example/acme/infra/pulls/3", "no operation was created", "again without a version"} {
		if !strings.Contains(got, want) {
			t.Errorf("text lacks %q:\n%s", want, got)
		}
	}
}

func TestPlanTextSaysWhenTheUpgradeBecomesAPullRequest(t *testing.T) {
	got := planText(pb.OperationKind_OPERATION_KIND_ICHOR, "c", &pb.PlanOperationResponse{
		Effect: pb.OperationEffect_OPERATION_EFFECT_NONE, Current: "v0.1.0", Target: "v0.2.0", SpecChanges: true, OpensPullRequest: true,
	})
	if !strings.Contains(got, "pull request") || strings.Contains(got, "a Job applies") {
		t.Errorf("plan text = %q", got)
	}
}

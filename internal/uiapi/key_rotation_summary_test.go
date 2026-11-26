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

package uiapi

import (
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

func TestDescribeKeyRotationSummaryNamesOnlyTheTargets(t *testing.T) {
	op := &ichorv1alpha1.TalosOperation{Spec: ichorv1alpha1.TalosOperationSpec{
		KeyRotation: &ichorv1alpha1.TalosOperationKeyRotationSpec{Targets: []string{ichorv1alpha1.KeyRotationTargetSecretbox}},
	}}
	out := &pb.Operation{}
	describeKeyRotation(out, op, func(string, string) {})

	if strings.Contains(out.Summary, "service-account") || !strings.Contains(out.Summary, "secretbox") {
		t.Fatalf("summary %q", out.Summary)
	}
}

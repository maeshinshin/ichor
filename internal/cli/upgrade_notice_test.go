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

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestUpgradeNoticeListsRolesGrantingTheEarlierVerb(t *testing.T) {
	scheme, err := cliScheme()
	if err != nil {
		t.Fatal(err)
	}

	rule := func(verb, group string) []rbacv1.PolicyRule {
		return []rbacv1.PolicyRule{{APIGroups: []string{group}, Resources: []string{"talosmachines"}, Verbs: []string{verb}}}
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&rbacv1.ClusterRole{Name: "mine", Rules: rule(earlierOverrideVerb, "ichor.maesh.dev")},
		&rbacv1.ClusterRole{Name: "ichors", Labels: map[string]string{"app.kubernetes.io/name": ownLabelValue}, Rules: rule(earlierOverrideVerb, "ichor.maesh.dev")},
		&rbacv1.ClusterRole{Name: "other-group", Rules: rule(earlierOverrideVerb, "example.test")},
		&rbacv1.ClusterRole{Name: "new-verb", Rules: rule("override", "ichor.maesh.dev")},
		&rbacv1.Role{Namespace: "team", Name: "ops", Rules: rule(earlierOverrideVerb, "ichor.maesh.dev")},
	).Build()

	var out bytes.Buffer

	noticeEarlierOverrideVerb(t.Context(), c, &out)

	got := out.String()
	for _, want := range []string{"NOTICE", "mine", "team/ops", `"override"`} {
		if !strings.Contains(got, want) {
			t.Errorf("notice lacks %q:\n%s", want, got)
		}
	}

	for _, not := range []string{"ichors", "other-group", "new-verb"} {
		if strings.Contains(got, not) {
			t.Errorf("notice must not list %q:\n%s", not, got)
		}
	}
}

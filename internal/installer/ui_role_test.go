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

package installer

import (
	"context"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The controller keeps the users rules of ichor-ui-impersonate; the manifest ships it with
// `rules: []`. Applying that over a live role wipes them until the controller's next requeue, so
// the installer leaves the rules of a live role alone and creates the role when it is absent.
func TestApplyOneKeepsTheRulesOfTheLiveImpersonationRole(t *testing.T) {
	ctx := context.Background()
	manifest := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			kAPIVersion: "rbac.authorization.k8s.io/v1", kKind: "ClusterRole",
			kMetadata: map[string]any{kName: uiImpersonateRole},
			"rules":   []any{},
		}}
	}
	key := client.ObjectKey{Name: uiImpersonateRole}

	// Absent: created, empty.
	if err := ApplyOne(ctx, k8sClient, manifest()); err != nil {
		t.Fatalf("ApplyOne on an absent role: %v", err)
	}

	role := &rbacv1.ClusterRole{}
	if err := k8sClient.Get(ctx, key, role); err != nil {
		t.Fatal(err)
	}

	// The controller's write: a plain Update under another manager.
	role.Rules = []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"users"}, Verbs: []string{"impersonate"}, ResourceNames: []string{"ichor:a"}}}
	if err := k8sClient.Update(ctx, role, client.FieldOwner("ichor-controller")); err != nil {
		t.Fatal(err)
	}

	if err := ApplyOne(ctx, k8sClient, manifest()); err != nil {
		t.Fatalf("ApplyOne on a live role: %v", err)
	}

	got := &rbacv1.ClusterRole{}
	if err := k8sClient.Get(ctx, key, got); err != nil {
		t.Fatal(err)
	}

	if len(got.Rules) != 1 {
		t.Errorf("the installer apply left %d rules on the live role, want the controller's 1", len(got.Rules))
	}

}

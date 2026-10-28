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

package stackapply

import (
	"os"
	"slices"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// resourceOf maps each kind the Job applies to its resource; a new kind in a bundle fails the
// test until it is listed here and granted in config/rbac/ichor-stack/role.yaml.
var resourceOf = map[string]string{
	"/ConfigMap": "configmaps", "/Namespace": "namespaces", "/Secret": "secrets", "/Service": "services",
	"/ServiceAccount": "serviceaccounts", "apps/Deployment": "deployments", "apps/StatefulSet": "statefulsets",
	"admissionregistration.k8s.io/ValidatingAdmissionPolicy":        "validatingadmissionpolicies",
	"admissionregistration.k8s.io/ValidatingAdmissionPolicyBinding": "validatingadmissionpolicybindings",
	"apiextensions.k8s.io/CustomResourceDefinition":                 "customresourcedefinitions",
	"argoproj.io/AppProject":                                        "appprojects",
	"cilium.io/CiliumNetworkPolicy":                                 "ciliumnetworkpolicies",
	"rbac.authorization.k8s.io/ClusterRole":                         "clusterroles",
	"rbac.authorization.k8s.io/ClusterRoleBinding":                  "clusterrolebindings",
	"rbac.authorization.k8s.io/Role":                                "roles",
	"rbac.authorization.k8s.io/RoleBinding":                         "rolebindings",
}

const verbCreate = "create"

func allows(rules []rbacv1.PolicyRule, group, resource, verb string) bool {
	for _, r := range rules {
		if slices.Contains(r.APIGroups, group) && slices.Contains(r.Resources, resource) && slices.Contains(r.Verbs, verb) {
			return true
		}
	}

	return false
}

// The Job's ClusterRole covers every kind of both bundles (create and patch: server-side apply)
// and can only create Secrets.
func TestRoleCoversEveryAppliedKind(t *testing.T) {
	raw, err := os.ReadFile("../../config/rbac/ichor-stack/role.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var role rbacv1.ClusterRole
	if err := sigsyaml.Unmarshal(raw, &role); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}

	for _, c := range []string{ComponentKokumi, ComponentArgoCD} {
		m, _, _, err := Render(Options{Component: c, KokumiAdminHash: "x", ArgoRedisPassword: "x"})
		if err != nil {
			t.Fatal(err)
		}

		docs, err := parse(m)
		if err != nil {
			t.Fatal(err)
		}

		pol, err := policiesFor(c)
		if err != nil {
			t.Fatal(err)
		}

		for _, d := range append(docs, pol...) {
			seen[d.GroupVersionKind().Group+"/"+d.GetKind()] = true
		}
	}

	for k := range seen {
		res, ok := resourceOf[k]
		if !ok {
			t.Errorf("kind %s is applied but unknown to this test and, likely, to the Job's role", k)

			continue
		}

		group, _, _ := strings.Cut(k, "/")
		verbs := []string{verbCreate, "patch"}

		if res == "secrets" {
			verbs = []string{verbCreate}
		}

		for _, v := range verbs {
			if !allows(role.Rules, group, res, v) {
				t.Errorf("config/rbac/ichor-stack/role.yaml does not grant %s on %s", v, res)
			}
		}
	}

	for _, r := range role.Rules {
		if slices.Contains(r.Resources, "secrets") && !slices.Equal(r.Verbs, []string{verbCreate}) {
			t.Errorf("the Job may only create Secrets, the role grants %v", r.Verbs)
		}

		if slices.Contains(r.Resources, "*") || slices.Contains(r.Verbs, "*") {
			t.Errorf("wildcard in rule %v", r)
		}
	}
}

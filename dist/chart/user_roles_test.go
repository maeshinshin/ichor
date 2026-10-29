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

package chart

import (
	"fmt"
	"strings"
	"testing"
)

// builtinUserRoles are the three built-in roles, lowest first. The chart names them
// ichor-<name>; config/rbac names them <name> and kustomize's namePrefix adds "ichor-".
const (
	ichorGroup      = "ichor.maesh.dev"
	verbStart       = "start"
	verbUpdate      = "update"
	verbImpersonate = "impersonate"
)

var builtinUserRoles = []string{"viewer", "operator", "admin"}

// TestBuiltinUserRoles guards: the three built-in ClusterRoles render only
// with ichorUI.users.enabled, never hold secrets, impersonate or '*', each includes the one below,
// are bound only to the groups ichor:viewer|operator|admin, and match config/rbac.
const (
	usersOn  = "ichorUI.users.enabled=true"
	usersURL = "ichorUI.users.externalURL=https://ichor.example.com"
)

func TestBuiltinUserRoles(t *testing.T) {
	off := renderChartWithValues(t, "ichor")
	for _, n := range builtinUserRoles {
		if find(off, kindClusterRole, "ichor-"+n) != nil {
			t.Errorf("ClusterRole ichor-%s rendered with ichorUI.users.enabled=false", n)
		}
	}

	docs := renderChartWithValues(t, "ichor", usersOn, usersURL)
	configDocs := loadYAMLDocs(t, "../../config/rbac/ichor_roles_binding.yaml")

	var prev map[rbacTriple]bool
	for _, n := range builtinUserRoles {
		role := find(docs, kindClusterRole, "ichor-"+n)
		if role == nil {
			t.Fatalf("expected ClusterRole ichor-%s with ichorUI.users.enabled=true", n)
		}
		got := rbacTriples(allRules(role))
		for tr := range got {
			if tr.verb == "*" || tr.resource == "*" || tr.group == "*" ||
				tr.verb == verbImpersonate || strings.Contains(tr.resource, "secrets") {
				t.Errorf("ClusterRole ichor-%s grants %s", n, tr)
			}
		}
		for tr := range prev {
			if !got[tr] {
				t.Errorf("ClusterRole ichor-%s lacks %s, which the role below it grants", n, tr)
			}
		}
		if prev != nil && len(got) <= len(prev) {
			t.Errorf("ClusterRole ichor-%s adds nothing to the role below it", n)
		}
		prev = got

		cfg := loadYAMLDocs(t, "../../config/rbac/ichor_"+n+"_role.yaml")
		checkRBACRulesMatch(t, "ichor", "ClusterRole ichor-"+n, find(cfg, kindClusterRole, n), role,
			"config/rbac/ichor_"+n+"_role.yaml", "dist/chart/templates/rbac/ichor-user-roles.yaml")

		// Subjects: exactly the one group, in both the chart and the kustomize binding.
		want := fmt.Sprint([]any{map[string]any{
			"apiGroup": "rbac.authorization.k8s.io", "kind": "Group", "name": "ichor:" + n,
		}})
		for label, set := range map[string][]map[string]any{"chart": docs, "kustomize": configDocs} {
			bname := "ichor-" + n + "-rolebinding"
			if label == "kustomize" {
				bname = n + "-rolebinding"
			}
			b := find(set, kindClusterRoleBinding, bname)
			if b == nil {
				t.Errorf("%s: no ClusterRoleBinding %s", label, bname)
				continue
			}
			if fmt.Sprint(b["subjects"]) != want {
				t.Errorf("%s: %s subjects = %v, want only Group ichor:%s", label, bname, b["subjects"], n)
			}
		}
	}

	// The explicit verbs live in ichor-admin and nowhere below it.
	adminOnly := []rbacTriple{
		{ichorGroup, "talosoperations/shutdown", verbStart, ""},
		{ichorGroup, "talosoperations/ca-rotation", verbStart, ""},
		{ichorGroup, "talosoperations/etcd-restore", verbStart, ""},
		{ichorGroup, "talosoperations/node-reset", verbStart, ""},
		{ichorGroup, "talosoperations/node-shutdown", verbStart, ""},
		{ichorGroup, "talosclusters/authentication", verbUpdate, ""},
		{ichorGroup, "talosclusters/secret-references", verbUpdate, ""},
		{ichorGroup, "talosetcdbackups/secret-references", verbUpdate, ""},
		{ichorGroup, "ichorusers/password", verbUpdate, ""},
		{ichorGroup, "talosclusters", "break-glass", ""},
		{ichorGroup, "talosclusters/credentials", "download", ""},
		{ichorGroup, "talosmachines/support-bundle", "download", ""},
		{ichorGroup, "ichorusers", "create", ""},
		{ichorGroup, "ichorinstallations/version", verbUpdate, ""},
		{ichorGroup, "machinereclaims", "reclaim", ""},
		{ichorGroup, "ichorinstallations/settings", verbUpdate, ""},
	}
	admin := rbacTriples(allRules(find(docs, kindClusterRole, "ichor-admin")))
	for _, tr := range adminOnly {
		if !admin[tr] {
			t.Errorf("ichor-admin lacks %s", tr)
		}
		for _, n := range []string{"viewer", "operator"} {
			if rbacTriples(allRules(find(docs, kindClusterRole, "ichor-"+n)))[tr] {
				t.Errorf("ichor-%s grants %s", n, tr)
			}
		}
	}

	// cluster-admin is opt-in.
	for _, d := range findAll(docs, kindClusterRoleBinding) {
		if ref, _ := clusterRoleBindingRefs(d); ref == "cluster-admin" {
			t.Errorf("cluster-admin bound by default: %v", d)
		}
	}
	ca := renderChartWithValues(t, "ichor", usersOn, usersURL, "ichorUI.users.clusterAdminRoles={admin}")
	if b := find(ca, kindClusterRoleBinding, "ichor-admin-cluster-admin"); b == nil ||
		!strings.Contains(fmt.Sprint(b["subjects"]), "ichor:admin") {
		t.Errorf("clusterAdminRoles={admin} must bind cluster-admin to ichor:admin, got %v", b)
	}
}

// TestUserAccessRoleEvents guards: the namespaced ichor-user-access
// Role lets viewers and operators read Events (the operation page's activity log), read-only, and
// the chart and config/rbac copies agree.
func TestUserAccessRoleEvents(t *testing.T) {
	docs := renderChartWithValues(t, "ichor", usersOn, usersURL)
	role := find(docs, "Role", "ichor-user-access")
	if role == nil {
		t.Fatal("expected Role ichor-user-access with ichorUI.users.enabled=true")
	}

	got := rbacTriples(allRules(role))
	for _, g := range []string{"", "events.k8s.io"} {
		for _, v := range []string{"get", "list", "watch"} {
			if !got[rbacTriple{g, "events", v, ""}] {
				t.Errorf("Role ichor-user-access lacks %s events %s", g, v)
			}
		}
	}

	for tr := range got {
		if tr.resource == "events" && tr.verb != "get" && tr.verb != "list" && tr.verb != "watch" {
			t.Errorf("Role ichor-user-access grants %s", tr)
		}
		if tr.verb == "*" || tr.verb == verbImpersonate || strings.Contains(tr.resource, "secrets") {
			t.Errorf("Role ichor-user-access grants %s", tr)
		}
	}

	cfg := loadYAMLDocs(t, "../../config/rbac/ichor_user_access_role.yaml")
	checkRBACRulesMatch(t, "ichor", "Role ichor-user-access", find(cfg, "Role", "user-access"), role,
		"config/rbac/ichor_user_access_role.yaml", "dist/chart/templates/rbac/ichor-user-roles.yaml")
}

// TestBreakGlassRole guards: the break-glass verb is grantable on clusters, machines and
// patches, in the chart and in config/rbac alike.
func TestBreakGlassRole(t *testing.T) {
	docs := renderChartWithValues(t, "ichor", "rbac.helpers.enabled=true")
	chartRole := findNameSuffix(docs, kindClusterRole, "break-glass-role")
	cfgRole := find(loadYAMLDocs(t, "../../config/rbac/break_glass_role.yaml"), kindClusterRole, "ichor-break-glass-role")

	checkRBACRulesMatch(t, "ichor", "ClusterRole ichor-break-glass-role", cfgRole, chartRole,
		"config/rbac/break_glass_role.yaml", "dist/chart/templates/rbac/ichor-break-glass-role.yaml")

	for _, res := range []string{"talosclusters", "talosconfigpatches", "talosmachines"} {
		if !rbacTriples(cfgRole["rules"].([]any))[rbacTriple{ichorGroup, res, "break-glass", ""}] {
			t.Errorf("config/rbac/break_glass_role.yaml does not grant break-glass on %s", res)
		}
	}
}

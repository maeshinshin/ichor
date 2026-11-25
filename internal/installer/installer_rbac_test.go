package installer

import (
	"os"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

const (
	groupIchor                   = "ichor.maesh.dev"
	verbGet, verbList, verbPatch = "get", "list", "patch"
	verbCreate, verbDelete       = "create", "delete"
	resConfigMaps, resPatches    = "configmaps", "talosconfigpatches"
	kVersionContract             = "versionContract"
)

// Every call the installer makes to a fixed resource, with the Role that must grant it. Add a row when the installer
// gains a call; a row the Role does not grant fails, as TalosClusters did when a get was written against a list-only
// rule. Calls on the objects of the install manifest are covered by the access check of Run, not listed here.
var installerCalls = []struct {
	cluster               bool // the call needs the ClusterRole: the resource is cluster-scoped or read across namespaces
	group, resource, verb string
}{
	{false, "apps", "deployments", verbGet},                              // customize.go customizeFromLive
	{false, "", resConfigMaps, verbGet},                                  // prune.go inventory, snapshot.go
	{false, "", resConfigMaps, verbCreate},                               // prune.go inventory, snapshot.go
	{false, "", resConfigMaps, verbDelete},                               // prune.go inventory, snapshot.go
	{false, "", resConfigMaps, verbList},                                 // snapshot.go
	{false, groupIchor, "talosclusters", verbList},                       // customize.go withIssuerURL
	{true, groupIchor, resPatches, verbPatch},                            // migrate_patches.go, every namespace
	{true, groupIchor, "talosconfigpatches/status", verbPatch},           // migrate_patches.go setMigrated, every namespace
	{true, groupIchor, resPatches, verbList},                             // retired_stored.go, every namespace
	{true, groupIchor, "talosclusters", verbList},                        // migrate_patches.go checkClusterContract, every namespace
	{true, "apiextensions.k8s.io", "customresourcedefinitions", verbGet}, // install.go, crdcheck.go
	{true, "rbac.authorization.k8s.io", "clusterroles", verbGet},         // prune.go renamedObjects
}

func TestInstallerRoleGrantsEveryCallTheInstallerMakes(t *testing.T) {
	raw, err := os.ReadFile("../../config/rbac/ichor-installer/role.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var cluster, namespaced []rbacv1.PolicyRule

	for doc := range strings.SplitSeq(string(raw), "\n---\n") {
		var m struct {
			Kind  string
			Rules []rbacv1.PolicyRule
		}

		if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
			t.Fatal(err)
		}

		switch m.Kind {
		case kindClusterRole:
			cluster = m.Rules
		case kindRole:
			namespaced = m.Rules
		}
	}

	grants := func(rules []rbacv1.PolicyRule, group, resource, verb string) bool {
		for _, r := range rules {
			if len(r.ResourceNames) == 0 && has(r.APIGroups, group) && has(r.Resources, resource) && has(r.Verbs, verb) {
				return true
			}
		}

		return false
	}

	for _, c := range installerCalls {
		rules := namespaced
		if c.cluster {
			rules = cluster
		}

		if !grants(rules, c.group, c.resource, c.verb) {
			t.Errorf("the installer role does not grant %s on %s.%s (cluster-wide: %v)", c.verb, c.resource, c.group, c.cluster)
		}
	}
}

func has(list []string, s string) bool {
	for _, v := range list {
		if v == s || v == "*" {
			return true
		}
	}

	return false
}

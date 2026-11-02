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

import "testing"

// need is one (resource, verb) a flow makes as the signed-in user: the ichor-ui backend, the CLI and
// kubectl all act under the user's own rights (ADR 0094), so the user's role must hold every one.
type need struct{ resource, verb string }

// userFlow is a documented flow (docs/guides, docs/adr/0094, ADR 0108) and the lowest built-in role that
// is meant to run it.
type userFlow struct {
	name  string
	role  string // viewer, operator or admin
	needs []need
}

// credentialRequest is what every download makes before the controller issues its short-lived certificate
// (ADR 0108); the webhook checks the download verb, RBAC checks the create.
var credentialRequest = need{"taloscredentialrequests", "create"}

var userFlows = []userFlow{
	{"list and read every Ichor resource (ichor machine list, cluster pages)", "viewer", []need{
		{"talosclusters", "get"}, {"talosclusters", "list"}, {"talosmachines", "watch"}, {"talosoperations", "list"},
		{"talosetcdbackups", "list"}, {"talosstorages", "list"}, {"ichorinstallations", "get"}, {"talosconfigpatches", "list"},
	}},
	{"start an operation such as ichor config apply, upgrade, reboot", "operator", []need{{"talosoperations", "create"}, {"talosoperations", "update"}}},
	{"pause, resume and delete an operation", "operator", []need{{"talosoperations", "patch"}, {"talosoperations", "delete"}}},
	{"create, edit and delete a config patch (ichor patch ...)", "operator", []need{
		{"talosconfigpatches", "create"}, {"talosconfigpatches", "update"}, {"talosconfigpatches", "delete"},
	}},
	{"approve a machine (ichor machine approve)", "operator", []need{{"talosmachines/approval", "approve"}, {"talosmachines", "update"}}},
	{"reject a machine and change its settings", "operator", []need{{"talosmachines", "patch"}}},
	{"add and remove discovery ranges (ichor cluster discovery)", "operator", []need{{"talosclusters", "update"}}},
	{"back up etcd now (ichor etcd backup now sets an annotation)", "operator", []need{{"talosetcdbackups", "patch"}, {"talosetcdbackups", "update"}}},
	{"define and delete a TalosEtcdBackup", "admin", []need{{"talosetcdbackups", "create"}, {"talosetcdbackups", "update"}, {"talosetcdbackups", "delete"}}},
	{"set the credentials of a backup destination", "admin", []need{{"talosetcdbackups/secret-references", "update"}}},
	{"delete a machine or a cluster", "admin", []need{{"talosmachines", "delete"}, {"talosclusters", "delete"}}},
	{"set a machine's endpoint outside discovery", "admin", []need{{"talosmachines/endpoint", "update"}}},
	{"change ichor settings (ichor settings set)", "admin", []need{{"ichorinstallations/settings", "update"}}},
	{"upgrade ichor (ichor upgrade ichor)", "admin", []need{{"ichorinstallations/version", "update"}, {"talosoperations", "create"}}},
	{"manage users (ichor user ...)", "admin", []need{
		{"ichorusers", "create"}, {"ichorusers", "update"}, {"ichorusers", "delete"}, {"ichorusers/password", "update"},
	}},
	{"change the authentication configuration", "admin", []need{{"talosclusters/authentication", "update"}, {"talosclusters/secret-references", "update"}}},
	{"rotate the CA (ichor cluster rotate-ca)", "admin", []need{{"talosoperations", "create"}, {"talosoperations/ca-rotation", "start"}}},
	{"restore etcd (ichor restore)", "admin", []need{{"talosoperations", "create"}, {"talosoperations/etcd-restore", "start"}}},
	{"shut a cluster down", "admin", []need{{"talosoperations", "create"}, {"talosoperations/shutdown", "start"}}},
	{"reset or shut down a node (ichor node reset)", "admin", []need{
		{"talosoperations/node-reset", "start"}, {"talosoperations/node-shutdown", "start"}, {"talosmachines", "break-glass"},
	}},
	{"reclaim a machine (ichor machine reclaim)", "admin", []need{
		{"machinereclaims", "create"}, {"machinereclaims", "reclaim"}, {"machinereclaims", "get"}, {"machinereclaims", "update"},
	}},
	{"download a support bundle", "admin", []need{{"talosmachines/support-bundle", "download"}, credentialRequest}},
	{"download an etcd snapshot", "admin", []need{{"talosmachines/etcd-snapshot", "download"}, credentialRequest}},
	{"capture packets", "admin", []need{{"talosmachines/packet-capture", "download"}, credentialRequest}},
	{"download the credentials after a CA rotation", "admin", []need{{"talosclusters/credentials", "download"}, credentialRequest}},
}

var roleRank = map[string]int{"viewer": 0, "operator": 1, "admin": 2}

// TestBuiltinRolesRunTheDocumentedFlows pins, in config/rbac and in the chart, that each built-in role holds
// every verb the flows it is meant to run make as the user. A role that has the download verb but not the
// create on taloscredentialrequests, or an admin who cannot patch a TalosEtcdBackup, fails here.
func TestBuiltinRolesRunTheDocumentedFlows(t *testing.T) {
	chartDocs := renderChartWithValues(t, "ichor", usersOn, usersURL)

	for _, f := range userFlows {
		for _, role := range builtinUserRoles {
			if roleRank[role] < roleRank[f.role] {
				continue
			}

			sources := map[string]map[rbacTriple]bool{
				labelChart: rbacTriples(allRules(find(chartDocs, kindClusterRole, "ichor-"+role))),
				labelConfig: rbacTriples(allRules(find(loadYAMLDocs(t, "../../config/rbac/ichor_"+role+"_role.yaml"),
					kindClusterRole, role))),
			}

			for label, got := range sources {
				for _, n := range f.needs {
					if !grantedOn(got, [2]string{ichorGroup, n.resource}, n.verb, "") {
						t.Errorf("%s: ClusterRole ichor-%s cannot %s %s, which the flow %q needs", label, role, n.verb, n.resource, f.name)
					}
				}
			}
		}
	}
}

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

package ichorauth

import "slices"

// The prefixes the kube-apiserver gives Ichor's own identities: user ichor:<name>, group ichor:<role>
// (ADR 0094). The external IdP cannot map to them.
const (
	UserPrefix  = "ichor:"
	GroupPrefix = "ichor:"
)

// UngrantedRole returns the first role in wanted that a requester in groups may not put on a user who
// holds current, or "" when there is none: an Ichor user may add only roles they hold, and an admin may
// add any (ADR 0108). The UI backend and the IchorUser webhook (ADR 0129) share this rule.
func UngrantedRole(groups, current, wanted []string) string {
	if slices.Contains(groups, GroupPrefix+RoleAdmin) {
		return ""
	}

	for _, r := range wanted {
		if !slices.Contains(current, r) && !slices.Contains(groups, GroupPrefix+r) {
			return r
		}
	}

	return ""
}

// IsEnabledAdmin reports whether a user with these roles, disabled or not, can administer Ichor.
func IsEnabledAdmin(roles []string, disabled bool) bool {
	return !disabled && slices.Contains(roles, RoleAdmin)
}

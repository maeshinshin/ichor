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

import (
	"slices"
	"strings"
)

// The prefixes the kube-apiserver gives Ichor's own identities: user ichor:<name>, group ichor:<role>
// (ADR 0094). The external IdP cannot map to them.
const (
	UserPrefix  = "ichor:"
	GroupPrefix = "ichor:"
)

// builtinRank orders the built-in roles: each one's ClusterRole includes the ones below it (ADR 0094).
var builtinRank = map[string]int{RoleViewer: 1, RoleOperator: 2, RoleAdmin: 3}

// UngrantedRole returns the first role in wanted that a requester in groups may not put on a user who
// holds current, or "" when there is none: an Ichor user may add the roles they hold and the built-in
// roles below their highest built-in one, a custom role only by a literal match, and an admin may add
// any (ADR 0108, ADR 0129). The UI backend and the IchorUser webhook share this rule.
func UngrantedRole(groups, current, wanted []string) string {
	top := 0
	for _, g := range groups {
		if r, ok := strings.CutPrefix(g, GroupPrefix); ok {
			top = max(top, builtinRank[r])
		}
	}

	if top == builtinRank[RoleAdmin] {
		return ""
	}

	for _, r := range wanted {
		below := builtinRank[r] > 0 && builtinRank[r] <= top
		if !below && !slices.Contains(current, r) && !slices.Contains(groups, GroupPrefix+r) {
			return r
		}
	}

	return ""
}

// IsEnabledAdmin reports whether a user with these roles, disabled or not, can administer Ichor.
func IsEnabledAdmin(roles []string, disabled bool) bool {
	return !disabled && slices.Contains(roles, RoleAdmin)
}

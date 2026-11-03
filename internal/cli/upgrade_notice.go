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
	"context"
	"fmt"
	"io"
	"slices"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// earlierOverrideVerb is the RBAC verb the safety-check override had before ADR 0109 renamed it.
// Ichor no longer accepts it; only this notice names it.
const earlierOverrideVerb = "break-glass"

// rolesGrantingEarlierVerb lists the ClusterRoles and Roles (as "name" or "namespace/name") that
// grant the earlier verb on ichor.maesh.dev. Roles labelled app.kubernetes.io/name=ichor are
// Ichor's own; the upgrade re-applies them, so they are skipped.
func rolesGrantingEarlierVerb(ctx context.Context, c client.Reader) ([]string, error) {
	grants := func(rules []rbacv1.PolicyRule) bool {
		return slices.ContainsFunc(rules, func(r rbacv1.PolicyRule) bool {
			return slices.Contains(r.Verbs, earlierOverrideVerb) && slices.Contains(r.APIGroups, "ichor.maesh.dev")
		})
	}

	var names []string

	var crs rbacv1.ClusterRoleList
	if err := c.List(ctx, &crs); err != nil {
		return nil, err
	}

	for _, r := range crs.Items {
		if !ownRole(r.Labels) && grants(r.Rules) {
			names = append(names, r.Name)
		}
	}

	var rs rbacv1.RoleList
	if err := c.List(ctx, &rs); err != nil {
		return nil, err
	}

	for _, r := range rs.Items {
		if !ownRole(r.Labels) && grants(r.Rules) {
			names = append(names, r.Namespace+"/"+r.Name)
		}
	}

	slices.Sort(names)

	return names, nil
}

// ownLabelValue is the app.kubernetes.io/name the installer and the chart put on Ichor's own objects.
const ownLabelValue = string(provenanceIchor)

// ownRole reports whether labels mark a role as one Ichor ships.
func ownRole(labels map[string]string) bool { return labels["app.kubernetes.io/name"] == ownLabelValue }

// noticeEarlierOverrideVerb prints a NOTICE naming the roles that still grant the earlier verb.
// It is advice: a cluster that cannot be read (no permission, no kubeconfig) prints nothing.
func noticeEarlierOverrideVerb(ctx context.Context, c client.Reader, w io.Writer) {
	names, err := rolesGrantingEarlierVerb(ctx, c)
	if err != nil || len(names) == 0 {
		return
	}

	_, _ = fmt.Fprintf(w, "NOTICE: these roles grant the %q verb on ichor.maesh.dev, which Ichor no longer accepts. Change it to %q:\n", earlierOverrideVerb, "override")

	for _, n := range names {
		_, _ = fmt.Fprintf(w, "  %s\n", n)
	}
}

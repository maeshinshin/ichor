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
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// This file is the pruning rule: an upgrade deletes what the previous run
// installed and the new manifest no longer ships, and nothing else.

const (
	// InstalledByLabel marks every object the installer applies. A later run deletes an object
	// only when it carries this label (or, for a renamed object, the app name label; see
	// renamedObjects), so a person's object, a Helm release's object and a CRD never match.
	InstalledByLabel = "ichor.maesh.dev/installed-by"

	// InstalledByValue is the value of InstalledByLabel: the installer's field manager.
	InstalledByValue = FieldOwner

	// inventoryName is the ConfigMap, in the installer's namespace, that lists the objects the
	// last successful run applied. Only those can be pruned by the next run.
	inventoryName = "ichor-installer-inventory"
	inventoryKey  = "objects"

	// appNameLabel is put on every Ichor object by the kustomize and chart bases. Objects that
	// predate InstalledByLabel carry it and nothing else that marks them as Ichor's.
	appNameLabel = "app.kubernetes.io/name"
	appNameValue = "ichor"
)

const (
	groupRBAC       = "rbac.authorization.k8s.io"
	groupAdmission  = "admissionregistration.k8s.io"
	kindClusterRole = "ClusterRole"
	kindRole        = "Role"
	breakGlassRole  = "ichor-ichor-break-glass-role"
	overrideRole    = "ichor-ichor-override-role"
)

// renamedObject is an object an earlier build installed under a name this build does not use.
// A build from before the inventory left no record of its objects, so these exact names are the
// only way such an object goes. Only renames already shipped belong here; a later rename is
// pruned from the inventory, because the build that shipped the old name recorded it.
type renamedObject struct {
	APIVersion, Kind, Name, To string
}

var renamedObjects = []renamedObject{
	// The safety-check override's ClusterRole.
	{APIVersion: "rbac.authorization.k8s.io/v1", Kind: kindClusterRole, Name: breakGlassRole, To: overrideRole},
}

// prunableKinds are the kinds a run may delete, each with the resource that
// config/rbac/ichor-installer/role.yaml grants delete on (TestPrunableKindsHaveDeleteRight holds
// the two together). Left out on purpose: CustomResourceDefinitions (deleting one deletes every
// custom resource of its kind), Namespaces and Secrets (they hold state), PriorityClasses (the
// installer has no delete on them), and CiliumNetworkPolicies (whether the bundle includes them
// depends on the detected CNI, not on the manifest, so their absence means nothing).
var prunableKinds = map[schema.GroupKind]string{
	{Group: groupRBAC, Kind: kindClusterRole}:                         "clusterroles",
	{Group: groupRBAC, Kind: "ClusterRoleBinding"}:                    "clusterrolebindings",
	{Group: groupRBAC, Kind: kindRole}:                                "roles",
	{Group: groupRBAC, Kind: "RoleBinding"}:                           "rolebindings",
	{Group: "", Kind: "ServiceAccount"}:                               "serviceaccounts",
	{Group: "", Kind: "Service"}:                                      "services",
	{Group: "apps", Kind: "Deployment"}:                               "deployments",
	{Group: groupAdmission, Kind: "MutatingWebhookConfiguration"}:     "mutatingwebhookconfigurations",
	{Group: groupAdmission, Kind: "ValidatingWebhookConfiguration"}:   "validatingwebhookconfigurations",
	{Group: groupAdmission, Kind: "ValidatingAdmissionPolicy"}:        "validatingadmissionpolicies",
	{Group: groupAdmission, Kind: "ValidatingAdmissionPolicyBinding"}: "validatingadmissionpolicybindings",
}

// ref names one object the way Kubernetes does. The version is not part of the identity: a
// bundle may move a kind to a newer version without renaming it.
type ref struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

func (r ref) groupKind() schema.GroupKind {
	return schema.FromAPIVersionAndKind(r.APIVersion, r.Kind).GroupKind()
}

func (r ref) id() string {
	return r.groupKind().String() + " " + r.Namespace + "/" + r.Name
}

// pruneCandidate is an object a run may remove. renamedTo is set for an entry of renamedObjects.
type pruneCandidate struct {
	ref
	renamedTo string
}

// pruneCandidates returns what a run in namespace ns may delete: the objects of the previous
// inventory that the bundle no longer ships, and the renamed objects the bundle does not ship.
// Each is of a prunable kind, is cluster-scoped or in ns, and is not one of the installer's own
// RBAC objects (the run applies the bundle with those rights until its last step). Whether the
// live object is actually the installer's is decided by owned.
func pruneCandidates(prev, bundle []ref, ns string) []pruneCandidate {
	shipped := make(map[string]bool, len(bundle))
	for _, r := range bundle {
		shipped[r.id()] = true
	}

	seen := map[string]bool{}

	var out []pruneCandidate

	add := func(r ref, renamedTo string) {
		_, prunable := prunableKinds[r.groupKind()]

		switch {
		case seen[r.id()], shipped[r.id()], !prunable:
			return
		case r.Namespace != "" && r.Namespace != ns:
			return
		case strings.HasPrefix(r.Name, installerNamePrefix):
			return
		}

		seen[r.id()] = true
		out = append(out, pruneCandidate{ref: r, renamedTo: renamedTo})
	}

	for _, r := range prev {
		add(r, "")
	}

	for _, rn := range renamedObjects {
		add(ref{APIVersion: rn.APIVersion, Kind: rn.Kind, Name: rn.Name}, rn.To)
	}

	return out
}

// owned reports whether live carries the marks that make it the installer's to remove: the
// installed-by label, or, for a renamed object from before that label existed, the app name label.
func owned(live *unstructured.Unstructured, c pruneCandidate) bool {
	labels := live.GetLabels()
	if labels[InstalledByLabel] == InstalledByValue {
		return true
	}

	return c.renamedTo != "" && labels[appNameLabel] == appNameValue
}

// markInstalled stamps InstalledByLabel on objs, the mark pruning relies on.
func markInstalled(objs []*unstructured.Unstructured) {
	for _, o := range objs {
		labels := o.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}

		labels[InstalledByLabel] = InstalledByValue
		o.SetLabels(labels)
	}
}

// readInventory returns the objects the last successful run recorded, or nil when none is.
// A recorded inventory that cannot be read is treated as none (logged): pruning then only
// removes the renamed objects, which is the safe side of the rule.
func readInventory(ctx context.Context, c client.Client, ns string, w io.Writer) ([]ref, error) {
	var cm corev1.ConfigMap

	err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: inventoryName}, &cm)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading the installer inventory: %w", err)
	}

	var refs []ref
	if err := json.Unmarshal([]byte(cm.Data[inventoryKey]), &refs); err != nil {
		logf(w, "ignoring the installer inventory, which cannot be read: %v", err)

		return nil, nil
	}

	return refs, nil
}

// writeInventory replaces the inventory with the objects of this run. The installer's Role may
// create and delete ConfigMaps but not update them, so the old one is deleted first. A write
// that fails costs the next run its prunes, never a wrong one, so the caller only logs it.
func writeInventory(ctx context.Context, c client.Client, ns string, refs []ref) error {
	raw, err := json.Marshal(refs)
	if err != nil {
		return err
	}

	cm := &corev1.ConfigMap{Data: map[string]string{inventoryKey: string(raw)}}
	cm.Namespace, cm.Name = ns, inventoryName

	if err := c.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	return c.Create(ctx, cm)
}

// prune deletes the candidates that are still present and owned, once the new bundle is in
// place. It snapshots them first, so a rollback restores them, and returns that snapshot.
func prune(ctx context.Context, c client.Client, opts Options, candidates []pruneCandidate) ([]entry, error) {
	var (
		doomed []pruneCandidate
		objs   []*unstructured.Unstructured
	)

	for _, cand := range candidates {
		o := &unstructured.Unstructured{}
		o.SetAPIVersion(cand.APIVersion)
		o.SetKind(cand.Kind)

		err := c.Get(ctx, types.NamespacedName{Namespace: cand.Namespace, Name: cand.Name}, o)
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("reading %s %s: %w", cand.Kind, cand.Name, err)
		}

		if !owned(o, cand) {
			logf(opts.Log, "keeping %s %s: it does not carry %s=%s, so this installer did not install it",
				cand.Kind, cand.Name, InstalledByLabel, InstalledByValue)

			continue
		}

		doomed = append(doomed, cand)
		objs = append(objs, o)
	}

	if len(objs) == 0 {
		return nil, nil
	}

	snap, err := takeSnapshot(ctx, c, opts.Namespace, opts.RunID, 3, objs)
	if err != nil {
		return nil, err
	}

	for i, o := range objs {
		reason := "the new manifest does not ship it"
		if doomed[i].renamedTo != "" {
			reason = "renamed to " + doomed[i].renamedTo
		}

		logf(opts.Log, "pruning %s %s: %s", o.GetKind(), o.GetName(), reason)

		if err := c.Delete(ctx, o); err != nil && !apierrors.IsNotFound(err) {
			return snap, fmt.Errorf("pruning %s %s: %w", o.GetKind(), o.GetName(), err)
		}
	}

	return snap, nil
}

// PendingRenames names the renamed objects of this build that the cluster still holds and the
// installer owns, so the upgrade plan can say what the run will remove. It returns nothing for a
// cluster where they are gone. A read error is returned for the caller to ignore: this is advice.
func PendingRenames(ctx context.Context, c client.Reader) ([]string, error) {
	var out []string

	for _, rn := range renamedObjects {
		o := &unstructured.Unstructured{}
		o.SetAPIVersion(rn.APIVersion)
		o.SetKind(rn.Kind)

		err := c.Get(ctx, types.NamespacedName{Name: rn.Name}, o)
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			continue
		}

		if err != nil {
			return nil, err
		}

		if owned(o, pruneCandidate{renamedTo: rn.To}) {
			out = append(out, fmt.Sprintf("%s %s (renamed to %s)", rn.Kind, rn.Name, rn.To))
		}
	}

	slices.Sort(out)

	return out, nil
}

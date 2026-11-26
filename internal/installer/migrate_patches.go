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
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

// MigratedCondition is the status condition a patch that could not be migrated carries (False, naming each setting
// spec.config has no place for) and a migrated one carries as True.
const MigratedCondition = "Migrated"

const kTalosCluster = "TalosCluster"

// The reasons of a False Migrated condition. NoPlaceInSpecConfig shipped in v0.3.0 and keeps its name.
const (
	reasonNoPlace       = "NoPlaceInSpecConfig"
	reasonClusterCannot = "ClusterCannotTake"
	reasonUnreadable    = "ClusterUnreadable"
	reasonWebhook       = "RefusedByWebhook"
)

// PatchMigration is what MigratePatches did.
type PatchMigration struct {
	Migrated []string // namespace/name of each patch rewritten to spec.config
	Failed   []string // namespace/name: reason, of each patch left as stored
}

// MigratePatches rewrites each TalosConfigPatch of namespace (every namespace when empty) that still sets
// spec.documents: spec.config gets the same settings and spec.documents is cleared, in one update. A patch whose
// settings spec.config has no place for is left as it is, with Migrated=False naming them; it keeps rendering from
// spec.documents. Running it again changes nothing that is already migrated.
func MigratePatches(ctx context.Context, c client.Client, namespace string, log io.Writer) (PatchMigration, error) {
	var res PatchMigration

	list := &unstructured.UnstructuredList{}
	list.SetAPIVersion(ichorv1alpha1.GroupVersion.String())
	list.SetKind("TalosConfigPatchList")

	if err := c.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return res, fmt.Errorf("listing TalosConfigPatches: %w", err)
	}

	for i := range list.Items {
		p := &list.Items[i]
		key := p.GetNamespace() + "/" + p.GetName()

		raw, _, _ := unstructured.NestedSlice(p.Object, "spec", "documents")
		if len(raw) == 0 {
			continue
		}

		if _, has, _ := unstructured.NestedMap(p.Object, "spec", "config"); has {
			res.Failed = append(res.Failed, key+": sets spec.config as well as spec.documents; remove spec.documents by hand")

			continue
		}

		reason := reasonNoPlace

		cfg, err := configOfDocuments(raw)
		if err == nil {
			if err = checkClusterContract(ctx, c, p, cfg); err != nil {
				reason = reasonClusterCannot

				if _, ok := errors.AsType[*unreadableError](err); ok {
					reason = reasonUnreadable
				}
			}
		}

		if err != nil {
			res.Failed = append(res.Failed, key+": "+err.Error())
			logf(log, "TalosConfigPatch %s is left as it is: %v", key, err)

			if serr := setMigrated(ctx, c, p, false, reason, err.Error()); serr != nil {
				logf(log, "could not set its Migrated condition: %v", serr)
			}

			continue
		}

		orig := p.DeepCopy()
		unstructured.RemoveNestedField(p.Object, "spec", "documents")

		if err := unstructured.SetNestedField(p.Object, cfg, "spec", "config"); err != nil {
			return res, err
		}

		// A refusal of one patch (the webhook) leaves that patch as stored; the others are still migrated.
		if err := c.Patch(ctx, p, client.MergeFrom(orig)); err != nil {
			err = fmt.Errorf("migrating TalosConfigPatch %s: %w", key, err)
			res.Failed = append(res.Failed, key+": "+err.Error())
			logf(log, "%v", err)

			// The patch object holds the failed edit; the condition is written on the stored one.
			if serr := setMigrated(ctx, c, orig, false, reasonWebhook, err.Error()); serr != nil {
				logf(log, "could not set its Migrated condition: %v", serr)
			}

			continue
		}

		res.Migrated = append(res.Migrated, key)
		logf(log, "TalosConfigPatch %s: spec.documents moved to spec.config", key)

		// Best effort: the patch is migrated whether or not its condition is written.
		_ = setMigrated(ctx, c, p, true, "Migrated", "spec.documents was moved to spec.config")
	}

	return res, nil
}

// unreadableError: the cluster of a patch could not be read, which says nothing about what it can take.
type unreadableError struct{ error }

func (e *unreadableError) Unwrap() error { return e.error }

// checkClusterContract refuses a spec.config the cluster of the patch cannot render: a setting its Talos version has
// no document for would make every node of the cluster fail to render. A cluster that is not there is not checked.
func checkClusterContract(ctx context.Context, c client.Client, p *unstructured.Unstructured, cfg map[string]any) error {
	// A list, not a get: the installer's roles grant talosclusters only `list`.
	cls := &unstructured.UnstructuredList{}
	cls.SetAPIVersion(ichorv1alpha1.GroupVersion.String())
	cls.SetKind(kTalosCluster + "List")

	if err := c.List(ctx, cls, client.InNamespace(p.GetNamespace())); err != nil {
		return &unreadableError{fmt.Errorf("reading the TalosClusters of %s: %w", p.GetNamespace(), err)}
	}

	name, _, _ := unstructured.NestedString(p.Object, "spec", "clusterRef", "name")

	var cl *unstructured.Unstructured

	for i := range cls.Items {
		if cls.Items[i].GetName() == name {
			cl = &cls.Items[i]
		}
	}

	if cl == nil {
		return nil
	}

	vc, _, _ := unstructured.NestedString(cl.Object, "spec", "versionContract")

	contract, err := coreconfig.ParseContractFromVersion(vc)
	if err != nil {
		return fmt.Errorf("cluster %s: parsing versionContract %q: %w", name, vc, err)
	}

	b, _ := json.Marshal(cfg)
	typed := &ichorconfig.Config{}

	if err := json.Unmarshal(b, typed); err != nil {
		return err
	}

	if _, err := ichorv1alpha1.ConvertConfig(typed, contract.MultidocKubernetesConfigSupported()); err != nil {
		return fmt.Errorf("cluster %s (versionContract %s) cannot take the migrated spec.config: %w; the patch keeps rendering from spec.documents", name, vc, err)
	}

	return nil
}

// configOfDocuments is spec.documents as the JSON of a spec.config.
func configOfDocuments(raw []any) (map[string]any, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}

	var docs []ichorv1alpha1.TalosConfigPatchDocument
	if err := json.Unmarshal(b, &docs); err != nil {
		return nil, err
	}

	cfg, err := ichorv1alpha1.ConfigFromDocuments(docs)
	if err != nil {
		return nil, err
	}

	if b, err = json.Marshal(cfg); err != nil {
		return nil, err
	}

	out := map[string]any{}

	return out, json.Unmarshal(b, &out)
}

// setMigrated records the Migrated condition. The hook Job and `ichor upgrade ichor` do not requeue, and the
// controller writes the same status, so a conflict re-reads the patch and applies only this condition again.
func setMigrated(ctx context.Context, c client.Client, p *unstructured.Unstructured, ok bool, reason, msg string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur := &unstructured.Unstructured{}
		cur.SetGroupVersionKind(p.GroupVersionKind())

		if err := c.Get(ctx, client.ObjectKeyFromObject(p), cur); err != nil {
			return err
		}

		return setMigratedOnce(ctx, c, cur, ok, reason, msg)
	})
}

func setMigratedOnce(ctx context.Context, c client.Client, p *unstructured.Unstructured, ok bool, reason, msg string) error {
	status := "False"
	if ok {
		status = "True"
	}

	orig := p.DeepCopy()
	conds, _, _ := unstructured.NestedSlice(p.Object, "status", "conditions")

	cond := map[string]any{
		"type": MigratedCondition, "status": status, "reason": reason, "message": msg,
		"observedGeneration": p.GetGeneration(), "lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
	}

	replaced := false

	for i, e := range conds {
		m, _ := e.(map[string]any)
		if m == nil || m["type"] != MigratedCondition {
			continue
		}

		replaced = true

		// meta.SetStatusCondition semantics: the transition time moves only with the status, and an
		// identical condition is not written at all (a write bumps resourceVersion).
		if m["status"] == status {
			cond["lastTransitionTime"] = m["lastTransitionTime"]
		}

		if reflect.DeepEqual(normalized(m), normalized(cond)) {
			return nil
		}

		conds[i] = cond
	}

	if !replaced {
		conds = append(conds, cond)
	}

	if err := unstructured.SetNestedSlice(p.Object, conds, "status", "conditions"); err != nil {
		return err
	}

	return c.Status().Patch(ctx, p, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
}

// normalized is a condition as JSON would hold it (numbers as int64), so a stored one compares with a built one.
func normalized(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))

	for k, v := range m {
		if n, ok := v.(float64); ok {
			v = int64(n)
		}

		out[k] = v
	}

	return out
}

// MigrateAllPatches is what the chart's post-upgrade hook runs (cmd/migrate_patches.go). A patch left as it is stays
// readable through spec.documents and carries Migrated=False, so only a failure to list is an error: a patch that
// cannot move would otherwise fail every later `helm upgrade` after the chart is already in place.
func MigrateAllPatches(ctx context.Context, c client.Client, log io.Writer) error {
	res, err := MigratePatches(ctx, c, "", log)
	if err != nil {
		return err
	}

	logf(log, "migrated %d TalosConfigPatches; %d left as they are", len(res.Migrated), len(res.Failed))

	return nil
}

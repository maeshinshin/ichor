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
	"reflect"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// MigratedCondition is the status condition a patch that could not be migrated carries (False, naming each setting
// spec.config has no place for) and a migrated one carries as True.
const MigratedCondition = "Migrated"

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

		cfg, err := configOfDocuments(raw)
		if err != nil {
			res.Failed = append(res.Failed, key+": "+err.Error())
			logf(log, "TalosConfigPatch %s is left as it is: %v", key, err)

			if serr := setMigrated(ctx, c, p, false, err.Error()); serr != nil {
				logf(log, "could not set its Migrated condition: %v", serr)
			}

			continue
		}

		orig := p.DeepCopy()
		unstructured.RemoveNestedField(p.Object, "spec", "documents")

		if err := unstructured.SetNestedField(p.Object, cfg, "spec", "config"); err != nil {
			return res, err
		}

		if err := c.Patch(ctx, p, client.MergeFrom(orig)); err != nil {
			return res, fmt.Errorf("migrating TalosConfigPatch %s: %w", key, err)
		}

		res.Migrated = append(res.Migrated, key)
		logf(log, "TalosConfigPatch %s: spec.documents moved to spec.config", key)

		// Best effort: the patch is migrated whether or not its condition is written.
		_ = setMigrated(ctx, c, p, true, "spec.documents was moved to spec.config")
	}

	return res, nil
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

func setMigrated(ctx context.Context, c client.Client, p *unstructured.Unstructured, ok bool, msg string) error {
	status, reason := "False", "NoPlaceInSpecConfig"
	if ok {
		status, reason = "True", "Migrated"
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

	return c.Status().Patch(ctx, p, client.MergeFrom(orig))
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

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
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// CheckStoredRetiredFields refuses while a stored TalosConfigPatch still holds a credential field ADR 0152 retired
// (cluster.extraManifestHeaders, machine.registries.config.<host>.tls.clientIdentity). The new CRD prunes them, so a
// node that used one would lose it silently at its next apply. It reads only, and must run before the new CRD is
// installed and before any write: the installer's Run and `ichor upgrade ichor` both call it. In the v0.2.4 schema the
// two fields exist only under spec.documents[].config.
func CheckStoredRetiredFields(ctx context.Context, c client.Reader) error {
	list := &unstructured.UnstructuredList{}
	list.SetAPIVersion(ichorv1alpha1.GroupVersion.String())
	list.SetKind("TalosConfigPatchList")

	// No CRD yet (a first install) means nothing is stored.
	if err := c.List(ctx, list); apimeta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("listing TalosConfigPatches: %w", err)
	}

	var found []string

	for i := range list.Items {
		p := &list.Items[i]
		docs, _, _ := unstructured.NestedSlice(p.Object, "spec", "documents")

		for j, d := range docs {
			m, _ := d.(map[string]any)
			if _, has, _ := unstructured.NestedFieldNoCopy(m, "config", "cluster", "extraManifestHeaders"); has {
				found = append(found, fmt.Sprintf("TalosConfigPatch %s/%s spec.documents[%d].config.cluster.extraManifestHeaders", p.GetNamespace(), p.GetName(), j))
			}

			hosts, _, _ := unstructured.NestedMap(m, "config", "machine", "registries", "config")
			for _, host := range sortedKeys(hosts) {
				if _, has, _ := unstructured.NestedFieldNoCopy(hosts, host, "tls", "clientIdentity"); has {
					found = append(found, fmt.Sprintf("TalosConfigPatch %s/%s spec.documents[%d].config.machine.registries.config.%s.tls.clientIdentity", p.GetNamespace(), p.GetName(), j, host))
				}
			}
		}
	}

	if len(found) == 0 {
		return nil
	}

	return fmt.Errorf("refusing to upgrade: this release drops credential fields the API server would prune without a word:\n  %s\n"+
		"move it out of the patch and set it on the node outside Ichor (ADR 0152) before upgrading", strings.Join(found, "\n  "))
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

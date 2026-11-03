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

package storage

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Select is the one rule for which TalosStorage governs a node: the storages
// belonging to cluster (same namespace, same clusterRef) whose spec.nodes selector matches
// nodeLabels; when several do, the first by name wins, the same on every call. nil, nil means
// none applies. Layer 1 (pkg/render, matched against the machine's declared nodeLabels) and
// layer 4 (internal/controller's evacuation, matched against the live Node's labels) both go
// through it so they cannot disagree about the rule, only about the label source.
func Select(
	cluster *ichorv1alpha1.TalosCluster, storages []ichorv1alpha1.TalosStorage, nodeLabels map[string]string,
) (*ichorv1alpha1.TalosStorage, error) {
	var first *ichorv1alpha1.TalosStorage

	for i := range storages {
		s := &storages[i]
		if s.Namespace != cluster.Namespace || s.Spec.ClusterRef.Name != cluster.Name {
			continue
		}

		sel, err := metav1.LabelSelectorAsSelector(&s.Spec.Nodes)
		if err != nil {
			return nil, fmt.Errorf("parsing TalosStorage %q's node selector: %w", s.Name, err)
		}

		if sel.Matches(labels.Set(nodeLabels)) && (first == nil || s.Name < first.Name) {
			first = s
		}
	}

	return first, nil
}

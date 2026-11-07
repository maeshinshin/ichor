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

package reclaim

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// LoadKnown lists the identities Ichor already manages: every TalosMachine's
// UUID, and every cluster's ID from its secrets bundle (read in ichorNamespace).
// The CLI and the controller both call it.
func LoadKnown(ctx context.Context, c client.Reader, ichorNamespace string) (Known, error) {
	var k Known

	var machines ichorv1alpha1.TalosMachineList
	if err := c.List(ctx, &machines); err != nil {
		return k, fmt.Errorf("listing TalosMachines: %w", err)
	}

	for i := range machines.Items {
		// A pending or rejected machine was made from a node seen in maintenance mode; for a node
		// that is configured now it is stale and must not stop a reclaim.
		if u := machines.Items[i].Spec.Identity.UUID; u != "" && machines.Items[i].Spec.IsApproved() {
			k.MachineUUIDs = append(k.MachineUUIDs, u)
		}
	}

	var clusters ichorv1alpha1.TalosClusterList
	if err := c.List(ctx, &clusters); err != nil {
		return k, fmt.Errorf("listing TalosClusters: %w", err)
	}

	for i := range clusters.Items {
		b, err := secretsbundle.Load(ctx, c, ichorNamespace, clusters.Items[i].Spec.SecretsRef.Name)
		if err != nil {
			return k, fmt.Errorf("reading the cluster ID of %s: %w", clusters.Items[i].Name, err)
		}

		if b.Cluster != nil && b.Cluster.ID != "" {
			k.ClusterIDs = append(k.ClusterIDs, b.Cluster.ID)

			if k.ClusterNames == nil {
				k.ClusterNames = map[string]string{}
			}

			k.ClusterNames[b.Cluster.ID] = clusters.Items[i].Name
		}
	}

	return k, nil
}

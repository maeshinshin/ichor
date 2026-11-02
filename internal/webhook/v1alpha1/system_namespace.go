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

package v1alpha1

import (
	"fmt"
	"slices"
)

// systemNamespaces are where Kubernetes keeps its own Leases (control-plane leader election in
// kube-system, node heartbeats in kube-node-lease).
var systemNamespaces = []string{"kube-system", "kube-public", "kube-node-lease"}

// refuseSystemNamespace is the error for creating an Ichor object in a Kubernetes system namespace. The per-cluster
// lock is a Lease named after the TalosCluster, written with the
// controller's cluster-wide Lease rights: a cluster called kube-scheduler in kube-system would make
// the controller take and delete the control plane's own Lease.
func refuseSystemNamespace(kind, namespace string) error {
	if !slices.Contains(systemNamespaces, namespace) {
		return nil
	}

	return fmt.Errorf("a %s cannot live in the Kubernetes system namespace %q: Ichor keeps per-cluster locks "+
		"as Leases there; use another namespace", kind, namespace)
}

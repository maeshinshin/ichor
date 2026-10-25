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

// Package deleteguard holds the deletion-protection check shared by every
// place that can delete a TalosCluster or a TalosMachine: the webhook and,
// later, the UI backend (design principle 10).
package deleteguard

import "fmt"

// AllowDeleteAnnotation, set to AllowDeleteValue, permits deleting a
// TalosCluster or TalosMachine. Its absence stops an Argo CD prune from
// cascading into node resets.
const AllowDeleteAnnotation = "ichor.maesh.dev/allow-delete"

// AllowDeleteValue is the only AllowDeleteAnnotation value that permits deletion.
const AllowDeleteValue = "true"

// CheckDelete returns an error unless annotations carries
// AllowDeleteAnnotation set to AllowDeleteValue.
func CheckDelete(annotations map[string]string) error {
	if annotations[AllowDeleteAnnotation] == AllowDeleteValue {
		return nil
	}
	return fmt.Errorf("delete refused: set the annotation %s=%q to allow it", AllowDeleteAnnotation, AllowDeleteValue)
}

// CheckControlPlaneQuorum refuses deleting a control-plane TalosMachine when
// doing so would leave etcd without quorum or without any control plane at
// all. currentControlPlanes counts every control-plane TalosMachine the
// cluster has right now, including the one being deleted; the caller (the
// webhook today, a UI backend later -- design principle 10) does the
// counting and passes only the number in, so this stays a pure function like
// CheckDelete. Talos itself enforces neither rule (docs/design/architecture.md,
// "Nodes"), and this check is stronger than CheckDelete: the allow-delete
// annotation does not override it.
//
// etcd quorum for n members is (n/2)+1. Removing one member from n leaves
// n-1, which only fails to keep that quorum when n is 1 or 2 -- from n=3
// upward, n-1 always meets (n/2)+1. n=1 is deliberately not special-cased:
// the result (n-1=0) always triggers "last control plane" below, so a
// single-node cluster's only control plane cannot be deleted this way either;
// removing the whole cluster goes through deleting the TalosCluster instead.
func CheckControlPlaneQuorum(currentControlPlanes int) error {
	after := currentControlPlanes - 1
	if after < 1 {
		return fmt.Errorf("delete refused: last control plane (%d -> %d); delete the TalosCluster instead",
			currentControlPlanes, after)
	}
	if quorum := currentControlPlanes/2 + 1; after < quorum {
		return fmt.Errorf("delete refused: control planes %d -> %d, below etcd quorum of %d for %d members",
			currentControlPlanes, after, quorum, currentControlPlanes)
	}
	return nil
}

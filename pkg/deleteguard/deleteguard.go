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

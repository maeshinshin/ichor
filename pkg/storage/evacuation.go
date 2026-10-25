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

import "time"

// EvacuationHook is the storage-system side of the design's shared
// node-operation hook (docs/design/architecture.md, "Storage"): mark a
// node (or one disk) unschedulable and request its evacuation, and later
// undo that. It makes no cluster call itself, the same restriction Preset
// has: the caller (internal/controller) reads and writes the storage
// system's own resources and applies the values this returns.
type EvacuationHook interface {
	// BeginEvacuation returns the (allowScheduling, evictionRequested)
	// values to write to the node's (or, for one disk, that disk's) spec
	// -- in that order, as two separate writes, never one combined
	// patch. For longhorn-v1, Longhorn's own admission webhook rejects a
	// write that leaves evictionRequested=true while allowScheduling is
	// still true (longhorn-manager v1.9.1,
	// webhook/resources/node/validator.go; see ADR 0025).
	BeginEvacuation() (allowScheduling, evictionRequested bool)

	// EndEvacuation is BeginEvacuation's inverse, written once the
	// caller is done with the node or disk -- whether evacuation
	// completed (EvacuationStatus returned EvacuationDone) or was
	// paused on timeout (EvacuationPaused).
	EndEvacuation() (allowScheduling, evictionRequested bool)
}

// NewEvacuationHook returns the EvacuationHook implementation for
// diskPreset, the same value TalosStorage.spec.diskPreset's enum allows.
// Every Preset is also an EvacuationHook (longhornV1 implements both);
// this is the discoverable, NewPreset-shaped way to reach the hook half
// without requiring the caller to know that and type-assert.
func NewEvacuationHook(diskPreset string) (EvacuationHook, error) {
	p, err := NewPreset(diskPreset)
	if err != nil {
		return nil, err
	}

	return p.(EvacuationHook), nil
}

// EvacuationProgress is what EvacuationStatus reports about an
// in-progress evacuation.
type EvacuationProgress int

const (
	// EvacuationWaiting means replicas still remain and the timeout has
	// not been reached; keep waiting before draining.
	EvacuationWaiting EvacuationProgress = iota

	// EvacuationDone means no replica lives only on the node or disk
	// being evacuated; it is safe to proceed to drain.
	EvacuationDone

	// EvacuationPaused means the timeout elapsed with replicas still
	// remaining. The design (architecture.md, "Storage") requires
	// pausing here, never proceeding to drain: draining a node that
	// still holds a replica's only copy destroys it.
	EvacuationPaused
)

// EvacuationStatus reports whether it is safe to proceed to drain, given
// how many of the storage system's own replica-shaped resources the
// caller already found still scheduled on the node or disk being
// evacuated (replicasRemaining), and how long the caller has been waiting
// (elapsed) against TalosStorage.spec.evacuationTimeout (timeout). For
// longhorn-v1, "a replica lives there" means a replicas.longhorn.io object
// whose spec.nodeID (or spec.diskID, for one disk) still names it --
// verified against longhorn-manager v1.9.1's volume controller
// (cleanupEvictionRequestedReplicas): it deletes a Replica once a healthy
// replacement exists elsewhere, which is the real completion signal, not a
// counter field going to zero (see ADR 0025).
//
// This decision does not vary by preset -- only what to write
// (EvacuationHook) does -- so it is a function, not an interface method.
func EvacuationStatus(replicasRemaining int, elapsed, timeout time.Duration) EvacuationProgress {
	switch {
	case replicasRemaining == 0:
		return EvacuationDone
	case elapsed >= timeout:
		return EvacuationPaused
	default:
		return EvacuationWaiting
	}
}

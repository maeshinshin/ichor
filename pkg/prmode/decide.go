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

package prmode

// Mode is the outcome of Decide: whether a caller should write a CR
// directly, go through SubmitPR, or do neither.
type Mode int

const (
	// ModeDirectWrite means the CR is not tracked by anything: the UI or
	// CLI writes it directly, the same as always.
	ModeDirectWrite Mode = iota

	// ModeOpenPR means the CR is tracked by Argo CD and a repository
	// connection exists: architecture.md's "GitOps and Argo CD" --
	// "If a CR is tracked by Argo CD and a repository connection exists,
	// the UI and CLI don't write it ... open a PR". SubmitPR is the
	// path for this.
	ModeOpenPR

	// ModeReadOnly means the CR must not be written directly, and
	// SubmitPR has no path for it either -- either Argo CD tracks it but
	// no repository connection exists (architecture.md: "Without a
	// repository connection the CR is read-only with an export button"),
	// or Flux tracks it. Flux has no Application-shaped object with a
	// single spec.source PR mode can locate a file through, so a Flux-tracked CR is read-only here
	// too, by construction rather than by a special case: this is simply
	// every outcome that is neither "not tracked" nor "Argo CD with a
	// connection".
	ModeReadOnly
)

// Decide is PR mode's write-vs-PR gate: given what
// pkg/gitopstracking.IsTracked already reported for a CR (trackedBy is ""
// when not tracked, otherwise "Argo CD" or "Flux") and whether the caller
// has a repository connection configured for it, it reports what the
// caller may do. It makes no decision architecture.md does not already
// state outright -- see Mode's own doc comments for the exact source of
// each branch.
//
// Decide takes no client.Client and does no I/O: the caller already ran
// IsTracked (which does) and already knows its own repository-connection
// state, so this stays the one place in pkg/prmode with no new
// dependency -- pure boolean logic over an answer computed elsewhere.
func Decide(trackedBy string, hasRepoConnection bool) Mode {
	switch {
	case trackedBy == "":
		return ModeDirectWrite
	case trackedBy == "Argo CD" && hasRepoConnection:
		return ModeOpenPR
	default:
		return ModeReadOnly
	}
}

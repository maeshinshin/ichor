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

// Package gitopstracking answers one question -- "is this object already
// tracked by Argo CD or Flux?" -- for every caller that needs it:
// `internal/cli`'s `gitops export` (a warning: an unapproved, tracked
// TalosMachine is surprising enough to flag even though export already
// leaves it out) and PR mode's write-vs-PR decision
// (`pkg/prmode.Decide`, docs/adr/0044): a tracked CR must never be written
// to directly, so this package's answer gates a write, not just a log
// line.
//
// IsTracked was moved here from `internal/cli/gitops_tracking.go` (which
// now calls this package instead of keeping its own copy) rather than
// duplicated, following docs/adr/0033's precedent for a rule more than
// one caller needs to agree on.
package gitopstracking

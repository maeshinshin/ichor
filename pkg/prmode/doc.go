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

// Package prmode is the file-finding and byte-preserving YAML edit half
// of PR mode (docs/design/architecture.md: "they find the file via the
// Application's source, edit the right node of the YAML while
// preserving comments and formatting ... Plain YAML and Kustomize
// sources are supported; Helm and Jsonnet sources get a diff to apply by
// hand").
//
// This package deliberately stops short of PR mode itself: opening a PR
// (a host-specific API call -- GitHub, GitLab, Gitea/Forgejo) and the UI
// following one are both out of scope. Nothing here
// talks to a repository host, clones a repository, or connects to
// anything: Locate walks an already-checked-out directory the caller
// provides, and Patch/Diff operate on bytes already in hand.
package prmode

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

// Package installmanifest carries Ichor's own install manifest --
// everything `make build-installer` writes to dist/install.yaml (CRDs,
// RBAC, the controller Deployment) -- into the `ichor` binary, so `ichor
// init` can apply it once the new cluster's kubeconfig is in hand,
// without fetching anything over the network (docs/design/
// architecture.md's "Bootstrap" step 3; Ichor itself is applied after
// bootstrap, through the API server, where a failure is a normal,
// visible error instead of something buried in Talos's boot log).
//
// Manifest is dist/install.yaml's bytes when the binary was built with
// `make build-ichor-embed` (-tags installembed), which embeds it from
// internal/installmanifest/install.yaml -- gitignored, like
// dist/install.yaml itself, since it is a build artifact derived from
// the same kustomize base. A plain `go build ./...`, with no tag and no
// prior `make` step, still compiles: it embeds the committed
// placeholder.yaml instead. The two build-tag variants live in
// manifest_embed.go and manifest_dev.go, mirroring cmd/ichor-ui's
// uiembed/placeholder pattern exactly.
//
// Because both the real manifest and the ichor binary that embeds it
// come out of the same `go build` invocation, the controller version
// `ichor init` installs is always the version of Ichor this CLI itself
// is -- there is no separate fetch step where the two could drift, and
// no version flag for a caller to get wrong. This same property keeps the
// install self-contained: the binary already carries
// everything it installs.
package installmanifest

// PlaceholderSentinel appears in placeholder.yaml. Any caller applying
// Manifest must refuse if it contains this string: it means the binary
// was built without the installembed tag, so Manifest is not a real,
// applicable install manifest.
const PlaceholderSentinel = "ichor-install-manifest-placeholder: true"

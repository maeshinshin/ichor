//go:build !installembed

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

package installmanifest

import _ "embed"

// This build (the default, no -tags installembed) embeds a placeholder
// instead of the real manifest, so `go build`, `go vet` and
// golangci-lint keep working on a plain checkout with no prior `make`
// step. Any caller that applies Manifest must check for
// PlaceholderSentinel first and refuse -- this is never meant to be
// applied to a real cluster.
//
//go:embed placeholder.yaml
var Manifest []byte

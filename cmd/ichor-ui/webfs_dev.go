//go:build !uiembed

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

package main

import "embed"

// This build (the default, no -tags uiembed) embeds a static placeholder
// instead of the real SPA, so `go build`, `go vet` and golangci-lint keep
// working for anyone who never runs `make ui-build`. The real
// bundle is embedded by webfs_embed.go once the "uiembed" tag is set.
//
//go:embed placeholder
var placeholderFS embed.FS

var webFS = mustSub(placeholderFS, "placeholder")

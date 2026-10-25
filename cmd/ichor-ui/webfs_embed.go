//go:build uiembed

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

// `make ui-build` writes the Vite build straight into ./dist (see
// ui/vite.config.ts build.outDir); `make build-ichor-ui` builds this file
// in with -tags uiembed so the real SPA ships instead of the placeholder.
//
//go:embed dist
var distFS embed.FS

var webFS = mustSub(distFS, "dist")

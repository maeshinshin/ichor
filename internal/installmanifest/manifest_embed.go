//go:build installembed

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

// `make build-ichor-embed` copies dist/install.yaml here (see the
// Makefile) before building this file in with -tags installembed, so
// the real manifest ships instead of the placeholder.
//
//go:embed install.yaml
var Manifest []byte

// NetworkPolicies is dist/network-policies.yaml (config/cilium-network-policy, ADR 0100): Ichor's
// CiliumNetworkPolicies, kept out of Manifest because their CRD exists only once Cilium runs.
//
//go:embed network-policies.yaml
var NetworkPolicies []byte

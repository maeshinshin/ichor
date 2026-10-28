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

// Package policies embeds the CiliumNetworkPolicies of the kokumi and argocd
// namespaces. The renderers in the kokumi and argocd
// packages append them to their bundle; pkg/stack exposes them on their own.
package policies

import _ "embed"

// Kokumi holds the policies of the kokumi namespace, as a YAML stream.
//
//go:embed kokumi.yaml
var Kokumi []byte

// Argocd holds the policies of the argocd namespace, as a YAML stream.
//
//go:embed argocd.yaml
var Argocd []byte

// Append adds a policy stream to the end of a rendered manifest stream. Each
// stream is a document sequence whose documents start with "---" or, for the
// first one, directly.
func Append(manifest, stream []byte) []byte {
	out := append([]byte{}, manifest...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}

	out = append(out, "---\n"...)

	return append(out, stream...)
}

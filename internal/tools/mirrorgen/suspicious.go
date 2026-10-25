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

import (
	"slices"
	"strings"
)

// suspiciousKeywords are substrings (checked case-insensitively against a
// field's Go name and its YAML/JSON name) that tend to show up in fields
// carrying a secret. This is a safety net for exactly the class of miss
// that shipped three times in a row -- EncryptionKey.KeyStatic silently
// surviving under a renamed mirror struct, then
// ClusterSecretboxEncryptionSecret missing a sibling entry its own
// AESCBC-named sibling had, then four credential fields (registry auth,
// cloud API tokens, a WireGuard private key) that were never flagged at
// all because nothing was looking for them until a human went looking by
// hand. It reports, rather than drops: a false positive here is a line in
// `go generate`'s output a human reads once, not a field silently removed
// from the CRD because its name happened to contain "key".
var suspiciousKeywords = []string{"secret", "token", "key", "password", "passphrase", "privatekey"}

// suspiciousField reports whether name or jsonName contains one of
// suspiciousKeywords, case-insensitively.
func suspiciousField(name, jsonName string) bool {
	lowerName, lowerJSON := strings.ToLower(name), strings.ToLower(jsonName)

	for _, kw := range suspiciousKeywords {
		if strings.Contains(lowerName, kw) || strings.Contains(lowerJSON, kw) {
			return true
		}
	}

	return false
}

// suspiciousSurvivors lists every field that made it into the mirror (so,
// not already dropped by secretFields or the meta.Unstructured rule) whose
// name suggests it carries a secret, for g.report() to print. It is
// deliberately not used to drop anything on its own -- see the package
// doc on suspiciousKeywords for why -- and is checked against secretFields
// by the same "machinery struct name + Go field name" key secretFields
// itself is matched on (structDef.machName, not the possibly-renamed
// mirror name), for the same reason that match has to use the machinery
// name: a field already in secretFields, under a since-renamed struct,
// must not reappear here as if it were new.
func (g *generator) suspiciousSurvivors() []string {
	var out []string

	for _, def := range g.order {
		for _, f := range def.fields {
			if !suspiciousField(f.name, f.jsonName) {
				continue
			}

			if _, ok := secretFields[def.machName+"."+f.name]; ok {
				continue
			}

			out = append(out, def.machName+"."+f.name+" (yaml \""+f.jsonName+"\")")
		}
	}

	slices.Sort(out)

	return out
}

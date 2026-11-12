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
// hand. It never drops anything by itself: a false positive is a line in
// reviewedSurvivors with a reason, not a field silently removed from the CRD
// because its name happened to contain "key". It does fail
// TestSuspiciousSurvivorsAreReviewed, so a new match cannot ship unread.
var suspiciousKeywords = []string{"secret", "token", "key", "password", "passphrase", "privatekey",
	"credential", "auth", "header", "bearer", "cookie"}

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

// reviewedSurvivors lists the fields suspiciousKeywords matches that a human
// read and judged not to carry a credential, keyed "machinery struct.Go
// field" like secretFields, with the reason. Adding a row is the review: a
// new match fails TestSuspiciousSurvivorsAreReviewed until it is either
// dropped through secretFields or listed here, and a row whose field no
// longer exists fails the same test so the list cannot rot.
var reviewedSurvivors = map[string]string{
	"APIServerConfig.AuthorizationConfigConfig":                     "authorizer list; a webhook authorizer names a kubeconfig file path on the node, not its contents",
	"AuthorizationConfigAuthorizerConfig.AuthorizerName":            "authorizer name",
	"AuthorizationConfigAuthorizerConfig.AuthorizerType":            "authorizer type, an enum",
	"AuthorizationConfigAuthorizerConfig.AuthorizerWebhook":         "webhook authorizer settings; the kubeconfig is a file path on the node, not its contents",
	"Bond.BondADUserPortKey":                                        "LACP aggregator port key, a number",
	"BondConfigV1Alpha1.BondADUserPortKey":                          "LACP aggregator port key, a number",
	"DeviceWireguardPeer.WireguardPublicKey":                        "WireGuard public key, not secret",
	"EncryptionConfig.EncryptionKeySize":                            "cipher key size in bits, a number",
	"EncryptionConfig.EncryptionKeys":                               "list of key sources; the static passphrase source is dropped (EncryptionKey.KeyStatic), the rest carry no secret",
	"EncryptionSpec.EncryptionKeySize":                              "cipher key size in bits, a number",
	"EncryptionSpec.EncryptionKeys":                                 "list of key sources; the static passphrase source is dropped (EncryptionKey.KeyStatic), the rest carry no secret",
	"EncryptionKey.KeyKMS":                                          "KMS endpoint URL; the key stays in the KMS",
	"EncryptionKey.KeyLockToSTATE":                                  "boolean",
	"EncryptionKey.KeyNodeID":                                       "empty marker; the key is derived on the node",
	"EncryptionKey.KeySlot":                                         "key slot number",
	"EncryptionKey.KeyTPM":                                          "TPM-sealed key source; options only, the key stays in the TPM",
	"FeaturesConfig.ApidCheckExtKeyUsage":                           "boolean about certificate extended key usage",
	"KubeAuthorizerConfigV1Alpha1.AuthorizerType":                   "authorizer type, an enum (Node, RBAC, Webhook)",
	"KubeAuthorizerConfigV1Alpha1.AuthorizerWebhook":                "webhook authorizer settings; the kubeconfig is a file path on the node, not its contents",
	"KubeCredentialProviderConfigV1Alpha1.CredentialProviderConfig": "lists credential provider plugins (name, image matches, command, args, env); the plugin fetches the credentials at pull time",
	"KubeletConfig.KubeletCredentialProviderConfig":                 "lists credential provider plugins (name, image matches, command, args, env); the plugin fetches the credentials at pull time",
	"ImageKeylessVerifierV1Alpha1.KeylessIssuer":                    "OIDC issuer URL an image signature must name; a public matcher",
	"ImageKeylessVerifierV1Alpha1.KeylessSubject":                   "signer identity (email or URI) an image signature must name; a public matcher",
	"ImageKeylessVerifierV1Alpha1.KeylessSubjectRegex":              "pattern for the signer identity; a public matcher",
	"ImageVerificationRuleV1Alpha1.RuleKeylessVerifier":             "keyless verifier settings (issuer and subject matchers); no secret",
	"ImageVerificationRuleV1Alpha1.RulePublicKeyVerifier":           "verifier holding a public key; the private half is the signer's and never in a config",
	"RegistryConfig.RegistryAuth":                                   "holds only the username once the password, auth and identityToken members are dropped",
}

// survivor is one field of the mirror that suspiciousKeywords matches.
type survivor struct {
	key  string // "machinery struct.Go field", as secretFields and reviewedSurvivors are keyed
	yaml string
}

// suspiciousFields lists every field that made it into the mirror (so, not
// already dropped by secretFields or the meta.Unstructured rule) whose name
// matches suspiciousKeywords, one entry per key, sorted. A key is "machinery
// struct name + Go field name" (structDef.machName, not the possibly-renamed
// mirror name), the same key secretFields is matched on, for the same reason:
// a field already in secretFields, under a since-renamed struct, must not
// reappear here as if it were new.
func (g *generator) suspiciousFields() []survivor {
	var out []survivor

	for _, def := range g.order {
		for _, f := range def.fields {
			key := def.machName + "." + f.name
			if _, ok := secretFields[key]; ok || !suspiciousField(f.name, f.jsonName) {
				continue
			}

			out = append(out, survivor{key, f.jsonName})
		}
	}

	slices.SortFunc(out, func(a, b survivor) int { return strings.Compare(a.key, b.key) })

	return slices.CompactFunc(out, func(a, b survivor) bool { return a.key == b.key })
}

// suspiciousSurvivors is suspiciousFields without the reviewedSurvivors, for
// g.report() to print and TestSuspiciousSurvivorsAreReviewed to fail on.
func (g *generator) suspiciousSurvivors() []survivor {
	return slices.DeleteFunc(g.suspiciousFields(), func(s survivor) bool {
		_, ok := reviewedSurvivors[s.key]

		return ok
	})
}

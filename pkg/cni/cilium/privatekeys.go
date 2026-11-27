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

package cilium

import (
	"encoding/base64"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/maeshinshin/ichor/pkg/values"
)

// PrivateKeyPaths are the chart values that hold private key material: a string leaf named "key" that sits
// beside a string leaf named "cert" in the chart's own defaults (tls.ca, hubble.tls.server, ...). Settings that
// only name a Secret entry (tls.caBundle.key, k8sServiceHostRef.key) have no "cert" sibling. Derived, so a
// chart upgrade that adds one is covered; "Secrets are referenced, never embedded"
// (docs/design/architecture/principles.md).
func PrivateKeyPaths() ([]string, error) {
	ch, err := loadChart()
	if err != nil {
		return nil, err
	}

	var paths []string

	var walk func(prefix string, m map[string]any)

	walk = func(prefix string, m map[string]any) {
		_, hasCert := m["cert"].(string)
		if _, isKey := m["key"].(string); isKey && hasCert && prefix != "" {
			paths = append(paths, prefix+".key")
		}

		for k, v := range m {
			if sub, ok := v.(map[string]any); ok {
				p := k
				if prefix != "" {
					p = prefix + "." + k
				}

				walk(p, sub)
			}
		}
	}

	walk("", ch.Values)
	slices.Sort(paths)

	return paths, nil
}

// EmbeddedPrivateKeys are the private-key paths of PrivateKeyPaths that user sets to a non-empty value, and
// the key material found anywhere else in user (see userKeyMaterial).
func EmbeddedPrivateKeys(user map[string]any) ([]string, error) {
	m, err := EmbeddedPrivateKeyValues(user)
	if err != nil {
		return nil, err
	}

	return slices.Sorted(maps.Keys(m)), nil
}

// EmbeddedPrivateKeyValues is EmbeddedPrivateKeys with the value at each path, so a caller can tell a key that
// is unchanged from one that is new or changed, including a key inside a list.
func EmbeddedPrivateKeyValues(user map[string]any) (map[string]any, error) {
	all, err := PrivateKeyPaths()
	if err != nil {
		return nil, err
	}

	set := map[string]any{}

	for _, p := range all {
		if v, ok := values.Get(user, p); ok && v != nil && v != "" {
			set[p] = v
		}
	}

	// The chart's defaults do not reach list-typed values (clustermesh.config.clusters, whose entries carry
	// tls.key), so the user's own values are walked as well.
	maps.Copy(set, userKeyMaterial("", user))

	return set, nil
}

// userKeyMaterial walks maps and lists in v: a non-empty "key" string beside a "cert" or "caCert" string, or
// any string that is a PEM private key (raw or base64), is key material. List entries are named name[i].
func userKeyMaterial(prefix string, v any) map[string]any {
	out := map[string]any{}

	switch t := v.(type) {
	case map[string]any:
		_, hasCert := t["cert"].(string)
		_, hasCA := t["caCert"].(string)

		for k, c := range t {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}

			if s, ok := c.(string); ok && s != "" && (k == "key" && (hasCert || hasCA) || isPEMPrivateKey(s)) {
				out[p] = s
				continue
			}

			maps.Copy(out, userKeyMaterial(p, c))
		}
	case []any:
		for i, c := range t {
			p := fmt.Sprintf("%s[%d]", prefix, i)
			if s, ok := c.(string); ok && isPEMPrivateKey(s) {
				out[p] = s
				continue
			}

			maps.Copy(out, userKeyMaterial(p, c))
		}
	}

	return out
}

func isPEMPrivateKey(s string) bool {
	if strings.Contains(s, "PRIVATE KEY-----") {
		return true
	}

	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))

	return err == nil && strings.Contains(string(b), "PRIVATE KEY-----")
}

// privateKeyAdvice names what the chart offers in place of the embedded key: the sibling existingSecret where
// the chart has one, else (tls.ca) the chart's own generation, hubble.tls.auto.method.
func privateKeyAdvice(path string) string {
	// A peer's tls.cert and tls.key are deprecated in the chart: with a CA shared across the clusters they are
	// omitted, and the chart uses the "remote" certificate it generates (the clustermesh-apiserver-remote-cert
	// Secret).
	if strings.HasPrefix(path, "clustermesh.config.clusters") && strings.HasSuffix(path, ".tls.key") {
		return "remove the peer's tls.cert and tls.key (and tls.caCert if the CA is shared): the chart then uses the " +
			"remote certificate it generates when clustermesh.useAPIServer is true and " +
			"clustermesh.apiserver.tls.auto.enabled is true"
	}

	if !slices.Contains(chartKeyPaths(), path) {
		return "move the key into a Secret and reference that Secret; check the chart for the Secret option of that setting"
	}

	parent := strings.TrimSuffix(path, ".key")
	if _, ok := values.Get(chartDefaults(), parent+".existingSecret"); ok {
		return "set " + parent + ".existingSecret to the name of a Secret that holds the certificate and key"
	}

	return "remove it and let the chart generate the CA (hubble.tls.auto.method: helm, cronJob or certmanager)"
}

func chartKeyPaths() []string {
	p, _ := PrivateKeyPaths()

	return p
}

func chartDefaults() map[string]any {
	ch, err := loadChart()
	if err != nil {
		return nil
	}

	return ch.Values
}

// checkPrivateKeys refuses each private key in user that is not in kept (the keys an existing object already
// carried unchanged).
func checkPrivateKeys(user map[string]any, kept []string) error {
	set, err := EmbeddedPrivateKeys(user)
	if err != nil {
		return err
	}

	var msgs []string

	for _, p := range set {
		if !slices.Contains(kept, p) {
			msgs = append(msgs, fmt.Sprintf(
				"%s holds a private key, and Secrets are referenced, never embedded in a TalosCluster: %s",
				p, privateKeyAdvice(p)))
		}
	}

	if len(msgs) == 0 {
		return nil
	}

	return fmt.Errorf("%s", strings.Join(msgs, "\n"))
}

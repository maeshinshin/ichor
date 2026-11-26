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
	"fmt"
	"slices"
	"strings"

	"github.com/maeshinshin/ichor/pkg/values"
)

// PrivateKeyPaths are the chart values that hold private key material: a string leaf named "key" that sits
// beside a string leaf named "cert" in the chart's own defaults (tls.ca, hubble.tls.server, ...). Settings that
// only name a Secret entry (tls.caBundle.key, k8sServiceHostRef.key) have no "cert" sibling. Derived, so a
// chart upgrade that adds one is covered; "Secrets are referenced, never embedded" (docs/design/architecture/principles.md).
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

// EmbeddedPrivateKeys are the private-key paths of PrivateKeyPaths that user sets to a non-empty value.
func EmbeddedPrivateKeys(user map[string]any) ([]string, error) {
	all, err := PrivateKeyPaths()
	if err != nil {
		return nil, err
	}

	var set []string

	for _, p := range all {
		if v, ok := values.Get(user, p); ok && v != nil && v != "" {
			set = append(set, p)
		}
	}

	return set, nil
}

// privateKeyAdvice names what the chart offers in place of the embedded key: the sibling existingSecret where
// the chart has one, else (tls.ca) the chart's own generation, hubble.tls.auto.method.
func privateKeyAdvice(path string) string {
	parent := strings.TrimSuffix(path, ".key")
	if _, ok := values.Get(chartDefaults(), parent+".existingSecret"); ok {
		return "set " + parent + ".existingSecret to the name of a Secret that holds the certificate and key"
	}

	return "remove it and let the chart generate the CA (hubble.tls.auto.method: helm, cronJob or certmanager)"
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
			msgs = append(msgs, fmt.Sprintf("%s holds a private key, and Secrets are referenced, never embedded in a TalosCluster: %s",
				p, privateKeyAdvice(p)))
		}
	}

	if len(msgs) == 0 {
		return nil
	}

	return fmt.Errorf("%s", strings.Join(msgs, "\n"))
}

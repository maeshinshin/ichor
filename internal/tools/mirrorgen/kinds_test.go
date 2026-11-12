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
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/go/packages"
)

var loadRegistry = sync.OnceValues(func() (map[string]registered, error) {
	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedSyntax | packages.NeedDeps | packages.NeedImports,
	}, pkgTypesTree)
	if err != nil {
		return nil, err
	}

	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			return nil, p.Errors[0]
		}
	}

	return scanRegistry(pkgs)
})

// talosRegistry is every registry.Register call in Talos's config/types tree, keyed by kind.
func talosRegistry(t *testing.T) map[string]registered {
	t.Helper()

	reg, err := loadRegistry()
	if err != nil {
		t.Fatalf("reading Talos's kind registry: %v", err)
	}

	return reg
}

func registeredKinds(t *testing.T) []string {
	t.Helper()

	return slices.Sorted(maps.Keys(talosRegistry(t)))
}

// TestEveryRegisteredKindIsClassified fails when Talos registers a document
// kind that kindClasses has no row for (a Talos upgrade added one), or when a
// row names a kind Talos no longer registers.
func TestEveryRegisteredKindIsClassified(t *testing.T) {
	var unclassified, stale []string

	registered := registeredKinds(t)

	for _, k := range registered {
		if _, ok := kindClasses[k]; !ok {
			unclassified = append(unclassified, k)
		}
	}

	for k, c := range kindClasses {
		if !slices.Contains(registered, k) {
			stale = append(stale, k)
		}

		switch c.class {
		case accept, secret, ichorOwned, unstructured:
		default:
			t.Errorf("kindClasses[%q]: %q is not a class", k, c.class)
		}

		if c.why == "" {
			t.Errorf("kindClasses[%q]: the reason is empty", k)
		}
	}

	slices.Sort(stale)

	if len(unclassified) > 0 {
		t.Errorf("Talos registers document kind(s) that internal/tools/mirrorgen/kinds.go has no row for: %s\n"+
			"Add one row per kind to kindClasses: accept, secret (carries credentials), ichor-owned (Ichor renders it "+
			"from a spec field) or unstructured (raw YAML), with a reason; docs/adr/0149-* defines the classes. "+
			"An accept kind goes live when "+
			"it is appended to shipped in shipped.go.", strings.Join(unclassified, ", "))
	}

	if len(stale) > 0 {
		t.Errorf("kindClasses has row(s) for kind(s) Talos no longer registers: %s\nDelete them.", strings.Join(stale, ", "))
	}
}

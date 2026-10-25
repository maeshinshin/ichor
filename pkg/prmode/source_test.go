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

package prmode

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	testArgoAPIVersion  = "argoproj.io/v1alpha1"
	testApplicationKind = "Application"
	testRepoURL         = "https://example.invalid/repo.git"
	pathKey             = "path"
	repoURLKey          = "repoURL"
)

// newApp builds a minimal Application unstructured.Unstructured with
// the given spec, the shape every test below needs.
func newApp(spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": testArgoAPIVersion,
		"kind":       testApplicationKind,
		specKey:      spec,
	}}
}

func appWithSource(source map[string]any) *unstructured.Unstructured {
	return newApp(map[string]any{"source": source})
}

// TestClassifySource_PlainPath proves a plain-manifest/Kustomize source
// (path set, no chart, no plugin) is SourceFile and its repoURL/path/
// targetRevision come back decoded.
func TestClassifySource_PlainPath(t *testing.T) {
	app := appWithSource(map[string]any{
		repoURLKey: testRepoURL, pathKey: testClustersProdPath, "targetRevision": "HEAD",
	})

	kind, src, err := ClassifySource(app)
	if err != nil {
		t.Fatalf("ClassifySource: %v", err)
	}

	if kind != SourceFile {
		t.Fatalf("kind = %v, want SourceFile", kind)
	}

	if src == nil || src.RepoURL != testRepoURL || src.Path != testClustersProdPath || src.TargetRevision != "HEAD" {
		t.Fatalf("source = %+v, want repoURL/path/targetRevision decoded", src)
	}
}

// TestClassifySource_Helm proves a chart source is SourceHelm, never
// SourceFile -- it has no single file to find.
func TestClassifySource_Helm(t *testing.T) {
	app := appWithSource(map[string]any{
		repoURLKey: "https://example.invalid/charts.git", "chart": "ichor", "targetRevision": "1.0.0",
	})

	kind, _, err := ClassifySource(app)
	if err != nil {
		t.Fatalf("ClassifySource: %v", err)
	}

	if kind != SourceHelm {
		t.Fatalf("kind = %v, want SourceHelm", kind)
	}
}

// TestClassifySource_Plugin proves a Config Management Plugin source
// (Jsonnet or otherwise) is SourcePlugin, even though it also carries a
// path -- the presence of "plugin" must win over "has a path".
func TestClassifySource_Plugin(t *testing.T) {
	app := appWithSource(map[string]any{
		repoURLKey: testRepoURL, pathKey: testClustersProdPath,
		"plugin": map[string]any{"name": "jsonnet"},
	})

	kind, _, err := ClassifySource(app)
	if err != nil {
		t.Fatalf("ClassifySource: %v", err)
	}

	if kind != SourcePlugin {
		t.Fatalf("kind = %v, want SourcePlugin", kind)
	}
}

// TestClassifySource_MultiSourceIsUnsupported proves spec.sources
// (plural) is never guessed at.
func TestClassifySource_MultiSourceIsUnsupported(t *testing.T) {
	app := newApp(map[string]any{
		"sources": []any{
			map[string]any{repoURLKey: "https://example.invalid/a.git", pathKey: "a"},
			map[string]any{repoURLKey: "https://example.invalid/b.git", pathKey: "b"},
		},
	})

	kind, src, err := ClassifySource(app)
	if err != nil {
		t.Fatalf("ClassifySource: %v", err)
	}

	if kind != SourceUnsupported || src != nil {
		t.Fatalf("kind, src = %v, %v, want SourceUnsupported, nil", kind, src)
	}
}

// TestClassifySource_NoSourceIsUnsupported proves an Application with no
// spec.source at all (should not happen in practice, but must not panic
// or be misclassified) is unsupported.
func TestClassifySource_NoSourceIsUnsupported(t *testing.T) {
	app := newApp(map[string]any{})

	kind, _, err := ClassifySource(app)
	if err != nil {
		t.Fatalf("ClassifySource: %v", err)
	}

	if kind != SourceUnsupported {
		t.Fatalf("kind = %v, want SourceUnsupported", kind)
	}
}

// TestClassifySource_PathlessIsUnsupported proves a source with neither
// a chart, a plugin, nor a path is treated conservatively, not guessed
// to be SourceFile.
func TestClassifySource_PathlessIsUnsupported(t *testing.T) {
	app := appWithSource(map[string]any{repoURLKey: testRepoURL})

	kind, _, err := ClassifySource(app)
	if err != nil {
		t.Fatalf("ClassifySource: %v", err)
	}

	if kind != SourceUnsupported {
		t.Fatalf("kind = %v, want SourceUnsupported", kind)
	}
}

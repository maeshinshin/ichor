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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// configDir and chartDir are the real repo directories this command syncs between,
// relative to this package -- run "make manifests" first if config/crd/bases is stale.
const (
	configDir = "../../../config/crd/bases"
	chartDir  = "../../../dist/chart/templates/crd"
)

// TestSyncIsIdempotent proves syncing an already-synced tree changes nothing: every
// chart CRD file's bytes are identical before and after.
func TestSyncIsIdempotent(t *testing.T) {
	entries, err := os.ReadDir(chartDir)
	if err != nil {
		t.Fatalf("reading %s: %v", chartDir, err)
	}

	before := make(map[string][]byte, len(entries))
	for _, e := range entries {
		path := filepath.Join(chartDir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		before[path] = b
	}
	if len(before) == 0 {
		t.Fatalf("%s has no files to compare", chartDir)
	}

	if err := syncDir(configDir, chartDir); err != nil {
		t.Fatalf("syncDir: %v", err)
	}

	for path, want := range before {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s changed on an already-synced tree; sync should be a no-op here", path)
		}
	}
}

// chartFixture is a minimal but realistic dist/chart/templates/crd/ file: the Helm
// wrapper every real one carries, around a tiny placeholder spec.
const chartFixture = `{{- if .Values.crd.enabled }}
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  annotations:
    {{- if .Values.crd.keep }}
    "helm.sh/resource-policy": keep
    {{- end }}
    controller-gen.kubebuilder.io/version: v0.21.0
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: OldWidget
{{- end }}
`

const configFixture = `---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  annotations:
    controller-gen.kubebuilder.io/version: v0.21.0
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: NewWidget
`

// TestSyncPropagatesAndPreservesWrapper proves a changed config/crd/bases file reaches
// the chart, and that everything outside "spec:" -- the enabled/keep templating and the
// trailing "{{- end }}" -- survives unchanged.
func TestSyncPropagatesAndPreservesWrapper(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	chtDir := filepath.Join(dir, "chart")
	for _, d := range []string{cfgDir, chtDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	configPath := filepath.Join(cfgDir, "example.com_widgets.yaml")
	chartPath := filepath.Join(chtDir, "widgets.example.com.yaml")
	if err := os.WriteFile(configPath, []byte(configFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chartPath, []byte(chartFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := syncDir(cfgDir, chtDir); err != nil {
		t.Fatalf("syncDir: %v", err)
	}

	got, err := os.ReadFile(chartPath)
	if err != nil {
		t.Fatal(err)
	}
	gotStr := string(got)

	// The new spec propagated.
	if !containsLine(gotStr, "    kind: NewWidget") {
		t.Errorf("chart file does not contain the updated spec; got:\n%s", gotStr)
	}
	if containsLine(gotStr, "    kind: OldWidget") {
		t.Errorf("chart file still has the old spec; got:\n%s", gotStr)
	}

	// The wrapper around it did not move.
	for _, want := range []string{
		"{{- if .Values.crd.enabled }}",
		`  name: widgets.example.com`,
		"    {{- if .Values.crd.keep }}",
		`    "helm.sh/resource-policy": keep`,
		"{{- end }}",
	} {
		if !containsLine(gotStr, want) {
			t.Errorf("chart wrapper line %q did not survive the sync; got:\n%s", want, gotStr)
		}
	}
}

func containsLine(text, line string) bool {
	return slices.Contains(strings.Split(text, "\n"), line)
}

// TestSyncFailsLoudly proves every assumption this command relies on -- the chart file
// already existing, and each file having exactly one top-level "spec:" line and exactly
// one trailing "{{- end }}" line -- fails the sync outright rather than silently writing
// something wrong when violated.
func TestSyncFailsLoudly(t *testing.T) {
	cases := []struct {
		name       string
		chart      string
		skipChart  bool
		wantErrSub string
	}{
		{name: "missing chart file", skipChart: true, wantErrSub: "does not exist"},
		{name: "no spec line", chart: "metadata:\n  name: x\n{{- end }}\n", wantErrSub: `"spec:" line`},
		{name: "two spec lines", chart: "spec:\nspec:\n{{- end }}\n", wantErrSub: `"spec:" line`},
		{name: "no end line", chart: "spec:\n  group: x\n", wantErrSub: `"{{- end }}" line`},
		{name: "two end lines", chart: "spec:\n{{- end }}\n{{- end }}\n", wantErrSub: `"{{- end }}" line`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "example.com_widgets.yaml")
			if err := os.WriteFile(configPath, []byte(configFixture), 0o644); err != nil {
				t.Fatal(err)
			}

			if !tc.skipChart {
				chartPath := filepath.Join(dir, "widgets.example.com.yaml")
				if err := os.WriteFile(chartPath, []byte(tc.chart), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			err := syncOne(configPath, dir)
			if err == nil {
				t.Fatalf("syncOne: expected an error for %q, got none", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("syncOne error = %q, want it to mention %q", err.Error(), tc.wantErrSub)
			}
		})
	}
}

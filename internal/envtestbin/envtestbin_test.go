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

package envtestbin

import (
	"os"
	"path/filepath"
	"testing"
)

// root builds a repository root with a go.mod body and the given bin/k8s entries; names ending in "/"
// are directories and the rest are files.
func root(t *testing.T, goMod string, entries ...string) string {
	t.Helper()

	r := t.TempDir()
	if err := os.WriteFile(filepath.Join(r, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		p := filepath.Join(r, "bin", "k8s", e)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if e[len(e)-1] == '/' {
			if err := os.MkdirAll(p, 0o750); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return r
}

const apiV036 = "module example.com/x\n\ngo 1.27.4\n\nrequire k8s.io/api v0.36.6\n"

// The lexical order puts 1.36.10 before 1.36.2; the highest patch must win, and a file is not a directory.
func TestDirPicksTheHighestPatchOfTheMakefileMinor(t *testing.T) {
	r := root(t, apiV036, "1.34.0-linux-amd64/", "1.36.2-linux-amd64/", "1.36.10-linux-amd64/", "1.36.99-linux-amd64")

	got, err := Dir(r)
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(r, "bin", "k8s", "1.36.10-linux-amd64"); got != want {
		t.Errorf("Dir = %s, want %s", got, want)
	}
}

// A 1.34 directory alone must not stand in for the 1.36 the go.mod names.
func TestDirRefusesAnotherMinor(t *testing.T) {
	r := root(t, apiV036, "1.34.0-linux-amd64/")

	if got, err := Dir(r); err == nil {
		t.Errorf("Dir = %s, want an error", got)
	}
}

// A replace with no version has no Kubernetes minor to read, as `go list -m` gives the Makefile none.
func TestDirRefusesAReplaceWithoutAVersion(t *testing.T) {
	r := root(t, apiV036+"\nreplace k8s.io/api => ../api\n", "1.36.2-linux-amd64/")

	if got, err := Dir(r); err == nil {
		t.Errorf("Dir = %s, want an error", got)
	}
}

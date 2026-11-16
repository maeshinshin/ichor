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

// Package envtestbin picks the envtest binary directory for the Kubernetes version the Makefile names.
// It is shared by the test suites that start envtest, so none of them can run a different minor.
package envtestbin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// Dir returns root/bin/k8s/<version>-<os>-<arch> for the Kubernetes minor that the Makefile's
// ENVTEST_K8S_VERSION names. The Makefile derives that from the k8s.io/api version in root/go.mod
// (v0.36.6 gives 1.36), and so does Dir. Among that minor's directories it returns the highest patch,
// as `setup-envtest use` would pick. It returns an error when there is none, so a caller never starts
// a different minor; envtest then reports that KUBEBUILDER_ASSETS is needed.
func Dir(root string) (string, error) {
	want, err := minor(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	base := filepath.Join(root, "bin", "k8s")
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", err
	}
	best, bestVer := "", ""
	for _, entry := range entries {
		ver, _, _ := strings.Cut(entry.Name(), "-")
		v := "v" + ver
		if !entry.IsDir() || semver.MajorMinor(v) != want || (best != "" && semver.Compare(v, bestVer) <= 0) {
			continue
		}
		best, bestVer = entry.Name(), v
	}
	if best == "" {
		return "", fmt.Errorf("no Kubernetes %s directory under %s", strings.TrimPrefix(want, "v"), base)
	}
	return filepath.Join(base, best), nil
}

// minor returns the minor of the k8s.io/api version in goMod, as "v1.36", following a replace the way
// `go list -m` does.
func minor(goMod string) (string, error) {
	data, err := os.ReadFile(goMod)
	if err != nil {
		return "", err
	}
	f, err := modfile.Parse(goMod, data, nil)
	if err != nil {
		return "", err
	}
	var v string
	for _, r := range f.Require {
		if r.Mod.Path == "k8s.io/api" {
			v = r.Mod.Version
		}
	}
	for _, r := range f.Replace {
		if r.Old.Path == "k8s.io/api" {
			v = r.New.Version
		}
	}
	if !semver.IsValid(v) {
		return "", fmt.Errorf("k8s.io/api in %s has no version to read the Kubernetes minor from (%q)", goMod, v)
	}
	return "v1." + strings.TrimPrefix(semver.MajorMinor(v), "v0."), nil
}

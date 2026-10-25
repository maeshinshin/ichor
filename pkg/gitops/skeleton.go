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

package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// dirPerm and filePerm match what a Git checkout needs: readable, and
// writable by its owner only. Nothing written here is ever a secret (see
// WriteObject), but there is no reason to make it group/world-writable.
const (
	dirPerm  os.FileMode = 0o755
	filePerm os.FileMode = 0o644
)

// Skeleton is the repository layout from docs/design/architecture.md
// ("Repository skeleton (`ichor gitops export`)"):
//
//	bootstrap/root-app.yaml      app of apps
//	apps/<name>/                 one Application per component
//	clusters/<cluster>/          Ichor CRs
//
// Which component directories exist under apps/ (argocd, cni, ichor, and the
// optional storage/monitoring only when selected) is a policy decision for
// the caller -- ichor init / ichor gitops export, neither built in this
// package -- not something Skeleton hardcodes.
type Skeleton struct {
	// Cluster names the clusters/<Cluster>/ directory.
	Cluster string

	// RootApp, if set, is written to bootstrap/root-app.yaml.
	RootApp *Application

	// Apps is written one Application per entry, to
	// apps/<key>/application.yaml. Keys are written in sorted order so the
	// result does not depend on map iteration order.
	Apps map[string]*Application

	// CRs is written one file per entry, to clusters/<Cluster>/<key>. Keys
	// are written in sorted order for the same reason.
	CRs map[string]any
}

// Write renders s under dir, creating it if necessary. The output is
// deterministic: for the same Skeleton value, byte-for-byte identical
// regardless of Go's map iteration order, because every map in Skeleton is
// walked in sorted key order before anything is written.
//
// Write never writes a Secret: WriteObject, which it calls for every file,
// refuses one. There is no field on Skeleton a repository credential or the
// secrets bundle could travel through in the first place -- the design is
// explicit that those are a Secret from the CLI, never a file in Git.
func (s Skeleton) Write(dir string) error {
	if s.Cluster == "" {
		return fmt.Errorf("gitops: skeleton: cluster name is required")
	}

	if s.RootApp != nil {
		if err := checkApplication(s.RootApp); err != nil {
			return fmt.Errorf("gitops: skeleton: root app: %w", err)
		}

		if err := WriteObject(filepath.Join(dir, "bootstrap", "root-app.yaml"), s.RootApp); err != nil {
			return fmt.Errorf("gitops: skeleton: root app: %w", err)
		}
	}

	for _, name := range sortedKeys(s.Apps) {
		app := s.Apps[name]
		if app == nil {
			return fmt.Errorf("gitops: skeleton: apps[%q] is nil", name)
		}

		if err := checkApplication(app); err != nil {
			return fmt.Errorf("gitops: skeleton: apps[%q]: %w", name, err)
		}

		path := filepath.Join(dir, "apps", name, "application.yaml")
		if err := WriteObject(path, app); err != nil {
			return fmt.Errorf("gitops: skeleton: apps[%q]: %w", name, err)
		}
	}

	for _, name := range sortedKeys(s.CRs) {
		path := filepath.Join(dir, "clusters", s.Cluster, name)
		if err := WriteObject(path, s.CRs[name]); err != nil {
			return fmt.Errorf("gitops: skeleton: clusters/%s/%s: %w", s.Cluster, name, err)
		}
	}

	return nil
}

// sortedKeys returns m's keys in sorted order, so a caller walking a map
// gets a deterministic sequence regardless of Go's randomized iteration.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

// WriteObject marshals obj as YAML and writes it to path, creating parent
// directories as needed. It refuses to write a corev1.Secret: Secrets never
// belong in the GitOps repository (docs/design/architecture.md, "What goes
// in Git" / "What doesn't").
func WriteObject(path string, obj any) error {
	if _, ok := obj.(*corev1.Secret); ok {
		return fmt.Errorf("gitops: refusing to write a Secret to the gitops repository: %s", path)
	}

	out, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("gitops: marshaling %s: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("gitops: creating directory for %s: %w", path, err)
	}

	if err := os.WriteFile(path, out, filePerm); err != nil {
		return fmt.Errorf("gitops: writing %s: %w", path, err)
	}

	return nil
}

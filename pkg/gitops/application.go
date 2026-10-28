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

// ApplicationSource is one entry of Application.spec.source(s).
type ApplicationSource struct {
	RepoURL        string                 `json:"repoURL"`
	Path           string                 `json:"path,omitempty"`
	Chart          string                 `json:"chart,omitempty"`
	TargetRevision string                 `json:"targetRevision,omitempty"`
	Helm           *ApplicationSourceHelm `json:"helm,omitempty"`
	Directory      *ApplicationSourceDir  `json:"directory,omitempty"`
}

// ApplicationSourceHelm is Application.spec.source.helm.
type ApplicationSourceHelm struct {
	ReleaseName string   `json:"releaseName,omitempty"`
	ValueFiles  []string `json:"valueFiles,omitempty"`

	// Values is an inline block of Helm values, as YAML text
	// (argo-cd.readthedocs.io, "Helm", retrieved 2026-10-25): the whole
	// values document a caller already rendered, rather than a path to one
	// in the chart's own repository.
	Values string `json:"values,omitempty"`
}

// ApplicationSourceDir is Application.spec.source.directory.
type ApplicationSourceDir struct {
	Recurse bool `json:"recurse,omitempty"`

	// Include is a glob (with {a,b} alternatives) of the files, relative to
	// the source path, the Application applies; empty means all.
	Include string `json:"include,omitempty"`
}

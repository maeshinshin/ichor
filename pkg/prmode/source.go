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
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/maeshinshin/ichor/pkg/gitops"
)

// specKey and sourceKey/sourcesKey are the Application spec fields this
// package reads, named once since both ClassifySource and its own tests
// use the same literals.
const (
	specKey    = "spec"
	sourceKey  = "source"
	sourcesKey = "sources"
)

// SourceKind classifies an Argo CD Application's source for PR mode's
// purposes: either this package can find and edit the file the CR lives
// in (SourceFile), or it cannot and the caller should fall back to a
// diff to apply by hand (docs/design/architecture.md: "Helm and Jsonnet
// sources get a diff to apply by hand").
type SourceKind int

const (
	// SourceUnsupported covers everything that is not a single,
	// plain-path source: no source at all, a multi-source Application
	// (spec.sources, plural -- which of several sources would hold the
	// CR is not something this unit guesses at), or a source this
	// function cannot otherwise classify. Treated identically to Helm
	// and Jsonnet below -- the caller must not edit in place -- so
	// "unrecognized" fails closed rather than being guessed at.
	SourceUnsupported SourceKind = iota

	// SourceFile is a plain-manifest or Kustomize source (Application
	// spec.source.path set, no chart, no plugin): the only kind this
	// package can locate a file for and edit.
	SourceFile

	// SourceHelm is a Helm chart source (spec.source.chart set): there
	// is no single YAML file holding the CR's declared form, only its
	// rendered output, so it is never edited in place.
	SourceHelm

	// SourcePlugin is a Config Management Plugin source
	// (spec.source.plugin set -- Argo CD's mechanism for a Jsonnet, or
	// any other non-native, source; architecture.md names Jsonnet
	// specifically). Same reasoning as SourceHelm: nothing to edit in
	// place.
	SourcePlugin
)

// ClassifySource reads app's spec.source (an Argo CD Application,
// argoproj.io/v1alpha1, read as unstructured.Unstructured the same way
// internal/controller's CNIHandoverReconciler does -- this project has
// no vendored Go types for a CRD it does not own) and returns its kind.
// For SourceFile, it also returns the decoded ApplicationSource (reusing
// pkg/gitops's own type rather than a second one) so the caller has
// RepoURL/Path/TargetRevision without decoding spec.source twice.
//
// spec.sources (plural, multi-source Applications) is deliberately not
// supported: resolving which of several sources would hold a given CR
// is not something architecture.md's "find the file via the Application's
// source" describes, and guessing (e.g. always "the first one") could
// silently edit the wrong repository.
func ClassifySource(app *unstructured.Unstructured) (SourceKind, *gitops.ApplicationSource, error) {
	if _, found, err := unstructured.NestedSlice(app.Object, specKey, sourcesKey); err != nil {
		return SourceUnsupported, nil, fmt.Errorf("prmode: reading spec.sources: %w", err)
	} else if found {
		return SourceUnsupported, nil, nil
	}

	srcMap, found, err := unstructured.NestedMap(app.Object, specKey, sourceKey)
	if err != nil {
		return SourceUnsupported, nil, fmt.Errorf("prmode: reading spec.source: %w", err)
	}

	if !found {
		return SourceUnsupported, nil, nil
	}

	if _, ok := srcMap["plugin"]; ok {
		return SourcePlugin, nil, nil
	}

	if chart, _ := srcMap["chart"].(string); chart != "" {
		return SourceHelm, nil, nil
	}

	raw, err := json.Marshal(srcMap)
	if err != nil {
		return SourceUnsupported, nil, fmt.Errorf("prmode: encoding spec.source: %w", err)
	}

	var src gitops.ApplicationSource
	if err := json.Unmarshal(raw, &src); err != nil {
		return SourceUnsupported, nil, fmt.Errorf("prmode: decoding spec.source: %w", err)
	}

	if src.Path == "" {
		// A Directory source always carries a path alongside it (see
		// pkg/gitops's own buildRootApplication); a source with neither a
		// chart, a plugin, nor a path is not a shape this package
		// recognizes.
		return SourceUnsupported, nil, nil
	}

	return SourceFile, &src, nil
}

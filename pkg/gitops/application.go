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
	"net/url"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ApplicationAPIVersion and ApplicationKind identify an Argo CD Application,
// per the example manifest in Argo CD's docs
// (operator-manual/application.yaml, checked against the v3.5.6 docs).
const (
	ApplicationAPIVersion = "argoproj.io/v1alpha1"
	ApplicationKind       = "Application"
)

// SyncWaveAnnotation is the annotation Argo CD reads to order resources
// within a sync (argo-cd.readthedocs.io, user-guide/sync-waves, checked
// against the v3.5.6 docs): lowest value first, default 0, negative values
// allowed.
const SyncWaveAnnotation = "argocd.argoproj.io/sync-wave"

// Sync waves for the hand-over order the design lays out ("Sync waves: Argo
// CD -> CNI -> Ichor (including CRDs) -> Ichor CRs -> storage after
// TalosStorage is healthy -> everything else").
const (
	SyncWaveArgoCD = iota
	SyncWaveCNI
	SyncWaveIchor
	SyncWaveIchorCRs
	SyncWaveStorage
	SyncWaveOther
)

// Known sync options, spelled out here so callers don't retype the magic
// strings (argo-cd.readthedocs.io, user-guide/sync-options, v3.5.6 docs).
const (
	syncOptionServerSideApply    = "ServerSideApply=true"
	syncOptionRespectIgnoreDiffs = "RespectIgnoreDifferences=true"
)

// defaultProject is the Argo CD project New falls back to when Options.Project is empty.
const defaultProject = "default"

// Application is the subset of Argo CD's Application CRD this package
// needs. See doc.go for why it is hand-rolled rather than imported.
//
// APIVersion and Kind are plain strings rather than an embedded
// metav1.TypeMeta on purpose: controller-gen's CRD generator treats any
// type in any package under paths="./..." that embeds both TypeMeta and
// ObjectMeta as one of Ichor's own CRD kinds (sigs.k8s.io/controller-tools
// pkg/crd/gen.go, FindKubeKinds -- it keys purely on the embedded field
// types, with no marker required), which produced a bogus
// "applications." CRD with an empty group and version the first time this
// was embedded the obvious way. Application is never registered with a
// runtime.Scheme or sent to an API server, so it never needed to satisfy
// runtime.Object in the first place.
type Application struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`

	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ApplicationSpec `json:"spec"`
}

// ApplicationSpec is argoproj.io/v1alpha1 Application.spec.
type ApplicationSpec struct {
	Project string `json:"project"`

	Source  *ApplicationSource  `json:"source,omitempty"`
	Sources []ApplicationSource `json:"sources,omitempty"`

	Destination ApplicationDestination `json:"destination"`

	SyncPolicy *SyncPolicy `json:"syncPolicy,omitempty"`

	// IgnoreDifferences lets a field a controller other than Argo CD owns
	// (named by its field manager) stop showing as OutOfSync. See New's doc
	// comment for the managedFieldsManagers pitfall this exists to cover.
	IgnoreDifferences []ResourceIgnoreDifference `json:"ignoreDifferences,omitempty"`
}

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

// ApplicationSourceDir is Application.spec.source.directory, used by the
// app-of-apps root Application to pick up everything under apps/.
type ApplicationSourceDir struct {
	Recurse bool `json:"recurse,omitempty"`
}

// ApplicationDestination is Application.spec.destination.
type ApplicationDestination struct {
	Server    string `json:"server,omitempty"`
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// SyncPolicy is Application.spec.syncPolicy.
type SyncPolicy struct {
	Automated   *SyncPolicyAutomated `json:"automated,omitempty"`
	SyncOptions []string             `json:"syncOptions,omitempty"`
}

// SyncPolicyAutomated is Application.spec.syncPolicy.automated.
type SyncPolicyAutomated struct {
	Prune      bool `json:"prune,omitempty"`
	SelfHeal   bool `json:"selfHeal,omitempty"`
	AllowEmpty bool `json:"allowEmpty,omitempty"`
}

// ResourceIgnoreDifference is one entry of Application.spec.ignoreDifferences.
type ResourceIgnoreDifference struct {
	Group string `json:"group,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Name  string `json:"name,omitempty"`

	JSONPointers      []string `json:"jsonPointers,omitempty"`
	JQPathExpressions []string `json:"jqPathExpressions,omitempty"`

	// ManagedFieldsManagers names the field manager(s) that own the ignored
	// fields, e.g. a CRD's own operator rather than Argo CD. This is the
	// pitfall from the design notes: ignoreDifferences only sticks on sync
	// (not just on diff) when the matching sync option below is also set,
	// which New sets automatically whenever this slice is non-empty.
	ManagedFieldsManagers []string `json:"managedFieldsManagers,omitempty"`
}

// Options configures New. Name, Project, a source and a destination are
// required; everything else is optional.
type Options struct {
	Name      string
	Namespace string // namespace of the Application resource itself, i.e. where Argo CD runs
	Project   string // defaults to "default"

	SyncWave int

	Source  *ApplicationSource
	Sources []ApplicationSource

	Destination ApplicationDestination

	// Automated, if true, sets syncPolicy.automated with Prune/SelfHeal.
	Automated bool
	Prune     bool
	SelfHeal  bool

	// ServerSideApply adds the ServerSideApply=true sync option -- needed
	// for large CRD groups per the design notes.
	ServerSideApply bool

	IgnoreDifferences []ResourceIgnoreDifference

	// ExtraSyncOptions is appended after the options New derives itself
	// (ServerSideApply=true, RespectIgnoreDifferences=true), so a caller
	// can add e.g. CreateNamespace=true without this package guessing at
	// options it has no opinion on.
	ExtraSyncOptions []string
}

// New builds an Application from opts, baking in the pitfalls the design
// notes call out:
//
//   - an IgnoreDifferences entry that names fields (jsonPointers,
//     jqPathExpressions or managedFieldsManagers) always pairs with the
//     RespectIgnoreDifferences=true sync option, because ignoreDifferences
//     is otherwise only honoured when Argo CD computes the diff for display,
//     not when it decides whether a sync is a no-op. An entry that names only
//     a group and kind selects no field of any desired manifest, so there is
//     nothing for the sync to respect and the option is not added.
//   - ServerSideApply, when requested, becomes the ServerSideApply=true sync
//     option.
//   - SyncWave becomes the argocd.argoproj.io/sync-wave annotation.
//
// It returns an error instead of building something that would leak a
// credential into Git: a RepoURL carrying embedded userinfo
// (https://user:pass@host/repo.git) is rejected, because the design is
// explicit that repo credentials are a Secret from the CLI and never go into
// Git -- including inside a URL written to a tracked Application manifest.
func New(opts Options) (*Application, error) {
	if opts.Name == "" {
		return nil, fmt.Errorf("gitops: application name is required")
	}

	sources := opts.Sources
	if opts.Source != nil {
		sources = append([]ApplicationSource{*opts.Source}, sources...)
	}

	if len(sources) == 0 {
		return nil, fmt.Errorf("gitops: application %q: at least one source is required", opts.Name)
	}

	if err := checkSources(sources); err != nil {
		return nil, fmt.Errorf("gitops: application %q: %w", opts.Name, err)
	}

	if opts.Destination.Server == "" && opts.Destination.Name == "" {
		return nil, fmt.Errorf("gitops: application %q: destination.server or destination.name is required", opts.Name)
	}

	project := opts.Project
	if project == "" {
		project = defaultProject
	}

	spec := ApplicationSpec{
		Project:           project,
		Destination:       opts.Destination,
		IgnoreDifferences: opts.IgnoreDifferences,
	}

	if opts.Source != nil {
		spec.Source = opts.Source
		spec.Sources = opts.Sources
	} else {
		spec.Sources = sources
	}

	var syncOptions []string
	if opts.ServerSideApply {
		syncOptions = append(syncOptions, syncOptionServerSideApply)
	}

	if hasFieldLevelIgnore(opts.IgnoreDifferences) {
		syncOptions = append(syncOptions, syncOptionRespectIgnoreDiffs)
	}

	syncOptions = append(syncOptions, opts.ExtraSyncOptions...)

	if opts.Automated || len(syncOptions) > 0 {
		spec.SyncPolicy = &SyncPolicy{SyncOptions: syncOptions}

		if opts.Automated {
			spec.SyncPolicy.Automated = &SyncPolicyAutomated{
				Prune:    opts.Prune,
				SelfHeal: opts.SelfHeal,
			}
		}
	}

	annotations := map[string]string{
		SyncWaveAnnotation: strconv.Itoa(opts.SyncWave),
	}

	return &Application{
		APIVersion:  ApplicationAPIVersion,
		Kind:        ApplicationKind,
		Name:        opts.Name,
		Namespace:   opts.Namespace,
		Annotations: annotations,
		Spec:        spec,
	}, nil
}

// hasFieldLevelIgnore reports whether any entry names fields of a manifest
// Argo CD applies, which is the only case RespectIgnoreDifferences changes.
func hasFieldLevelIgnore(entries []ResourceIgnoreDifference) bool {
	for _, e := range entries {
		if len(e.JSONPointers) > 0 || len(e.JQPathExpressions) > 0 || len(e.ManagedFieldsManagers) > 0 {
			return true
		}
	}

	return false
}

// checkSources rejects any source whose RepoURL carries embedded userinfo,
// which would otherwise write a credential straight into Git.
func checkSources(sources []ApplicationSource) error {
	for _, s := range sources {
		if err := checkRepoURL(s.RepoURL); err != nil {
			return err
		}
	}

	return nil
}

// checkApplication re-runs checkSources against an already-built
// Application's source(s). New already checks before it ever builds one;
// this exists so Skeleton.Write (skeleton.go) can check again for an
// Application that reached it some other way -- a literal, a different
// constructor added later -- rather than trusting that every caller went
// through New.
func checkApplication(app *Application) error {
	sources := app.Spec.Sources
	if app.Spec.Source != nil {
		sources = append([]ApplicationSource{*app.Spec.Source}, sources...)
	}

	if err := checkSources(sources); err != nil {
		return fmt.Errorf("application %q: %w", app.Name, err)
	}

	return nil
}

// checkRepoURL rejects a repository URL carrying embedded userinfo, which
// would otherwise write a credential straight into Git.
func checkRepoURL(repoURL string) error {
	if repoURL == "" {
		return fmt.Errorf("source repoURL is required")
	}

	u, err := url.Parse(repoURL)
	if err != nil {
		// Not every valid Argo CD source is a URL url.Parse accepts
		// cleanly (e.g. scp-style git@host:path); those have no "://" for
		// userinfo to hide behind, so there is nothing to catch here.
		return nil //nolint:nilerr // unparsable means "not a URL with credentials", not an error
	}

	if u.User != nil {
		return fmt.Errorf("source repoURL %q must not carry embedded credentials; use a repository Secret instead", repoURL)
	}

	return nil
}

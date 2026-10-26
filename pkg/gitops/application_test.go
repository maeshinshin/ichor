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
	"slices"
	"strconv"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

const (
	testRepoURL = "https://github.com/maeshinshin/k8s-clusters.git"
	testServer  = "https://kubernetes.default.svc"
)

func validOptions() Options {
	return Options{
		Name: "ichor-crs",
		Source: &ApplicationSource{
			RepoURL:        testRepoURL,
			Path:           "clusters/prod",
			TargetRevision: "main",
		},
		Destination: ApplicationDestination{
			Server:    testServer,
			Namespace: "ichor-system",
		},
	}
}

// TestNew_ServerSideApplyAndIgnoreDifferences checks the first named
// pitfall: a large CRD group gets server-side apply, and an
// ignoreDifferences entry keyed by managedFieldsManagers always pairs with
// RespectIgnoreDifferences=true (the CDI example from the design notes:
// cdi-operator owns spec.versions on the CRD).
func TestNew_ServerSideApplyAndIgnoreDifferences(t *testing.T) {
	opts := validOptions()
	opts.ServerSideApply = true
	opts.IgnoreDifferences = []ResourceIgnoreDifference{
		{
			Group:                 "apiextensions.k8s.io",
			Kind:                  "CustomResourceDefinition",
			Name:                  "cdis.cdi.kubevirt.io",
			ManagedFieldsManagers: []string{"cdi-operator"},
		},
	}

	app, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if app.Spec.SyncPolicy == nil {
		t.Fatal("syncPolicy is nil")
	}

	syncOpts := app.Spec.SyncPolicy.SyncOptions
	if !slices.Contains(syncOpts, syncOptionServerSideApply) {
		t.Errorf("syncOptions = %v, want it to contain %q", syncOpts, syncOptionServerSideApply)
	}

	if !slices.Contains(syncOpts, syncOptionRespectIgnoreDiffs) {
		t.Errorf("syncOptions = %v, want it to contain %q (required for\n"+
			"managedFieldsManagers to take effect on sync, not just on diff)", syncOpts, syncOptionRespectIgnoreDiffs)
	}

	if len(app.Spec.IgnoreDifferences) != 1 || app.Spec.IgnoreDifferences[0].ManagedFieldsManagers[0] != "cdi-operator" {
		t.Errorf("ignoreDifferences = %+v, want the cdi-operator managedFieldsManagers\n"+
			"entry preserved", app.Spec.IgnoreDifferences)
	}
}

// TestNew_IgnoreDifferencesWithoutServerSideApply checks that
// RespectIgnoreDifferences=true is added even when ServerSideApply is not
// requested: the two pitfalls are independent.
func TestNew_IgnoreDifferencesWithoutServerSideApply(t *testing.T) {
	opts := validOptions()
	opts.IgnoreDifferences = []ResourceIgnoreDifference{
		{Group: "*", Kind: "*", ManagedFieldsManagers: []string{"kube-controller-manager"}},
	}

	app, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if app.Spec.SyncPolicy == nil || !slices.Contains(app.Spec.SyncPolicy.SyncOptions, syncOptionRespectIgnoreDiffs) {
		t.Errorf("syncOptions = %+v, want RespectIgnoreDifferences=true", app.Spec.SyncPolicy)
	}

	if slices.Contains(app.Spec.SyncPolicy.SyncOptions, syncOptionServerSideApply) {
		t.Errorf("syncOptions = %v, did not request ServerSideApply but got it anyway", app.Spec.SyncPolicy.SyncOptions)
	}
}

// TestNew_KindOnlyIgnoreDoesNotRespect checks that an ignoreDifferences entry
// naming only group and kind (Cilium's CiliumIdentity, which the chart never
// renders) does not add RespectIgnoreDifferences=true: ADR 0055.
func TestNew_KindOnlyIgnoreDoesNotRespect(t *testing.T) {
	opts := validOptions()
	opts.IgnoreDifferences = []ResourceIgnoreDifference{{Group: "cilium.io", Kind: "CiliumIdentity"}}

	app, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if app.Spec.SyncPolicy != nil && slices.Contains(app.Spec.SyncPolicy.SyncOptions, syncOptionRespectIgnoreDiffs) {
		t.Errorf("syncOptions = %v, want no RespectIgnoreDifferences=true for a kind-only entry", app.Spec.SyncPolicy.SyncOptions)
	}

	if len(app.Spec.IgnoreDifferences) != 1 {
		t.Errorf("ignoreDifferences = %+v, want the entry preserved", app.Spec.IgnoreDifferences)
	}
}

// TestNew_SyncWaveAnnotation checks the third named pitfall: sync-wave
// ordering is carried as the argocd.argoproj.io/sync-wave annotation.
func TestNew_SyncWaveAnnotation(t *testing.T) {
	for _, wave := range []int{SyncWaveArgoCD, SyncWaveCNI, SyncWaveIchor, SyncWaveIchorCRs, SyncWaveStorage,
		SyncWaveOther} {
		opts := validOptions()
		opts.SyncWave = wave

		app, err := New(opts)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		got := app.Annotations[SyncWaveAnnotation]
		want := strconv.Itoa(wave)

		if got != want {
			t.Errorf("sync-wave annotation = %q, want %q", got, want)
		}
	}

	// And the documented order holds: Argo CD -> CNI -> Ichor -> Ichor CRs -> storage -> everything else.
	inOrder := SyncWaveArgoCD < SyncWaveCNI && SyncWaveCNI < SyncWaveIchor && SyncWaveIchor < SyncWaveIchorCRs &&
		SyncWaveIchorCRs < SyncWaveStorage && SyncWaveStorage < SyncWaveOther
	if !inOrder {
		t.Fatal("sync wave constants are not in the design's documented order")
	}
}

func TestNew_RequiresSourceAndDestination(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*Options)
	}{
		{"no name", func(o *Options) { o.Name = "" }},
		{"no source", func(o *Options) { o.Source = nil }},
		{"no destination", func(o *Options) { o.Destination = ApplicationDestination{} }},
		{"empty repoURL", func(o *Options) { o.Source.RepoURL = "" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := validOptions()
			tc.mod(&opts)

			if _, err := New(opts); err == nil {
				t.Fatal("New: want error, got nil")
			}
		})
	}
}

// TestNew_RejectsCredentialsInRepoURL is the credential-leak test the
// acceptance criteria call for: repo credentials must never end up in a
// generated file, and the most direct way they could is a RepoURL with
// embedded userinfo.
func TestNew_RejectsCredentialsInRepoURL(t *testing.T) {
	leaky := []string{
		"https://user:supersecrettoken@github.com/maeshinshin/k8s-clusters.git",
		"https://x-access-token:ghp_abcdef0123456789@github.com/org/repo.git",
	}

	for _, repoURL := range leaky {
		opts := validOptions()
		opts.Source.RepoURL = repoURL

		app, err := New(opts)
		if err == nil {
			t.Fatalf("New(%q): want error, got Application %+v", repoURL, app)
		}

		if !strings.Contains(err.Error(), "credentials") {
			t.Errorf("New(%q) error = %q, want it to mention credentials", repoURL, err)
		}
	}
}

// TestApplication_YAMLHasRequiredFields checks the generated YAML is a
// structurally valid Argo CD Application: apiVersion, kind and the fields
// Argo CD's schema requires are present once round-tripped through YAML,
// the same encoding Skeleton.Write uses.
func TestApplication_YAMLHasRequiredFields(t *testing.T) {
	opts := validOptions()
	opts.Automated = true
	opts.Prune = true
	opts.SelfHeal = true

	app, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := yaml.Marshal(app)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	if doc["apiVersion"] != ApplicationAPIVersion {
		t.Errorf("apiVersion = %v, want %q", doc["apiVersion"], ApplicationAPIVersion)
	}

	if doc["kind"] != ApplicationKind {
		t.Errorf("kind = %v, want %q", doc["kind"], ApplicationKind)
	}

	meta, ok := doc["metadata"].(map[string]any)
	if !ok || meta["name"] != opts.Name {
		t.Errorf("metadata.name = %v, want %q", doc["metadata"], opts.Name)
	}

	spec, ok := doc["spec"].(map[string]any)
	if !ok {
		t.Fatalf("spec is not an object: %v", doc["spec"])
	}

	if spec["project"] != defaultProject {
		t.Errorf("spec.project = %v, want %q", spec["project"], defaultProject)
	}

	if _, ok := spec["source"]; !ok {
		t.Errorf("spec.source missing: %v", spec)
	}

	if _, ok := spec["destination"]; !ok {
		t.Errorf("spec.destination missing: %v", spec)
	}
}

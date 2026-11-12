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
	"os"
	"slices"
	"strings"
	"testing"
)

const unionPath = "../../../api/v1alpha1/zz_generated.talosconfigpatchdocument.go"

// TestShippedRootsAreDerived checks that every shipped kind resolves against Talos's registry to the document
// type, kind constant and API version the generator used to carry by hand.
func TestShippedRootsAreDerived(t *testing.T) {
	roots, notes, err := deriveRoots(talosRegistry(t), kindClasses, shipped)
	if err != nil {
		t.Fatal(err)
	}

	if len(notes) > 0 {
		t.Errorf("shipped kinds that are not classed accept: %v", notes)
	}

	got := make([]string, 0, len(roots))

	for _, r := range roots {
		got = append(got, r.kind+" "+r.typeName+" "+r.kindConstOrNone()+" "+r.apiVersion)
	}

	want := []string{
		"SysctlConfig SysctlConfigV1Alpha1 SysctlConfigKind v1alpha1",
		"KernelModuleConfig KernelModuleConfigV1Alpha1 KernelModuleConfigKind v1alpha1",
		"Config Config  ",
		"VolumeConfig VolumeConfigV1Alpha1 VolumeConfigKind v1alpha1",
		"UserVolumeConfig UserVolumeConfigV1Alpha1 UserVolumeConfigKind v1alpha1",
		"RawVolumeConfig RawVolumeConfigV1Alpha1 RawVolumeConfigKind v1alpha1",
		"ResolverConfig ResolverConfigV1Alpha1 ResolverKind v1alpha1",
	}

	if !slices.Equal(got, want) {
		t.Errorf("derived roots:\n got %q\nwant %q", got, want)
	}
}

func (r rootKind) kindConstOrNone() string { return r.constName }

// TestReclassifiedKindKeepsItsField is the append-only rule: a shipped kind that is later reclassified away
// from accept still gets its field, its enum value and its place in both CEL rules, and the generator says so.
func TestReclassifiedKindKeepsItsField(t *testing.T) {
	reg := talosRegistry(t)

	before, _, err := deriveRoots(reg, kindClasses, shipped)
	if err != nil {
		t.Fatal(err)
	}

	classes := maps.Clone(kindClasses)
	classes["ResolverConfig"] = kindRow{secret, "reclassified for this test"}
	classes["SysctlConfig"] = kindRow{unstructured, "reclassified for this test"}

	after, notes, err := deriveRoots(reg, classes, shipped)
	if err != nil {
		t.Fatal(err)
	}

	if unionSource(after) != unionSource(before) {
		t.Error("reclassifying a shipped kind changed the generated union")
	}

	if len(notes) != 2 {
		t.Errorf("want one note per reclassified shipped kind, got %v", notes)
	}

	src := unionSource(after)
	for _, want := range []string{"ResolverConfig *typedconfig.", "SysctlConfig *typedconfig.", "Enum=SysctlConfig;", ";ResolverConfig\n"} {
		if !strings.Contains(src, want) {
			t.Errorf("generated union lost %q after reclassification", want)
		}
	}
}

// TestShippedKindMustStayRegisteredAndClassified: a shipped kind Talos stops registering, or that loses its
// kindClasses row, is an error; silently omitting it would drop a field.
func TestShippedKindMustStayRegisteredAndClassified(t *testing.T) {
	reg := maps.Clone(talosRegistry(t))
	delete(reg, "ResolverConfig")

	if _, _, err := deriveRoots(reg, kindClasses, shipped); err == nil || !strings.Contains(err.Error(), "no longer registered") {
		t.Errorf("a vanished shipped kind: got %v", err)
	}

	classes := maps.Clone(kindClasses)
	delete(classes, "ResolverConfig")

	if _, _, err := deriveRoots(talosRegistry(t), classes, shipped); err == nil {
		t.Error("a shipped kind with no classification row did not fail")
	}
}

// TestUnionIsCommitted checks that the committed union file is what the generator emits for shipped now.
func TestUnionIsCommitted(t *testing.T) {
	roots, _, err := deriveRoots(talosRegistry(t), kindClasses, shipped)
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(unionPath)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != unionSource(roots) {
		t.Errorf("%s is stale; run make generate", unionPath)
	}
}

func TestFieldName(t *testing.T) {
	for kind, want := range map[string]string{
		"SysctlConfig":       "sysctlConfig",
		"Config":             "config",
		"BGPInstanceConfig":  "bgpInstanceConfig",
		"UserVolumeConfig":   "userVolumeConfig",
		"HTTPProbeConfig":    "httpProbeConfig",
		"DNSResolverConfig2": "dnsResolverConfig2",
	} {
		if got := fieldName(kind); got != want {
			t.Errorf("fieldName(%q) = %q, want %q", kind, got, want)
		}
	}
}

// TestUnionHasNoCELAndChecksEveryKind: the union bounds a document by property count, never by a CEL rule
// that grows with the number of kinds (the API server rejects a CRD whose rule cost passes its limit), and
// Validate has a case and a field test for every shipped kind.
func TestUnionHasNoCELAndChecksEveryKind(t *testing.T) {
	roots, _, err := deriveRoots(talosRegistry(t), kindClasses, shipped)
	if err != nil {
		t.Fatal(err)
	}

	src := unionSource(roots)

	if strings.Contains(src, "XValidation") {
		t.Error("the union carries a CEL rule")
	}

	for _, want := range []string{"MinProperties=2\n", "MaxProperties=2\n"} {
		if !strings.Contains(src, want) {
			t.Errorf("the union lost +kubebuilder:validation:%s", want)
		}
	}

	for _, r := range roots {
		for _, want := range []string{"case \"" + r.kind + "\":\n\t\twant = \"" + fieldName(r.kind) + "\"", "if d." + r.kind + " != nil {"} {
			if !strings.Contains(src, want) {
				t.Errorf("Validate does not check %s: missing %q", r.kind, want)
			}
		}
	}
}

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

func indexRows(t *testing.T, crds string) map[string]fieldRow {
	t.Helper()

	rows, err := fieldRows(crds)
	if err != nil {
		t.Fatalf("fieldRows: %v", err)
	}

	out := map[string]fieldRow{}
	for _, r := range rows {
		out[r.Kind+" "+r.Path] = r
	}

	return out
}

// TestFieldIndexHasEveryDocumentField checks the index against the CRD reference page, which is written by
// a different walk of the same schema: every field of every document kind there is a row here.
func TestFieldIndexHasEveryDocumentField(t *testing.T) {
	page, err := os.ReadFile(filepath.Join(repoRoot, outputDir, "crds", "talosconfigpatch.md"))
	if err != nil {
		t.Fatal(err)
	}

	rows := indexRows(t, filepath.Join(repoRoot, crdDir))
	byPath := map[string]bool{}

	for _, r := range rows {
		byPath[r.Path] = true
	}

	// | `spec.documents[].bondConfig.links[].name` | ...
	re := regexp.MustCompile("(?m)^\\| `spec\\.documents\\[\\]\\.([A-Za-z0-9]+)\\.([^`]+)`")
	seen := 0

	for _, m := range re.FindAllStringSubmatch(string(page), -1) {
		if strings.EqualFold(m[1], "kubeStaticPodConfig") {
			continue
		}

		seen++

		if !byPath[m[2]] {
			t.Errorf("the CRD has %s.%s and the index has no row for it", m[1], m[2])
		}
	}

	if seen < 1000 {
		t.Fatalf("read only %d fields from the CRD reference; the pattern no longer matches", seen)
	}
}

// TestFieldIndexFollowsTheSchema adds a field to one kind's schema and checks it is in the index with no
// other change: the index is built from the schema, not from a list kept beside it.
func TestFieldIndexFollowsTheSchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, crdDir, patchCRD))
	if err != nil {
		t.Fatal(err)
	}

	var crd apiextv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}

	module := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["documents"].Items.Schema.Properties["kernelModuleConfig"]
	module.Properties["addedByTheTest"] = apiextv1.JSONSchemaProps{Type: "string", Description: "A field the test added."}

	changed, err := yaml.Marshal(&crd)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, patchCRD), changed, 0o644); err != nil {
		t.Fatal(err)
	}

	row, ok := indexRows(t, dir)["KernelModuleConfig addedByTheTest"]
	if !ok {
		t.Fatal("a field added to the schema has no row")
	}

	if row.Type != "string" || row.Description != "A field the test added." {
		t.Errorf("row = %+v", row)
	}
}

// TestFieldIndexTakesTalosDocumentation checks what the CRD schema lacks comes from the Talos tables: a
// description the schema leaves empty, and the allowed values of a string field.
func TestFieldIndexTakesTalosDocumentation(t *testing.T) {
	rows := indexRows(t, filepath.Join(repoRoot, crdDir))

	if r := rows["BGPInstanceConfig advertise"]; !strings.Contains(r.Description, "originated into BGP") {
		t.Errorf("BGPInstanceConfig advertise has description %q", r.Description)
	}

	if r := rows["UserVolumeConfig volumeType"]; strings.Join(r.Values, ",") != "directory,disk,partition" {
		t.Errorf("UserVolumeConfig volumeType has values %v", r.Values)
	}

	if r := rows["BondConfig bondMode"]; len(r.Values) != 0 {
		t.Errorf("an integer field takes no value names, got %v", r.Values)
	}
}

// TestFieldIndexKeepsTheStaticPodToOneRow checks the pod kind is reachable and its 1,075 fields are not in.
func TestFieldIndexKeepsTheStaticPodToOneRow(t *testing.T) {
	n := 0

	for key := range indexRows(t, filepath.Join(repoRoot, crdDir)) {
		if strings.HasPrefix(key, "KubeStaticPodConfig ") {
			n++
		}
	}

	if n != 1 {
		t.Errorf("KubeStaticPodConfig has %d rows, want its own row only", n)
	}
}

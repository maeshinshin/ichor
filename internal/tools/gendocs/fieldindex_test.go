package main

import (
	"os"
	"path/filepath"
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

// TestFieldIndexHasTheFieldsOfEveryDocumentKind checks the index holds the documents the editor writes: the kinds
// that spec.config converts to, each with its own row and the fields of its settings in their document paths.
func TestFieldIndexHasTheFieldsOfEveryDocumentKind(t *testing.T) {
	rows := indexRows(t, filepath.Join(repoRoot, crdDir))

	for _, key := range []string{
		"Config ", "SysctlConfig params", "SysctlConfig ", "KernelModuleConfig name", "KernelModuleConfig parameters",
		"UserVolumeConfig provisioning.diskSelector.match", "KubeletConfig extraArgs", "Config machine.install.disk",
		"KubeAPIServerConfig certExtraSANs", "BondConfig bondMode",
	} {
		if _, ok := rows[key]; !ok {
			t.Errorf("the index has no row %q", key)
		}
	}

	// A setting a document spells is a row of the document, not of the v1alpha1 document that also has it.
	if _, ok := rows["Config machine.sysctls"]; ok {
		t.Error("machine.sysctls is a row of the v1alpha1 document as well as SysctlConfig")
	}

	if len(rows) < 1000 {
		t.Errorf("the index has only %d rows", len(rows))
	}
}

// TestFieldIndexFollowsTheSchema changes the format of a setting in the CRD and checks the row has it, with no
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

	sysctls := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["config"].Properties["machine"].Properties["sysctls"]
	sysctls.Format = "test-format"
	crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["config"].Properties["machine"].Properties["sysctls"] = sysctls

	changed, err := yaml.Marshal(&crd)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, patchCRD), changed, 0o644); err != nil {
		t.Fatal(err)
	}

	row, ok := indexRows(t, dir)["SysctlConfig params"]
	if !ok {
		t.Fatal("the setting has no row")
	}

	if row.Format != "test-format" {
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

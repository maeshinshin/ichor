package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

func indexRows(t *testing.T, crds string) map[string]fieldRow {
	t.Helper()

	rows, err := fieldRows(crds)
	if err != nil {
		t.Fatalf("fieldRows: %v", err)
	}

	out := map[string]fieldRow{}
	for _, r := range rows {
		if _, dup := out[r.Path]; dup {
			t.Errorf("the index has two rows for %q", r.Path)
		}

		out[r.Path] = r
	}

	return out
}

// TestFieldIndexHasEachSettingOnceByItsPathInSpecConfig checks a row is a setting of spec.config, by its path
// there: a setting several Talos documents spell is one row, and the documents' own spellings are not rows.
func TestFieldIndexHasEachSettingOnceByItsPathInSpecConfig(t *testing.T) {
	rows := indexRows(t, filepath.Join(repoRoot, crdDir))

	for path, doc := range map[string]string{
		"machine.sysctls":                              "SysctlConfig:params",
		"machine.kernel.modules":                       "KernelModuleConfig:",
		"machine.kernel.modules.<key>":                 "KernelModuleConfig:name",
		"machine.kernel.modules.<key>.parameters":      "KernelModuleConfig:parameters",
		"machine.volumes.user.<key>.provisioning.grow": "UserVolumeConfig:provisioning.grow",
		"machine.install.disk":                         "Config:machine.install.disk",
		"cluster.apiServer.certSANs":                   "",
		"machine.network.nameservers[].address":        "ResolverConfig:nameservers[].address",
		"machine.network.bonds.<key>.bondMode":         "BondConfig:bondMode",
		"machine.kubelet.extraArgs":                    "KubeletConfig:extraArgs",
	} {
		r, ok := rows[path]
		switch {
		case doc == "" && ok:
			t.Errorf("%q is a row, and the schema does not carry it", path)
		case doc != "" && !ok:
			t.Errorf("the index has no row %q", path)
		case doc != "" && r.Doc != doc:
			t.Errorf("%q is written in %q, want %q", path, r.Doc, doc)
		}
	}

	// The spelling of a document is not a row of its own: the row is the path, not Kind plus a path.
	for _, spelling := range []string{"params", "Config", "SysctlConfig", "machine.sysctls.params"} {
		if _, ok := rows[spelling]; ok {
			t.Errorf("%q is a row, and it is a spelling of machine.sysctls", spelling)
		}
	}

	seen := map[string]string{}

	for path, r := range rows {
		if other, dup := seen[r.Doc]; dup {
			t.Errorf("%q and %q are the same setting of a document, %q", path, other, r.Doc)
		}

		seen[r.Doc] = path
	}

	if len(rows) < 1000 {
		t.Errorf("the index has only %d rows", len(rows))
	}
}

// TestASettingHasOnePathInSpecConfig checks no setting of a document is written at two places of the schema,
// which would be two rows of the search for one setting.
func TestASettingHasOnePathInSpecConfig(t *testing.T) {
	seen := map[string]string{}

	var walk func(rt reflect.Type, path string)

	walk = func(rt reflect.Type, path string) {
		for f := range rt.Fields() {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			p := join(path, name)

			tag := f.Tag.Get("ichor")
			if sub := structOf(f.Type); sub.PkgPath() == rt.PkgPath() {
				walk(sub, p)
			}

			if strings.HasPrefix(tag, "docs=") {
				continue
			}

			for part := range strings.SplitSeq(tag, ";") {
				if part == "" || strings.HasPrefix(part, "+") {
					continue
				}

				if other, dup := seen[part]; dup {
					t.Errorf("%q is written at %q and at %q", part, other, p)
				}

				seen[part] = p
			}
		}
	}

	walk(reflect.TypeFor[ichorconfig.Config](), "")
}

// TestFieldIndexFollowsTheSchema changes the format of a setting in the CRD, and adds a field to a Talos type's
// object in it, and checks the rows have both with no other change: the index is built from the schema, not
// from a list kept beside it.
func TestFieldIndexFollowsTheSchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, crdDir, patchCRD))
	if err != nil {
		t.Fatal(err)
	}

	var crd apiextv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}

	machine := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["config"].Properties["machine"]

	sysctls := machine.Properties["sysctls"]
	sysctls.Format = "test-format"
	machine.Properties["sysctls"] = sysctls

	install := machine.Properties["install"]
	install.Properties["brandNewField"] = apiextv1.JSONSchemaProps{Type: "string"}
	machine.Properties["install"] = install

	crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["config"].Properties["machine"] = machine

	changed, err := yaml.Marshal(&crd)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, patchCRD), changed, 0o644); err != nil {
		t.Fatal(err)
	}

	rows := indexRows(t, dir)

	if r, ok := rows["machine.sysctls"]; !ok || r.Format != "test-format" {
		t.Errorf("machine.sysctls = %+v", r)
	}

	if r, ok := rows["machine.install.brandNewField"]; !ok || r.Doc != "Config:machine.install.brandNewField" {
		t.Errorf("machine.install.brandNewField = %+v", r)
	}
}

// TestFieldIndexTakesTalosDocumentation checks what the CRD schema lacks comes from the Talos tables: a
// description the schema leaves empty, and the allowed values of a string field.
func TestFieldIndexTakesTalosDocumentation(t *testing.T) {
	rows := indexRows(t, filepath.Join(repoRoot, crdDir))

	if r := rows["machine.network.bgp.<key>.advertise"]; !strings.Contains(r.Description, "originated into BGP") {
		t.Errorf("machine.network.bgp.<key>.advertise has description %q", r.Description)
	}

	if r := rows["machine.volumes.user.<key>.volumeType"]; strings.Join(r.Values, ",") != "directory,disk,partition" {
		t.Errorf("machine.volumes.user.<key>.volumeType has values %v", r.Values)
	}

	if r := rows["machine.network.bonds.<key>.bondMode"]; len(r.Values) != 0 {
		t.Errorf("an integer field takes no value names, got %v", r.Values)
	}
}

// TestFieldIndexKeepsTheStaticPodToTwoRows checks the pod is reachable, by its collection and its key, and its
// 1,075 fields are not in.
func TestFieldIndexKeepsTheStaticPodToTwoRows(t *testing.T) {
	n := 0

	for path := range indexRows(t, filepath.Join(repoRoot, crdDir)) {
		if strings.HasPrefix(path, "machine.staticPods") {
			n++
		}
	}

	if n != 2 {
		t.Errorf("machine.staticPods has %d rows, want the collection and its key", n)
	}
}

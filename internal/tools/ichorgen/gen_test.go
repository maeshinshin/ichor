package main

import (
	"os"
	"strings"
	"testing"
)

// inTypedconfig runs the test where go generate runs the tool, since that is where the generator finds the mirror.
func inTypedconfig(t *testing.T) {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir("../../../pkg/typedconfig"); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chdir(wd) })
}

// addField gives a mirror struct one more field, as a bump of Talos machinery does.
func addField(src *source, structName, jsonName string) {
	st := src.structs[structName]
	st.fields = append(st.fields, &srcField{goName: "Added", json: jsonName, t: &typ{kind: tPtr, elem: &typ{kind: tBasic, name: "bool"}}})
}

func render(t *testing.T, src *source) string {
	t.Helper()

	g, err := run(src)
	if err != nil {
		t.Fatal(err)
	}

	if len(g.conflicts) > 0 {
		t.Fatalf("conflicts: %v", g.conflicts)
	}

	out, err := g.render()
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}

// A field Talos adds to a document appears in the schema, in the place its kind has, with no row edited: in the
// entries of a collection, and in an object two documents share.
func TestFieldAddedToATalosTypeAppearsInTheSchemaWithNoRowEdited(t *testing.T) {
	inTypedconfig(t)

	src, err := loadSource()
	if err != nil {
		t.Fatal(err)
	}

	before := render(t, src)

	if strings.Contains(before, "FreshSetting") {
		t.Fatal("the field is there before it was added")
	}

	addField(src, src.roots["KernelModuleConfig"], "freshSetting")
	addField(src, src.roots["KubeletConfig"], "freshSetting")
	addField(src, "KubeletConfig", "freshSetting")

	after := render(t, src)

	for _, want := range []string{
		"FreshSetting *bool `json:\"freshSetting,omitempty\" ichor:\"KernelModuleConfig:freshSetting\"`",
		"FreshSetting *bool `json:\"freshSetting,omitempty\" ichor:\"Config:machine.kubelet.freshSetting;KubeletConfig:freshSetting\"`",
	} {
		if !strings.Contains(after, want) {
			t.Errorf("the schema lacks %s", want)
		}
	}
}

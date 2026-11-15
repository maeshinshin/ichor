package ichorconfig_test

import (
	"bufio"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorconfig"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// This test checks Ichor's schema against the mirror types of the Talos documents, the generated types the
// schema is itself generated from, but through reflection over the compiled types and the struct tags: a
// separate reading of both. A setting the mirror has and the schema has no sink for fails here until the
// schema is regenerated, which places it with no row edited. The settings the schema deliberately does not
// carry because another setting spells them are recorded in testdata/not_carried.txt, outside the generator,
// so that a row added to the generator that drops a setting cannot also drop it from the record.

type sink struct{ kind, path string }

// mirrorLeaves lists the settings of one document type: the dotted JSON path of every field that is not
// itself a struct of the mirror package.
func mirrorLeaves(t reflect.Type, path string, chain []reflect.Type, out *[]string) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct || t.PkgPath() != reflect.TypeFor[typedconfig.Config]().PkgPath() || slices.Contains(chain, t) {
		*out = append(*out, path)

		return
	}

	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}

		p := name
		if path != "" {
			p = path + "." + name
		}

		mirrorLeaves(t.Field(i).Type, p, append(chain, t), out)
	}
}

// schemaSinks lists every sink the schema's tags name, and the kinds that are collections of named documents.
func schemaSinks(t reflect.Type, sinks *[]sink, mapKinds map[string]bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct || t.PkgPath() != reflect.TypeFor[ichorconfig.Config]().PkgPath() {
		return
	}

	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("ichor")

		if kind, ok := strings.CutPrefix(tag, "docs="); ok {
			if f.Type.Kind() == reflect.Map {
				mapKinds[kind] = true
			}
		} else if tag != "" && !isSchema(f.Type) {
			for _, part := range strings.Split(tag, ";") {
				kind, path, _ := strings.Cut(part, ":")
				if !strings.HasPrefix(kind, "+") {
					*sinks = append(*sinks, sink{kind, path})
				}
			}
		}

		schemaSinks(f.Type, sinks, mapKinds)
	}
}

// isSchema reports whether t is an object of the schema itself, whose sinks are those of its settings.
func isSchema(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}

	return t.Kind() == reflect.Struct && t.PkgPath() == reflect.TypeFor[ichorconfig.Config]().PkgPath()
}

func rootTypes(t *testing.T) map[string]reflect.Type {
	t.Helper()

	d := reflect.TypeFor[ichorv1alpha1.TalosConfigPatchDocument]()
	byKey := map[string]reflect.Type{}

	for i := range d.NumField() {
		name, _, _ := strings.Cut(d.Field(i).Tag.Get("json"), ",")
		byKey[name] = d.Field(i).Type
	}

	out := map[string]reflect.Type{}

	for _, kind := range ichorv1alpha1.TalosConfigPatchDocumentKinds() {
		key, _ := ichorv1alpha1.TalosConfigPatchDocumentFieldKey(kind)
		out[kind] = byKey[key]
	}

	return out
}

func covered(sinks []sink, kind, path string) bool {
	return slices.ContainsFunc(sinks, func(s sink) bool {
		return s.kind == kind && (s.path == path || strings.HasPrefix(path, s.path+"."))
	})
}

func notCarried(t *testing.T) []string {
	t.Helper()

	f, err := os.Open("testdata/not_carried.txt")
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	var out []string

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}

	return out
}

func TestEverySettingOfATalosDocumentHasAPlaceInTheSchema(t *testing.T) {
	var sinks []sink

	mapKinds := map[string]bool{}
	schemaSinks(reflect.TypeFor[ichorconfig.Config](), &sinks, mapKinds)

	recorded := notCarried(t)

	var missing []string

	seen := map[string]bool{}

	for kind, root := range rootTypes(t) {
		var leaves []string

		mirrorLeaves(root, "", nil, &leaves)

		for _, leaf := range leaves {
			entry := kind + " " + leaf

			switch {
			case covered(sinks, kind, leaf):
				if slices.Contains(recorded, entry) {
					t.Errorf("%s is in testdata/not_carried.txt but the schema carries it", entry)
				}
			case leaf == "name" && mapKinds[kind]:
				// The key of the collection.
			case slices.Contains(recorded, entry):
				seen[entry] = true
			default:
				missing = append(missing, entry)
			}
		}
	}

	slices.Sort(missing)

	for _, m := range missing {
		t.Errorf("%s has no place in Ichor's schema: run make generate", m)
	}

	for _, r := range recorded {
		if !seen[r] {
			t.Errorf("%s is in testdata/not_carried.txt but is not a setting of that document", r)
		}
	}
}

func TestEverySinkOfTheSchemaIsASettingOfATalosDocument(t *testing.T) {
	var sinks []sink

	schemaSinks(reflect.TypeFor[ichorconfig.Config](), &sinks, map[string]bool{})

	leaves := map[string][]string{}

	for kind, root := range rootTypes(t) {
		var l []string

		mirrorLeaves(root, "", nil, &l)

		leaves[kind] = l
	}

	for _, s := range sinks {
		if !slices.ContainsFunc(leaves[s.kind], func(l string) bool { return l == s.path || strings.HasPrefix(l, s.path+".") }) {
			t.Errorf("the schema writes to %s:%s, which is not a setting of that document", s.kind, s.path)
		}
	}
}

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

package typedconfig_test

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// carriedFieldsFile lists, one path per line, the fields of the kubelet, kube-proxy, kube-scheduler and static
// pod kinds that the mirror carries, as read from the generated types when the file was written.
const carriedFieldsFile = "testdata/carried_fields.txt"

// TestSubsetKindsKeepTheFieldsTheyCarried fails when a field that these kinds carried leaves the mirror.
//
// The round trip cannot see it: it fills the mirror's own fields, so a field that is gone is never filled. The
// omit table cannot either, since a row added to it removes the field from the mirror and from anything derived
// from the table in the same step. The record has to come from outside the generator, and so it is a file
// that make generate does not write. A CRD field cannot be withdrawn once a build has had it (ADR 0158), so the
// mirror may carry more than the file lists, and a field is carried later by deleting its omit row alone;
// taking one out of the file is the decision to withdraw it.
//
// shortcut: a field carried after the file was written is not in it, so it is not protected until its path is
// appended. Upgrade by writing the file from the generator at release time.
func TestSubsetKindsKeepTheFieldsTheyCarried(t *testing.T) {
	raw, err := os.ReadFile(carriedFieldsFile)
	if err != nil {
		t.Fatal(err)
	}

	doc := reflect.TypeFor[ichorv1alpha1.TalosConfigPatchDocument]()

	var have []string

	for _, kind := range []string{"KubeletConfig", "KubeProxyConfig", "KubeSchedulerConfig", "KubeStaticPodConfig"} {
		f, ok := doc.FieldByName(kind)
		if !ok {
			t.Fatalf("TalosConfigPatchDocument has no %s", kind)
		}

		have = carriedPaths(kind, f.Type, have)
	}

	for want := range strings.FieldsSeq(string(raw)) {
		if !slices.Contains(have, want) {
			t.Errorf("%s no longer carries %s, which an earlier build did; a CRD field cannot be withdrawn (ADR 0158). "+
				"Delete its omit row again, or remove its line from %s to withdraw it on purpose.",
				strings.SplitN(want, ".", 2)[0], want, carriedFieldsFile)
		}
	}
}

// carriedPaths appends the JSON path of every field the mirror type t has, looking through an inline embedded
// struct and into every struct of the mirror package; a field of an upstream type is a leaf.
func carriedPaths(prefix string, t reflect.Type, out []string) []string {
	for t = peel(t); t.Kind() == reflect.Slice || t.Kind() == reflect.Map; t = peel(t.Elem()) {
		prefix += "[]"
	}

	if t.Kind() != reflect.Struct || t.PkgPath() != mirrorPkg {
		return out
	}

	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")

		switch {
		case !f.IsExported() || name == "-":
		case name == "": // inline: its fields are this struct's
			out = carriedPaths(prefix, f.Type, out)
		default:
			out = carriedPaths(prefix+"."+name, f.Type, append(out, prefix+"."+name))
		}
	}

	return out
}

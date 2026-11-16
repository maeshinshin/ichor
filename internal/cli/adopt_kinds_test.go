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

package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// A kind Ichor ships is a kind the adopt plan can describe: its diff paths are claimed by kind
// name and its machinery document converts back through the generated converter. The expected
// set is the generated kind list itself, so a new kind is covered without an edit here; the one
// kind machinery does not name by kind (the v1alpha1 document) is found by its type and is
// captured through the plan's path-by-path capture instead.
func TestAdoptPlanDescribesEveryShippedKind(t *testing.T) {
	kinds := ichorv1alpha1.TalosConfigPatchDocumentKinds()
	if len(kinds) == 0 {
		t.Fatal("the generated kind list is empty")
	}

	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			key, ok := ichorv1alpha1.TalosConfigPatchDocumentFieldKey(kind)
			if !ok {
				t.Fatalf("no field for shipped kind %s", kind)
			}

			doc := ichorv1alpha1.TalosConfigPatchDocument{Kind: kind}
			dv := reflect.ValueOf(&doc).Elem()

			var field reflect.Value

			for i := range dv.NumField() {
				if strings.Split(dv.Type().Field(i).Tag.Get("json"), ",")[0] == key {
					field = dv.Field(i)
				}
			}

			if !field.IsValid() {
				t.Fatalf("no struct field with JSON name %q", key)
			}

			field.Set(reflect.New(field.Type().Elem()))

			// Named documents must be named for the loader to accept them.
			for i := range field.Elem().NumField() {
				f := field.Elem().Type().Field(i)
				if strings.Split(f.Tag.Get("json"), ",")[0] == keyName && f.Type.Kind() == reflect.String {
					field.Elem().Field(i).SetString("x")
				}
			}

			md, err := doc.Document()
			if err != nil {
				t.Fatalf("converting to machinery: %v", err)
			}

			if _, legacy := md.(*mv1alpha1.Config); legacy {
				return
			}

			if md.Kind() != kind {
				t.Fatalf("machinery names this document %q, the diff roots its paths at %q", md.Kind(), kind)
			}

			if got, _, rest := groupChangedPaths([]string{kind + ".x"}); !got[kind] || len(rest) != 0 {
				t.Fatalf("the plan does not claim %s.x: kinds %v, unrepresentable %v", kind, got, rest)
			}

			encoded, err := encoder.NewEncoder(md, encoder.WithComments(encoder.CommentsDisabled)).Encode()
			if err != nil {
				t.Fatalf("encoding: %v", err)
			}

			docs, err := captureDocuments(encoded, map[string]bool{kind: true}, nil, nil)
			if err != nil {
				t.Fatalf("capturing: %v", err)
			}

			if len(docs) != 1 || docs[0].Kind != kind {
				t.Fatalf("captured %+v, want one %s document", docs, kind)
			}
		})
	}
}

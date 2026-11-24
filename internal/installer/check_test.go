package installer

import (
	"context"
	"fmt"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	kTalosConfigPatch = "TalosConfigPatch"
	kSize             = "size"
)

// checkCRDManifest is a one-CRD install manifest whose schema has the given spec properties.
func checkCRDManifest(props ...string) string {
	p := ""
	for _, n := range props {
		p += fmt.Sprintf("                %s: {type: string}\n", n)
	}

	return `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: widgets.example.dev}
spec:
  group: example.dev
  scope: Namespaced
  names: {kind: Widget, plural: widgets}
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
` + p
}

func checkFixtureClient(t *testing.T, liveProps []string, extra ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	objs, err := Decode([]byte(checkCRDManifest(liveProps...)))
	if err != nil {
		t.Fatal(err)
	}

	b := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs[0]).WithObjects(extra...)

	return b.Build()
}

// Check and Run answer the same question before Run's first write: for the same cluster and manifest they return the
// same refusal, and Check writes nothing.
func TestCheckAndRunRefuseTheSameWay(t *testing.T) {
	retired := &unstructured.Unstructured{Object: map[string]any{
		kAPIVersion: ichorAPIVersion, kKind: kTalosConfigPatch,
		kMetadata: map[string]any{kName: "reg", kNamespace: "ns"}, // the installer's namespace: its Role lists there only
		kSpec: map[string]any{kDocuments: []any{map[string]any{kKind: kConfigKind, kConfigKey: map[string]any{kVersion: kV1,
			"machine": map[string]any{"registries": map[string]any{"config": map[string]any{"r.example": map[string]any{"tls": map[string]any{"clientIdentity": map[string]any{"crt": "x"}}}}}}}}}},
	}}

	cases := map[string]struct {
		live   []string
		target []string
		extra  []client.Object
	}{
		"a CRD that would lose a property": {live: []string{"color", kSize}, target: []string{kSize}},
		"a stored retired field":           {live: []string{kSize}, target: []string{kSize}, extra: []client.Object{retired}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := checkFixtureClient(t, tc.live, tc.extra...)
			opts := Options{Namespace: "ns", RunID: "run", Manifest: []byte(checkCRDManifest(tc.target...))}

			checkErr := Check(context.Background(), c, opts)
			runErr := Run(context.Background(), c, opts)

			if checkErr == nil || runErr == nil {
				t.Fatalf("both must refuse: Check=%v Run=%v", checkErr, runErr)
			}

			if checkErr.Error() != runErr.Error() {
				t.Errorf("the two paths disagree:\n Check: %v\n Run:   %v", checkErr, runErr)
			}
		})
	}
}

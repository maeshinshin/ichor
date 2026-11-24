package installer

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func storedPatch(name string, docs []any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		kAPIVersion: ichorAPIVersion, kKind: kTalosConfigPatch,
		kMetadata: map[string]any{kName: name, kNamespace: kDefault},
		kSpec:     map[string]any{kClusterRef: map[string]any{kName: "c"}, "layer": kClusterLayer, kDocuments: docs},
	}}
}

func migrationClient(objs ...*unstructured.Unstructured) client.Client {
	b := fake.NewClientBuilder().WithStatusSubresource(storedPatch("s", nil))
	for _, o := range objs {
		b = b.WithObjects(o)
	}

	return b.Build()
}

func getPatch(t *testing.T, c client.Client, name string) *ichorv1alpha1.TalosConfigPatch {
	t.Helper()

	u := &unstructured.Unstructured{}
	u.SetAPIVersion(ichorAPIVersion)
	u.SetKind(kTalosConfigPatch)

	if err := c.Get(context.Background(), types.NamespacedName{Namespace: kDefault, Name: name}, u); err != nil {
		t.Fatal(err)
	}

	b, _ := json.Marshal(u.Object)
	p := &ichorv1alpha1.TalosConfigPatch{}

	if err := json.Unmarshal(b, p); err != nil {
		t.Fatal(err)
	}

	return p
}

var certSANDocs = []any{map[string]any{
	kKind: kConfigKind, kConfigKey: map[string]any{kVersion: kV1, kCluster: map[string]any{"apiServer": map[string]any{"certSANs": []any{kExample}}}},
}}

var noPlaceDocs = []any{map[string]any{
	kKind: kConfigKind, kConfigKey: map[string]any{kVersion: kV1, kCluster: map[string]any{"extraManifests": []any{"https://x.example/y.yaml"}}},
}}

// The migration writes spec.config, clears spec.documents, and leaves what the patch renders as it was; a second run
// has nothing to do.
func TestMigratePatchesMovesDocumentsToConfigWithoutChangingTheDocuments(t *testing.T) {
	c := migrationClient(storedPatch("p", certSANDocs))
	before := getPatch(t, c, "p")

	wantDocs, err := before.Spec.EffectiveDocuments(true)
	if err != nil {
		t.Fatal(err)
	}

	res, err := MigratePatches(context.Background(), c, "", io.Discard)
	if err != nil || len(res.Migrated) != 1 || len(res.Failed) != 0 {
		t.Fatalf("got %+v, %v", res, err)
	}

	after := getPatch(t, c, "p")
	if after.Spec.Config == nil || after.Spec.Config.Cluster.ApiServer.CertExtraSANs[0] != kExample {
		t.Fatalf("want certSANs written as cluster.apiServer.certExtraSANs, got %+v", after.Spec.Config)
	}

	if len(after.Spec.Documents) != 0 || after.Spec.Config == nil {
		t.Fatalf("want spec.config set and spec.documents cleared, got documents=%v config=%v", after.Spec.Documents, after.Spec.Config)
	}

	gotDocs, err := after.Spec.EffectiveDocuments(true)
	if err != nil {
		t.Fatal(err)
	}

	w, _ := json.Marshal(wantDocs)
	g, _ := json.Marshal(gotDocs)

	if string(w) != string(g) {
		t.Errorf("the documents the patch renders changed:\nbefore %s\nafter  %s", w, g)
	}

	if res, err = MigratePatches(context.Background(), c, "", io.Discard); err != nil || len(res.Migrated)+len(res.Failed) != 0 {
		t.Errorf("a second run did something: %+v, %v", res, err)
	}
}

// A setting spec.config has no place for (the v1alpha1 spelling of extraManifests) leaves the patch as it is, names the
// setting, sets Migrated=False, and the patch still renders it.
func TestMigratePatchesLeavesAPatchWhoseSettingHasNoPlace(t *testing.T) {
	c := migrationClient(storedPatch("p", noPlaceDocs))

	res, err := MigratePatches(context.Background(), c, "", io.Discard)
	if err != nil || len(res.Migrated) != 0 || len(res.Failed) != 1 || !strings.Contains(res.Failed[0], "extraManifests") {
		t.Fatalf("want one failure naming extraManifests, got %+v, %v", res, err)
	}

	p := getPatch(t, c, "p")
	if len(p.Spec.Documents) != 1 || p.Spec.Config != nil {
		t.Fatalf("the patch was changed: %+v", p.Spec)
	}

	var cond string

	for _, k := range p.Status.Conditions {
		if k.Type == MigratedCondition && k.Status == "False" {
			cond = k.Message
		}
	}

	if !strings.Contains(cond, "extraManifests") {
		t.Errorf("want Migrated=False naming extraManifests, got conditions %+v", p.Status.Conditions)
	}

	docs, err := p.Spec.EffectiveDocuments(true)
	if err != nil || len(docs) != 1 || docs[0].Config == nil {
		t.Errorf("the setting is no longer rendered: %v, %v", docs, err)
	}
}

// A second run on a patch that cannot be migrated writes nothing: resourceVersion and the condition's
// lastTransitionTime stay as they were.
func TestMigratePatchesSecondRunOnAnUnmigratablePatchWritesNothing(t *testing.T) {
	c := migrationClient(storedPatch("p", noPlaceDocs))
	ctx := context.Background()

	if _, err := MigratePatches(ctx, c, "", io.Discard); err != nil {
		t.Fatal(err)
	}

	// Age the condition so a reset to now is visible at the second granularity of RFC 3339.
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(ichorAPIVersion)
	u.SetKind(kTalosConfigPatch)

	key := types.NamespacedName{Namespace: kDefault, Name: "p"}
	if err := c.Get(ctx, key, u); err != nil {
		t.Fatal(err)
	}

	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	const aged = "2020-01-01T00:00:00Z"

	conds[0].(map[string]any)["lastTransitionTime"] = aged

	if err := unstructured.SetNestedSlice(u.Object, conds, "status", "conditions"); err != nil {
		t.Fatal(err)
	}

	if err := c.Status().Update(ctx, u); err != nil {
		t.Fatal(err)
	}

	rv := u.GetResourceVersion()

	if _, err := MigratePatches(ctx, c, "", io.Discard); err != nil {
		t.Fatal(err)
	}

	if err := c.Get(ctx, key, u); err != nil {
		t.Fatal(err)
	}

	if got := u.GetResourceVersion(); got != rv {
		t.Errorf("the second run wrote: resourceVersion %s -> %s", rv, got)
	}

	conds, _, _ = unstructured.NestedSlice(u.Object, "status", "conditions")
	if got := conds[0].(map[string]any)["lastTransitionTime"]; got != aged {
		t.Errorf("lastTransitionTime was reset to %v", got)
	}
}

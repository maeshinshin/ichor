package chart

import (
	"slices"
	"testing"
)

const (
	migrateHookName = "ichor-migrate-patches"
	resPatches      = "talosconfigpatches"
	pinnedImage     = "ghcr.io/maeshinshin/ichor-controller:v9.9.9@sha256:" +
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// TestMigratePatchesHook pins the automatic move of a v0.2 patch's spec.documents to spec.config on `helm upgrade`
// (ADR 0152): a post-upgrade hook Job, not a post-install one, running the controller image the chart pins, under a
// ServiceAccount of its own that holds only what installer.MigratePatches calls.
func TestMigratePatchesHook(t *testing.T) {
	docs := renderChartWithValues(t, "ichor", "manager.image.repository="+pinnedImage)

	hookOf := func(doc map[string]any) any {
		meta, _ := doc["metadata"].(map[string]any)
		ann, _ := meta["annotations"].(map[string]any)

		return ann["helm.sh/hook"]
	}

	for _, d := range []struct{ kind, name string }{
		{kindServiceAccount, migrateHookName}, {kindClusterRole, migrateHookName},
		{kindClusterRoleBinding, migrateHookName}, {kindJob, migrateHookName},
	} {
		doc := find(docs, d.kind, d.name)
		if doc == nil {
			t.Fatalf("the chart renders no %s %s", d.kind, d.name)
		}

		if got := hookOf(doc); got != "post-upgrade" {
			t.Errorf("%s %s: helm.sh/hook = %v, want post-upgrade (post-install runs on a fresh install)", d.kind, d.name, got)
		}
	}

	job := find(docs, kindJob, migrateHookName)
	pod := job["spec"].(map[string]any)[kTemplate].(map[string]any)["spec"].(map[string]any)

	if pod["serviceAccountName"] != migrateHookName || pod["restartPolicy"] != kNever {
		t.Errorf("pod runs as %v with restartPolicy %v", pod["serviceAccountName"], pod["restartPolicy"])
	}

	ctrs := pod["containers"].([]any)
	if len(ctrs) != 1 {
		t.Fatalf("want one container, got %d", len(ctrs))
	}

	ctr := ctrs[0].(map[string]any)
	if ctr["image"] != pinnedImage {
		t.Errorf("image = %v, want the manager image %s", ctr["image"], pinnedImage)
	}

	if args, _ := ctr["args"].([]any); !slices.Equal(args, []any{"migrate-patches"}) {
		t.Errorf("args = %v, want [migrate-patches]", args)
	}

	want := map[rbacTriple]bool{
		{ichorGroup, resPatches, verbGetController, ""}:     true,
		{ichorGroup, resPatches, verbList, ""}:              true,
		{ichorGroup, resPatches, verbPatch, ""}:             true,
		{ichorGroup, resPatches + "/status", verbPatch, ""}: true,
		{ichorGroup, resClusters, verbList, ""}:             true,
	}

	got := rbacTriples(allRules(find(docs, kindClusterRole, migrateHookName)))
	if len(got) != len(want) {
		t.Errorf("the hook's ClusterRole grants %v, want exactly %v", got, want)
	}

	for tr := range want {
		if !got[tr] {
			t.Errorf("the hook's ClusterRole does not grant %s", tr)
		}
	}
}

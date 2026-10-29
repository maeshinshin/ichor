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

package render

import (
	"strings"
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// TestTakenOverInlineManifestIsDroppedOnlyOnceHandedOver covers an adopted cluster whose Cilium
// sits in an inline manifest under a name of the operator's choosing (ADR 0103, 1.2): the
// recorded name is dropped once CNIHandover is True and not before, and an unrecorded name is
// never dropped.
func TestTakenOverInlineManifestIsDroppedOnlyOnceHandedOver(t *testing.T) {
	bundle := testBundle(t)
	m := testMachine("node-a", roleWorker, nil)
	patch := inlineManifestPatch("my-cilium", "my-cilium-manifest")

	const entry = "name: my-cilium-manifest"

	cluster := func(handed bool, recorded string) *ichorv1alpha1.TalosCluster {
		c := testCluster("true")
		c.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{TakenOverInlineManifest: recorded}

		status := metav1.ConditionFalse
		if handed {
			status = metav1.ConditionTrue
		}

		apimeta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
			Type: ConditionCNIHandover, Status: status, Reason: "x", Message: "x",
		})

		return c
	}

	for name, tc := range map[string]struct {
		c    *ichorv1alpha1.TalosCluster
		keep bool
	}{
		"recorded but not handed over":       {cluster(false, "my-cilium-manifest"), true},
		"handed over, nothing recorded":      {cluster(true, ""), true},
		"handed over, another name recorded": {cluster(true, "other"), true},
		"handed over and recorded":           {cluster(true, "my-cilium-manifest"), false},
	} {
		t.Run(name, func(t *testing.T) {
			out := mustRender(t, tc.c, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch})
			if got := strings.Contains(out, entry); got != tc.keep {
				t.Fatalf("entry rendered = %v, want %v:\n%s", got, tc.keep, out)
			}

			if !strings.Contains(out, "unrelated.yaml") {
				t.Fatal("the sibling extraManifests entry must survive")
			}
		})
	}
}

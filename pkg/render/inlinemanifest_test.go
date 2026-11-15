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
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

// TestInlineManifestForm: a patch's cluster.inlineManifests entry renders as the KubeInlineManifestConfig
// document on a v1.14 contract and as the v1alpha1 document's cluster.inlineManifests below it, and the hand-over
// removes the CNI entry from either form while a sibling entry and field survive.
func TestInlineManifestForm(t *testing.T) {
	bundle := testBundle(t)
	m := testMachine("node-a", roleControlPlane, nil)

	const (
		newKind    = "kind: KubeInlineManifestConfig"
		legacy     = "inlineManifests:"
		cniName    = "name: " + cni.InlineManifestName
		otherName  = "name: other"
		sibling    = "unrelated.yaml"
		handedOver = true
	)

	patch := inlineManifestPatch("manifests", cni.InlineManifestName)
	patch.Spec.Config.Cluster.InlineManifests["other"] = &ichorconfig.ClusterInlineManifestsEntry{Manifest: "kind: Nothing\n"}

	for _, tc := range []struct {
		name, contract       string
		wantForm, absentForm string
	}{
		{"multidoc", versionContract, newKind, legacy},
		{"legacy", contractV112, legacy, newKind},
	} {
		for _, handover := range []bool{false, handedOver} {
			name := tc.name + "/kept"
			if handover {
				name = tc.name + "/handed over"
			}

			t.Run(name, func(t *testing.T) {
				cluster := testCluster("true")
				cluster.Spec.VersionContract = tc.contract

				if handover {
					apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
						Type: ConditionCNIHandover, Status: metav1.ConditionTrue,
						Reason: handoverCompleteReason, Message: handoverCompleteMessage,
					})
				}

				out := mustRender(t, cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch})

				requireAll(t, out, tc.wantForm, otherName, sibling)
				requireNone(t, out, tc.absentForm)

				if handover {
					requireNone(t, out, cniName)
				} else {
					requireAll(t, out, cniName)
				}
			})
		}
	}
}

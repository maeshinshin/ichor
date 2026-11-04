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

	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
)

// TestInlineManifestForm: a patch's cluster.inlineManifests entry renders as the KubeInlineManifestConfig
// document on a v1.14 contract and as the legacy cluster.inlineManifests below it, and the hand-over
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
	patch.Spec.Documents[0].Config.ClusterConfig.ClusterInlineManifests = append(
		patch.Spec.Documents[0].Config.ClusterConfig.ClusterInlineManifests,
		patch.Spec.Documents[0].Config.ClusterConfig.ClusterInlineManifests[0].DeepCopy(),
	)
	patch.Spec.Documents[0].Config.ClusterConfig.ClusterInlineManifests[1].InlineManifestName = "other"

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

// A document that carries nothing but inline manifests keeps no empty legacy cluster block.
//
//nolint:staticcheck // builds the deprecated legacy field on purpose
func TestTakeInlineManifestsClearsEmptyCluster(t *testing.T) {
	c := &mv1alpha1.Config{ClusterConfig: &mv1alpha1.ClusterConfig{
		ClusterInlineManifests: mv1alpha1.ClusterInlineManifests{{InlineManifestName: "a", InlineManifestContents: "x"}},
	}}

	if got := takeInlineManifests(c); len(got) != 1 {
		t.Fatalf("got %d documents, want 1", len(got))
	}

	if c.ClusterConfig != nil {
		t.Fatalf("ClusterConfig = %+v, want nil", c.ClusterConfig)
	}
}

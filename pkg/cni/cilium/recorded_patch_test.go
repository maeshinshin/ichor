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

package cilium

import (
	"reflect"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
)

// Cilium runs only the images of the chart Ichor embeds (ADR 0115, amendment 2026-11-04 (3)). A patch an
// earlier build recorded in status.cni.imageOverride changes nothing in the values.
func TestEffectiveValuesIgnoreARecordedPatchOverride(t *testing.T) {
	plain := &ichorv1alpha1.TalosCluster{}
	plain.Name, plain.Namespace = "c", "ns"
	plain.Spec.ControlPlane.Count = 1

	patched := plain.DeepCopy()
	patched.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{
		ImageOverride: &ichorv1alpha1.TalosCiliumImageOverride{
			Version: "v1.99.9",
			Images:  map[string]string{"agent": "quay.io/cilium/cilium:v1.99.9"},
		},
	}

	want, err := EffectiveValuesForMode(plain, cni.KubeProxyEnabled)
	if err != nil {
		t.Fatal(err)
	}

	got, err := EffectiveValuesForMode(patched, cni.KubeProxyEnabled)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("a recorded patch override changed the values:\n got  %v\n want %v", got, want)
	}
}

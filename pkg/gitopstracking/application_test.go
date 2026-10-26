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

package gitopstracking

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testApp = "prod-root"

func TestArgoApplicationOf(t *testing.T) {
	cases := []struct {
		name        string
		annotations map[string]string
		labels      map[string]string
		cmData      map[string]string
		want        types.NamespacedName
		wantErr     bool
	}{
		{
			name:        "annotation in the Argo CD namespace",
			annotations: map[string]string{ArgoTrackingAnnotation: testApp + ":ichor.maesh.dev/TalosMachine:default/m1"},
			want:        types.NamespacedName{Namespace: trackingTestArgoCDNamespace, Name: testApp},
		},
		{
			name:        "annotation naming an Application in another namespace",
			annotations: map[string]string{ArgoTrackingAnnotation: "team-a_prod-root:ichor.maesh.dev/TalosMachine:default/m1"},
			want:        types.NamespacedName{Namespace: "team-a", Name: testApp},
		},
		{
			name:   "default instance label when the method is label",
			labels: map[string]string{ArgoDefaultInstanceLabelKey: testApp},
			cmData: map[string]string{ResourceTrackingMethodKey: "label"},
			want:   types.NamespacedName{Namespace: trackingTestArgoCDNamespace, Name: testApp},
		},
		{name: "not tracked by Argo CD", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obj := trackingTestObject()
			obj.Annotations, obj.Labels = tc.annotations, tc.labels

			cm := &corev1.ConfigMap{Name: ArgoConfigMapName, Namespace: trackingTestArgoCDNamespace, Data: tc.cmData}
			k8sClient := fake.NewClientBuilder().WithScheme(trackingTestScheme(t)).WithObjects(cm).Build()

			got, err := ArgoApplicationOf(context.Background(), k8sClient, trackingTestArgoCDNamespace, obj)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("got %v, %v; want %v (err %v)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

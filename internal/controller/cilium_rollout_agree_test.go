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

package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// The adopt preflight and the hand-over controller ask one question of the cilium DaemonSet; they
// must give one answer.
func TestCiliumReadinessAgreesWithPreflight(t *testing.T) {
	ds := func(strategy appsv1.DaemonSetUpdateStrategyType, gen, observed int64, desired, ready, updated int32) *appsv1.DaemonSet {
		return &appsv1.DaemonSet{
			Namespace: cilium.ReleaseNamespace, Name: cilium.ReleaseName, Generation: gen,
			Spec: appsv1.DaemonSetSpec{
				Selector:       &metav1.LabelSelector{MatchLabels: map[string]string{"k": "v"}},
				UpdateStrategy: appsv1.DaemonSetUpdateStrategy{Type: strategy},
			},
			Status: appsv1.DaemonSetStatus{
				ObservedGeneration: observed, DesiredNumberScheduled: desired, NumberReady: ready, UpdatedNumberScheduled: updated,
			},
		}
	}

	for name, tc := range map[string]struct {
		ds   *appsv1.DaemonSet
		want bool
	}{
		"RollingUpdate, all updated and ready":    {ds(appsv1.RollingUpdateDaemonSetStrategyType, 1, 1, 3, 3, 3), true},
		"RollingUpdate mid-rollout must wait":     {ds(appsv1.RollingUpdateDaemonSetStrategyType, 1, 1, 3, 3, 1), false},
		"OnDelete with old pods is not a rollout": {ds(appsv1.OnDeleteDaemonSetStrategyType, 1, 1, 3, 3, 1), true},
		"not ready":               {ds(appsv1.OnDeleteDaemonSetStrategyType, 1, 1, 3, 2, 3), false},
		"generation not observed": {ds(appsv1.RollingUpdateDaemonSetStrategyType, 2, 1, 3, 3, 3), false},
	} {
		t.Run(name, func(t *testing.T) {
			kube := kubefake.NewClientset(tc.ds, &appsv1.Deployment{
				Namespace: cilium.ReleaseNamespace, Name: "cilium-operator",
				Spec:   appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"op": "v"}}},
				Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
			})

			notReady, err := cilium.CheckAgentsReady(context.Background(), kube)
			if err != nil {
				t.Fatal(err)
			}

			scheme := runtime.NewScheme()
			if err := clientgoscheme.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}

			r := &CNIHandoverReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.ds).Build()}

			ready, why, err := r.ciliumReady(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			if got := notReady == nil; got != tc.want || ready != tc.want {
				t.Fatalf("want ready=%v; preflight ready=%v (%v), controller ready=%v (%q)", tc.want, got, notReady, ready, why)
			}
		})
	}
}

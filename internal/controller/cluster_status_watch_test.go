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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

func TestClusterStatusRenderInputChanged(t *testing.T) {
	handover := metav1.Condition{Type: render.ConditionCNIHandover, Status: metav1.ConditionTrue}
	notYet := metav1.Condition{Type: render.ConditionCNIHandover, Status: metav1.ConditionFalse}

	mk := func(c *metav1.Condition, kubeProxy string) *ichorv1alpha1.TalosCluster {
		cl := &ichorv1alpha1.TalosCluster{}
		if c != nil {
			cl.Status.Conditions = []metav1.Condition{*c}
		}

		if kubeProxy != "" {
			cl.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{KubeProxy: kubeProxy}
		}

		return cl
	}

	cases := []struct {
		name     string
		old, new *ichorv1alpha1.TalosCluster
		want     bool
	}{
		{"handover flips to true", mk(&notYet, ""), mk(&handover, ""), true},
		{"handover condition absent to true", mk(nil, ""), mk(&handover, ""), true},
		{"kubeProxy set", mk(nil, ""), mk(nil, "replaced"), true},
		{"kubeProxy changed", mk(nil, "enabled"), mk(nil, "replaced"), true},
		{"unrelated status write", mk(&handover, "replaced"), func() *ichorv1alpha1.TalosCluster {
			c := mk(&handover, "replaced")
			c.Status.Conditions = append(c.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue})

			return c
		}(), false},
		{"handover False to absent", mk(&notYet, ""), mk(nil, ""), false},
	}

	p := clusterStatusRenderInputChanged()

	for _, tc := range cases {
		if got := p.Update(event.UpdateEvent{ObjectOld: tc.old, ObjectNew: tc.new}); got != tc.want {
			t.Errorf("%s: Update = %v, want %v", tc.name, got, tc.want)
		}
	}

	if p.Create(event.CreateEvent{Object: mk(&handover, "")}) {
		t.Error("Create must not fire from this predicate")
	}
}

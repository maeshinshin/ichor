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
	"fmt"
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// The plan an operator approves is built from upgrade.KubernetesOrder; this walks kubernetesBatch to the end the
// way the reconciler does and requires the same stages, steps and machines, so the two cannot drift apart.
func TestKubernetesBatchVisitsTheMachinesInTheOrderThePlanStates(t *testing.T) {
	m := func(role, name string) *ichorv1alpha1.TalosMachine {
		return &ichorv1alpha1.TalosMachine{Name: name, Spec: ichorv1alpha1.TalosMachineSpec{Role: role}}
	}
	cp1, cp2, cp3 := m(roleControlPlane, "cp1"), m(roleControlPlane, "cp2"), m(roleControlPlane, "cp3")
	w1, w2, w3 := m(workerRole, "w1"), m(workerRole, "w2"), m(workerRole, "w3")

	for name, tc := range map[string]struct {
		cps, workers, own []*ichorv1alpha1.TalosMachine
		maxUnavailable    int32
	}{
		"the controller on the middle control plane": {[]*ichorv1alpha1.TalosMachine{cp1, cp3}, []*ichorv1alpha1.TalosMachine{w1, w2, w3}, []*ichorv1alpha1.TalosMachine{cp2}, 2},
		"the controller on a worker":                 {[]*ichorv1alpha1.TalosMachine{cp1, cp2, cp3}, []*ichorv1alpha1.TalosMachine{w1, w3}, []*ichorv1alpha1.TalosMachine{w2}, 1},
		"the controller outside the cluster":         {[]*ichorv1alpha1.TalosMachine{cp1, cp2, cp3}, []*ichorv1alpha1.TalosMachine{w1, w2, w3}, nil, 0},
	} {
		t.Run(name, func(t *testing.T) {
			all := append(append([]*ichorv1alpha1.TalosMachine{}, tc.cps...), tc.own...)
			all = slicesOnly(all, roleControlPlane)
			upgrade.SortByOrder(all)

			want := describeOrder(upgrade.KubernetesOrder(all, tc.cps, tc.workers, tc.own, tc.maxUnavailable))

			op := &ichorv1alpha1.TalosOperation{Spec: ichorv1alpha1.TalosOperationSpec{MaxUnavailable: tc.maxUnavailable}}

			var got []string

			for {
				batch := kubernetesBatch(op, all, tc.cps, tc.workers, tc.own)
				if len(batch) == 0 {
					break
				}

				names := make([]string, len(batch))
				for i, b := range batch {
					names[i] = b.Name
					op.Status.Machines = append(op.Status.Machines, ichorv1alpha1.TalosOperationMachineStatus{
						Name: b.Name, Phase: ichorv1alpha1.TalosOperationMachinePhaseCompleted,
					})
				}

				got = append(got, op.Status.Kubernetes.Stage+": "+strings.Join(names, "+"))
			}

			if g := strings.Join(got, " | "); g != want {
				t.Fatalf("the executor visits\n  %s\nbut upgrade.KubernetesOrder, which the plan states, says\n  %s", g, want)
			}
		})
	}
}

func slicesOnly(ms []*ichorv1alpha1.TalosMachine, role string) (out []*ichorv1alpha1.TalosMachine) {
	for _, m := range ms {
		if m.Spec.Role == role {
			out = append(out, m)
		}
	}

	return out
}

func describeOrder(order []upgrade.KubernetesStageSteps) string {
	var steps []string

	for _, st := range order {
		for _, step := range st.Steps {
			names := make([]string, len(step))
			for i, m := range step {
				names[i] = m.Name
			}

			steps = append(steps, fmt.Sprintf("%s: %s", st.Stage, strings.Join(names, "+")))
		}
	}

	return strings.Join(steps, " | ")
}

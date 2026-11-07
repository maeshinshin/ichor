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

package uiapi

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// ADR 0138: an operation parked because the control-plane count is even holds no lock, so it does not block
// the removal that ends the even count.

func fourControlPlanes(op *ichorv1alpha1.TalosOperation) []client.Object {
	cluster := &ichorv1alpha1.TalosCluster{
		Name: opCluster, Namespace: opNS,
		Spec: ichorv1alpha1.TalosClusterSpec{TalosVersion: opOldTalos, KubernetesVersion: opOldK8s},
		Status: ichorv1alpha1.TalosClusterStatus{Conditions: []metav1.Condition{{
			Type: "Transitioning", Status: metav1.ConditionTrue, Reason: "EvenControlPlaneCount",
			Message: "approved control planes right now: 4", LastTransitionTime: metav1.Now(),
		}}},
	}
	cluster.Spec.ControlPlane.Count = 3

	objs := []client.Object{cluster, opMachine("w1", "worker", nil, nil)}
	for _, n := range []string{"cp1", "cp2", "cp3", "cp4"} {
		objs = append(objs, opMachine(n, "controlplane", nil, nil))
	}

	if op != nil {
		objs = append(objs, op)
	}

	return objs
}

func pausedOp(reason string) *ichorv1alpha1.TalosOperation {
	return &ichorv1alpha1.TalosOperation{
		Name: "ichor-x", Namespace: opNS,
		Spec: ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindIchor},
		Status: ichorv1alpha1.TalosOperationStatus{
			Phase: ichorv1alpha1.TalosOperationPhasePaused,
			Conditions: []metav1.Condition{{
				Type: "Paused", Status: metav1.ConditionTrue, Reason: reason,
				Message: "cluster has 4 approved control planes (even -- a replacement is in progress)", LastTransitionTime: metav1.Now(),
			}},
		},
	}
}

func TestRemoveControlPlaneIsNotBlockedByAnOperationParkedForTheEvenCount(t *testing.T) {
	s, _ := opServer(t, fourControlPlanes(pausedOp("ClusterTransitioning"))...)

	if got := planNodeOK(t, s, nodePlanReq("cp4", pb.NodeAction_NODE_ACTION_REMOVE)).Blocked; got != "" {
		t.Fatalf("remove blocked: %q", got)
	}
}

func TestRemoveControlPlaneIsStillBlockedByARunningOrOtherwisePausedOperation(t *testing.T) {
	for name, op := range map[string]*ichorv1alpha1.TalosOperation{
		"paused by the operator": pausedOp("OperatorPaused"),
		"running": func() *ichorv1alpha1.TalosOperation {
			o := pausedOp("x")
			o.Status.Phase, o.Status.Conditions = ichorv1alpha1.TalosOperationPhaseInProgress, nil

			return o
		}(),
	} {
		s, _ := opServer(t, fourControlPlanes(op)...)

		if got := planNodeOK(t, s, nodePlanReq("cp4", pb.NodeAction_NODE_ACTION_REMOVE)).Blocked; !strings.Contains(got, "not finished") {
			t.Errorf("%s: blocked = %q", name, got)
		}
	}
}

func TestNewOperationIsRefusedWhileTheCountIsEvenAndSaysWhatToDo(t *testing.T) {
	s, _ := opServer(t, fourControlPlanes(nil)...)

	res, err := planOp(s, pb.OperationKind_OPERATION_KIND_TALOS, opNewTalos)
	if err != nil {
		t.Fatal(err)
	}

	if got := res.Msg.Blocked; !strings.Contains(got, "even number") || !strings.Contains(got, "Remove a control plane") {
		t.Errorf("blocked = %q", got)
	}
}

func TestOperationParkedForTheEvenCountTellsItResumesByItself(t *testing.T) {
	s, k8s := opServer(t, fourControlPlanes(pausedOp("ClusterTransitioning"))...)

	got, err := s.unfinishedOperation(t.Context(), getCluster(t, k8s))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, "resumes by itself") || strings.Contains(got, "ichor operation resume") {
		t.Errorf("message = %q", got)
	}
}

func TestResetOfAControlPlaneWhileEvenPointsAtRemove(t *testing.T) {
	s, _ := opServer(t, fourControlPlanes(nil)...)

	if got := planNodeOK(t, s, nodePlanReq("cp4", pb.NodeAction_NODE_ACTION_RESET)).Blocked; !strings.Contains(got, "Remove the node instead") {
		t.Errorf("blocked = %q", got)
	}
}

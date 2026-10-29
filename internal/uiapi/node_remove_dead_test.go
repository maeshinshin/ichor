package uiapi

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// ADR 0106: removing a control plane below the declared count is planned when the control plane does
// not answer (Ready=False NodeUnreachable), and still refused when it does.
func TestNodeRemovePlanBelowTheDeclaredCount(t *testing.T) {
	objs := func(unreachable bool) []client.Object {
		cluster := opObjects()[0].(*ichorv1alpha1.TalosCluster)
		cluster.Spec.ControlPlane.Count = 3

		dead := opMachine("cp1", "controlplane", nil, nil)
		if unreachable {
			dead.Status.Conditions = []metav1.Condition{{
				Type: "Ready", Status: metav1.ConditionFalse, Reason: ichorv1alpha1.TalosMachineReasonNodeUnreachable,
			}}
		}

		return []client.Object{cluster, dead, opMachine("cp2", "controlplane", nil, nil), opMachine("cp3", "controlplane", nil, nil)}
	}

	plan := func(unreachable bool) *pb.PlanOperationResponse {
		s, _ := opServer(t, objs(unreachable)...)

		res, err := s.PlanOperation(context.Background(), nodePlanReq("cp1", pb.NodeAction_NODE_ACTION_REMOVE))
		if err != nil {
			t.Fatal(err)
		}

		return res.Msg
	}

	if p := plan(false); !strings.Contains(p.GetBlocked(), "below the declared count of 3") {
		t.Fatalf("a control plane that answers: blocked = %q", p.GetBlocked())
	}

	p := plan(true)
	if p.GetBlocked() != "" {
		t.Fatalf("an unreachable control plane: blocked = %q", p.GetBlocked())
	}

	if !strings.Contains(p.GetWarning(), "does not answer") || !strings.Contains(p.GetWarning(), "replacement") {
		t.Fatalf("warning = %q", p.GetWarning())
	}

	// Resetting needs the node: the unreachable machine does not lift that refusal.
	s, _ := opServer(t, objs(true)...)

	res, err := s.PlanOperation(context.Background(), nodePlanReq("cp1", pb.NodeAction_NODE_ACTION_RESET))
	if err != nil || !strings.Contains(res.Msg.GetBlocked(), "declared count") {
		t.Fatalf("reset: blocked = %q, %v", res.Msg.GetBlocked(), err)
	}
}

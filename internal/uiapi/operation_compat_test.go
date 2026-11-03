package uiapi

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

func planKubernetesTarget(t *testing.T, cluster *ichorv1alpha1.TalosCluster, target string, bg *pb.Override) error {
	t.Helper()

	srv, _ := opServer(t, cluster, recordedLists(nil, nil, ""))
	_, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_KUBERNETES, Target: target, Override: bg,
	}))

	return err
}

func TestPlanKubernetesMarksACompatibilityRefusalOverridable(t *testing.T) {
	cluster := upgradeCluster(fixtureTalos, "1.20.4")
	cluster.Spec.KubernetesVersion = "v1.36.0"
	err := planKubernetesTarget(t, cluster, "v1.37.0", nil)

	var ce *connect.Error
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !errors.As(err, &ce) || ce.Meta().Get(overridableKey) != overridableValue {
		t.Fatalf("err = %v, want FailedPrecondition marked overridable", err)
	}
}

func TestPlanKubernetesDoesNotMarkAnUnknownVersionOverridable(t *testing.T) {
	cluster := upgradeCluster(fixtureTalos, "1.99.0")
	cluster.Spec.KubernetesVersion = "v1.36.0"
	err := planKubernetesTarget(t, cluster, "v1.37.0", &pb.Override{})

	var ce *connect.Error
	if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument || (errors.As(err, &ce) && ce.Meta().Get(overridableKey) != "") {
		t.Fatalf("err = %v, want a refusal without the overridable mark", err)
	}
}

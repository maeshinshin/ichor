package uiapi

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

func TestNoSignInServesReadOnlyCalls(t *testing.T) {
	call := func(procedure string) error {
		h := AuthInterceptor(nil).WrapUnary(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
			return connect.NewResponse(&emptypb.Empty{}), nil
		})
		req := connect.NewRequest(&emptypb.Empty{})
		_, err := h(context.Background(), &specRequest{AnyRequest: req, procedure: procedure})

		return err
	}

	writes := []string{
		ichorv1alpha1connect.OperationServiceCreateOperationProcedure,
		ichorv1alpha1connect.OperationServiceDeleteOperationProcedure,
		ichorv1alpha1connect.OperationServiceSetOperationPausedProcedure,
		ichorv1alpha1connect.PatchServiceCreatePatchProcedure,
		ichorv1alpha1connect.PatchServiceUpdatePatchProcedure,
		ichorv1alpha1connect.PatchServiceDeletePatchProcedure,
		ichorv1alpha1connect.MachineServiceApproveMachineProcedure,
		ichorv1alpha1connect.MachineServiceRemoveMachineProcedure,
		ichorv1alpha1connect.ClusterServiceAddDiscoveryAddressProcedure,
		ichorv1alpha1connect.CniServiceSetCiliumValuesProcedure,
		ichorv1alpha1connect.CniServiceStartCniUpgradeProcedure,
		ichorv1alpha1connect.SettingsServiceUpdateSettingsProcedure,
		ichorv1alpha1connect.ReclaimServiceCreateReclaimProcedure,
		ichorv1alpha1connect.ReclaimServiceConfirmReclaimProcedure,
		ichorv1alpha1connect.UserServiceCreateUserProcedure,
		ichorv1alpha1connect.OperationServiceRequestEtcdBackupProcedure,
		ichorv1alpha1connect.DiagnosticsServiceDownloadEtcdSnapshotProcedure,
		"/ichor.v1alpha1.FutureService/DoSomething",
	}
	for _, p := range writes {
		if err := call(p); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("%s without sign-in: %v, want PermissionDenied", p, err)
		}
	}

	for p := range readOnlyProcedures {
		if err := call(p); err != nil {
			t.Errorf("read-only %s refused: %v", p, err)
		}
	}
}

// specRequest overrides the procedure a connect.Request reports.
type specRequest struct {
	connect.AnyRequest
	procedure string
}

func (r *specRequest) Spec() connect.Spec { return connect.Spec{Procedure: r.procedure} }

package uiapi

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// TestAnAnonymousCallerStartsNoDestructiveOperation calls CreateOperation for each of the six
// destructive starts with nobody signed in and no caller credentials: the backend on its own
// ServiceAccount. Each must be refused and write nothing. The same request from a signed-in user
// whose RBAC allows it is created, so the refusal is the gate and not a malformed request.
//
// The six are written out, not read from StartSubresource: that function is what the gate asks.
func TestAnAnonymousCallerStartsNoDestructiveOperation(t *testing.T) {
	for name, tc := range map[string]struct {
		objs []client.Object
		req  func() *connect.Request[pb.CreateOperationRequest]
	}{
		"shutdown": {opObjects(), func() *connect.Request[pb.CreateOperationRequest] {
			return opCreate(pb.OperationKind_OPERATION_KIND_SHUTDOWN, "")
		}},
		"ca-rotation": {opObjects(), func() *connect.Request[pb.CreateOperationRequest] {
			return opCreate(pb.OperationKind_OPERATION_KIND_CA_ROTATION, "")
		}},
		"key-rotation": {keyObjects("v1.14"), func() *connect.Request[pb.CreateOperationRequest] { return keyCreate(nil) }},
		"etcd-restore": {restoreObjects(), func() *connect.Request[pb.CreateOperationRequest] {
			return restoreCreateReq(restoreReq(restoreSnap, opCluster, true))
		}},
		"node-reset": {opObjects(), func() *connect.Request[pb.CreateOperationRequest] { return resetReq("w1", true) }},
		"node-shutdown": {opObjects(), func() *connect.Request[pb.CreateOperationRequest] {
			return nodeCreateReq(&pb.NodeActionRequest{Machine: objectRef(opNS, "w1"), Action: pb.NodeAction_NODE_ACTION_SHUTDOWN})
		}},
	} {
		t.Run(name, func(t *testing.T) {
			srv, k8s := opServer(t, tc.objs...)

			_, err := srv.CreateOperation(context.Background(), tc.req())
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("anonymous: err = %v, want PermissionDenied", err)
			}

			if ops := listOps(t, k8s); len(ops) != 0 {
				t.Fatalf("anonymous: the refused start was written: %+v", ops)
			}

			if _, err := srv.CreateOperation(signedInWhoMayDoAnything(srv, k8s), tc.req()); err != nil {
				t.Fatalf("signed in, RBAC allows: %v", err)
			}

			if ops := listOps(t, k8s); len(ops) != 1 {
				t.Fatalf("signed in: operations = %+v", ops)
			}
		})
	}
}

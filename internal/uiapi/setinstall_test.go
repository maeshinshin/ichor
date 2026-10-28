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
	"context"
	"testing"

	"connectrpc.com/connect"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

func installReq(in *pb.MachineInstall) *connect.Request[pb.SetMachineInstallRequest] {
	return connect.NewRequest(&pb.SetMachineInstallRequest{Machine: objectRef(testNS, "m1"), Install: in})
}

func TestSetMachineInstallWritesOnlyInstall(t *testing.T) {
	m := testMachine()
	m.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateApproved
	m.Spec.Role = roleWorker
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(m).WithStatusSubresource(m).Build()
	s := &Server{Client: k8s}
	key := types.NamespacedName{Namespace: testNS, Name: "m1"}

	// no install, ambiguous disk: refused.
	for _, bad := range []*pb.MachineInstall{nil, {Disk: &pb.MachineDisk{Wwid: "w", Serial: "s"}}} {
		_, err := s.SetMachineInstall(context.Background(), installReq(bad))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("want InvalidArgument, got %v", err)
		}
	}

	resp, err := s.SetMachineInstall(context.Background(), installReq(&pb.MachineInstall{Disk: &pb.MachineDisk{Wwid: "w"}}))
	if err != nil {
		t.Fatal(err)
	}

	if resp.Msg.GetAudit().GetAction() != "set-install" {
		t.Fatalf("audit action %q", resp.Msg.GetAudit().GetAction())
	}

	var got ichorv1alpha1.TalosMachine
	if err := k8s.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}

	if got.Spec.Install == nil || got.Spec.Install.Disk.WWID != "w" || got.Spec.Role != roleWorker || got.Spec.ApprovalDecision != nil {
		t.Fatalf("want only spec.install changed, got %+v", got.Spec)
	}

	// once the machine has converged an install target no longer applies.
	got.Status.AppliedConfigHash = "sha256:x"
	if err := k8s.Status().Update(context.Background(), &got); err != nil {
		t.Fatal(err)
	}

	_, err = s.SetMachineInstall(context.Background(), installReq(&pb.MachineInstall{Disk: &pb.MachineDisk{Wwid: "w2"}}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("want FailedPrecondition for a converged machine, got %v", err)
	}
}

func TestSetMachineInstallRefusesAPendingMachine(t *testing.T) {
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(testMachine()).Build()

	_, err := (&Server{Client: k8s}).SetMachineInstall(context.Background(), installReq(&pb.MachineInstall{Disk: &pb.MachineDisk{Wwid: "w"}}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}
}

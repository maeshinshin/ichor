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
	"strings"
	"testing"

	"connectrpc.com/connect"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// The TalosMachine webhook asks for approve on talosmachines/approval as whoever writes.
// With no sign-in that is ichor-ui's ServiceAccount, which does not hold it by design, so the refusal
// says what to do instead of quoting the bare verb.
func TestApproveDeniedByTheApproveRightSaysWhatToDo(t *testing.T) {
	denied := apierrors.NewForbidden(schema.GroupResource{Group: ichorv1alpha1.GroupVersion.Group, Resource: "talosmachines"}, "m1", nil)
	denied.ErrStatus.Message = `admission webhook "vtalosmachine-v1alpha1.kb.io" denied the request: approving a machine ` +
		`hands it the cluster's configuration: you need the "approve" verb on talosmachines/approval`

	base := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(testMachine()).Build()
	denyingClient := interceptor.NewClient(base, interceptor.Funcs{
		Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error { return denied },
	})

	req := func() *connect.Request[pb.ApproveMachineRequest] {
		return connect.NewRequest(&pb.ApproveMachineRequest{
			Machine: objectRef(testNS, "m1"), Role: pb.MachineRole_MACHINE_ROLE_WORKER,
		})
	}

	// No sign-in: ichor-ui acts as its ServiceAccount.
	s := &Server{Client: denyingClient}

	_, err := s.ApproveMachine(context.Background(), req())
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("want PermissionDenied, got %v", err)
	}

	for _, want := range []string{`"approve" verb`, "ServiceAccount", "--local", "ichor-operator"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("no sign-in: %q missing from %q", want, err)
		}
	}

	// A signed-in user: the hint is about their binding, not the ServiceAccount.
	s = &Server{Client: base, Impersonate: func(Identity) (client.Client, error) { return denyingClient, nil }}

	_, err = s.ApproveMachine(asUser(context.Background()), req())
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("want PermissionDenied, got %v", err)
	}

	if !strings.Contains(err.Error(), "ichor-operator") || strings.Contains(err.Error(), "ServiceAccount") {
		t.Errorf("signed in: %q", err)
	}
}

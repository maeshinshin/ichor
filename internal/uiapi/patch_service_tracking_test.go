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
	"errors"
	"testing"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// ADR 0065: when tracking cannot be determined the patch is not written (Unavailable), whatever
// the break-glass flag says: the flag only chooses between a PR and a direct write for a tracked CR.
func TestPatchWritesUnavailableWhenTrackingCannotBeDetermined(t *testing.T) {
	base := fake.NewClientBuilder().WithScheme(trackedScheme(t)).WithObjects(trackedPatch(), argoCM()).Build()
	k8s := interceptor.NewClient(base, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.ConfigMap); ok {
				return errors.New("forbidden")
			}

			return c.Get(ctx, key, obj, opts...)
		},
	})
	s := &Server{Client: k8s}
	glass := &pb.BreakGlass{Reason: "incident"}

	_, err := s.UpdatePatch(context.Background(), connect.NewRequest(&pb.UpdatePatchRequest{Patch: sysctlPatchWire("p1", "c"), BreakGlass: glass}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("update: want Unavailable, got %v", err)
	}

	_, err = s.DeletePatch(context.Background(), connect.NewRequest(&pb.DeletePatchRequest{Patch: objectRef(trackedTestNS, "p1"), BreakGlass: glass}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("delete: want Unavailable, got %v", err)
	}

	if err := base.Get(context.Background(), types.NamespacedName{Namespace: trackedTestNS, Name: "p1"}, &ichorv1alpha1.TalosConfigPatch{}); err != nil {
		t.Fatalf("the patch must still exist: %v", err)
	}
}

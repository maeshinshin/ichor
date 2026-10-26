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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Strict reads (docs/adr/0062): what the ServiceAccount reads and a caller
// then sees must be read as the caller. The "user" is the same fake cluster
// behind an interceptor that refuses the one kind a test wants denied.

func denyApplications(base client.Client) client.Client {
	return interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*unstructured.Unstructured); ok && obj.GetObjectKind().GroupVersionKind().Kind == "Application" {
				return apierrors.NewForbidden(schema.GroupResource{Group: "argoproj.io", Resource: "applications"}, key.Name, nil)
			}

			return c.Get(ctx, key, obj, opts...)
		},
	})
}

func denyNodes(base client.Client) client.Client {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", nil)

	return interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Node); ok {
				return forbidden
			}

			return c.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*corev1.NodeList); ok {
				return forbidden
			}

			return c.List(ctx, list, opts...)
		},
	})
}

func asUser(ctx context.Context) context.Context {
	return context.WithValue(ctx, identityKey{}, Identity{User: testUserBob})
}

func (e *prEnv) impersonateWith(user client.Client) {
	e.srv.Impersonate = func(Identity) (client.Client, error) { return user, nil }
}

func listGitOpsAs(ctx context.Context, t *testing.T, e *prEnv) map[string]*pb.GitOpsItem {
	t.Helper()

	res, err := e.srv.ListGitOps(ctx, connect.NewRequest(&pb.ListGitOpsRequest{}))
	if err != nil {
		t.Fatal(err)
	}

	out := map[string]*pb.GitOpsItem{}
	for _, it := range res.Msg.GetItems() {
		out[it.GetKind().String()+"/"+it.GetRef().GetName()] = it
	}

	return out
}

func TestListGitOpsHidesApplicationStateFromACallerWhoCannotReadIt(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})
	setAppStatus(t, e, testOutOfSync, testDegraded)
	e.impersonateWith(denyApplications(e.k8s))

	items := listGitOpsAs(asUser(context.Background()), t, e)

	m := items["GIT_OPS_KIND_MACHINE/m1"]
	if m == nil || !m.GetPermissionDenied() || m.GetSyncStatus() != "" || m.GetHealthStatus() != "" || m.GetError() != "" {
		t.Fatalf("machine row = %v", m)
	}

	// The rest of the page still renders.
	if p := items["GIT_OPS_KIND_PATCH/p1"]; p == nil || !p.GetPermissionDenied() {
		t.Fatalf("patch row = %v", p)
	}

	if c := items["GIT_OPS_KIND_CLUSTER/c"]; c == nil || c.GetPermissionDenied() {
		t.Fatalf("cluster row = %v", c)
	}
}

func TestListGitOpsShowsApplicationStateToACallerWhoMayRead(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})
	setAppStatus(t, e, testOutOfSync, testDegraded)
	e.impersonateWith(e.k8s)

	m := listGitOpsAs(asUser(context.Background()), t, e)["GIT_OPS_KIND_MACHINE/m1"]
	if m.GetPermissionDenied() || m.GetSyncStatus() != testOutOfSync || m.GetHealthStatus() != testDegraded {
		t.Fatalf("machine row = %v", m)
	}
}

func TestListGitOpsWithoutAnIdentityReadsAsTheServiceAccount(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})
	setAppStatus(t, e, "Synced", "Healthy")
	// OIDC disabled: no Impersonate wired and no identity (docs/adr/0058, point 4).
	e.srv.Impersonate = nil

	m := listGitOpsAs(context.Background(), t, e)["GIT_OPS_KIND_MACHINE/m1"]
	if m.GetPermissionDenied() || m.GetSyncStatus() != "Synced" {
		t.Fatalf("machine row = %v", m)
	}
}

func TestKubernetesNodeIsReadAsTheCaller(t *testing.T) {
	node := &corev1.Node{}
	node.Name = "n1"

	k8s := fake.NewClientBuilder().WithScheme(trackedScheme(t)).WithObjects(node).Build()
	srv := &Server{Client: k8s, Impersonate: func(Identity) (client.Client, error) { return denyNodes(k8s), nil }}

	m := &ichorv1alpha1.TalosMachine{Spec: ichorv1alpha1.TalosMachineSpec{Hostname: "n1"}}

	if _, err := srv.findKubernetesNode(asUser(context.Background()), m); !apierrors.IsForbidden(err) {
		t.Fatalf("a caller who may not read Nodes must be refused, got %v", err)
	}

	got, err := srv.findKubernetesNode(context.Background(), m)
	if err != nil || got == nil {
		t.Fatalf("without an identity the ServiceAccount reads: %v %v", got, err)
	}
}

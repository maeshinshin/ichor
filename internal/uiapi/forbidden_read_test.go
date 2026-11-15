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
	"io"
	"log"
	"strings"
	"testing"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	forbiddenReadUser = "alice"
	roleKindName      = "Role"
)

func sanitized(err error) error {
	return sanitizer{l: log.New(io.Discard, "", 0)}.clean("/ichor.v1alpha1.X/Y", err)
}

// A user bound in one namespace only is refused by the real API server, not by a fake error.
var _ = Describe("a read the caller's RBAC forbids", func() {
	var s *Server

	asAlice := func() context.Context {
		return ContextWithIdentity(ctx, Identity{User: forbiddenReadUser})
	}

	BeforeEach(func() {
		role := &rbacv1.Role{
			Name: "forbidden-read", Namespace: testNamespace,
			Rules: []rbacv1.PolicyRule{{
				APIGroups: []string{ichorv1alpha1.GroupVersion.Group},
				Resources: []string{"talosclusters", resourceTalosMachines},
				Verbs:     []string{cniVerbGet, cniVerbList},
			}},
		}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, role))).To(Succeed())
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.RoleBinding{
			Name: "forbidden-read", Namespace: testNamespace,
			RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: roleKindName, Name: role.Name},
			Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: forbiddenReadUser}},
		}))).To(Succeed())

		s = &Server{
			Client: k8sClient, ControllerNamespace: testNamespace,
			Impersonate: NewImpersonator(cfg, client.Options{Scheme: scheme.Scheme}),
		}
	})

	expectDenied := func(err error) {
		GinkgoHelper()
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)
		Expect(connect.CodeOf(sanitized(err))).To(Equal(connect.CodePermissionDenied))
		Expect(sanitized(err).Error()).NotTo(ContainSubstring("reference"), "the sanitiser must leave a refusal alone")
		Expect(err.Error()).To(ContainSubstring(forbiddenReadUser))
	}

	It("is PermissionDenied for the cluster-wide ListClusters", func() {
		_, err := s.ListClusters(asAlice(), connect.NewRequest(&pb.ListClustersRequest{}))
		expectDenied(err)
	})

	It("is PermissionDenied for the cluster-wide ListMachines", func() {
		_, err := s.ListMachines(asAlice(), connect.NewRequest(&pb.ListMachinesRequest{}))
		expectDenied(err)
	})

	It("is PermissionDenied for the cluster-wide ListPatches", func() {
		_, err := s.ListPatches(asAlice(), connect.NewRequest(&pb.ListPatchesRequest{}))
		expectDenied(err)
	})

	It("is PermissionDenied for the patches GetEffectiveConfig lists in the machine's namespace", func() {
		secret := newTestSecret(newTestBundle())
		cluster := newTestCluster(secret.Name)
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-forbidden-read"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)

		_, err := s.GetEffectiveConfig(asAlice(), connect.NewRequest(&pb.GetEffectiveConfigRequest{
			Machine: objectRef(m.Namespace, m.Name),
		}))
		expectDenied(err)
	})
})

func forbidden(resource string) error {
	return apierrors.NewForbidden(schema.GroupResource{Group: ichorv1alpha1.GroupVersion.Group, Resource: resource}, "x", errors.New("no"))
}

func failing(t *testing.T, objs []client.Object, funcs interceptor.Funcs) client.Client {
	t.Helper()

	return interceptor.NewClient(fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build(), funcs)
}

// The same calls where the failure is the server's own (the API server is down, not saying no) stay
// Internal, and the sanitiser still hides them behind a reference.
func TestAGenuineListFailureStaysInternalAndIsSanitised(t *testing.T) {
	boom := errors.New("etcdserver: request timed out at 10.1.2.3")
	c := failing(t, nil, interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error { return boom },
	})
	s := &Server{Client: c}
	bg := context.Background()

	calls := map[string]func() error{
		"ListClusters": func() error { _, err := s.ListClusters(bg, connect.NewRequest(&pb.ListClustersRequest{})); return err },
		"ListMachines": func() error { _, err := s.ListMachines(bg, connect.NewRequest(&pb.ListMachinesRequest{})); return err },
		"ListPatches":  func() error { _, err := s.ListPatches(bg, connect.NewRequest(&pb.ListPatchesRequest{})); return err },
	}

	for name, call := range calls {
		err := call()
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("%s: code = %v, want Internal", name, connect.CodeOf(err))
		}

		out := sanitized(err)
		if connect.CodeOf(out) != connect.CodeInternal || !strings.Contains(out.Error(), "reference") || strings.Contains(out.Error(), "10.1.2.3") {
			t.Errorf("%s: sanitised = %v", name, out)
		}
	}
}

// The reads and writes below sit behind the caller's own client; a refusal from Kubernetes RBAC is theirs to see.
func TestAForbiddenListOrWriteIsPermissionDenied(t *testing.T) {
	bg := context.Background()

	t.Run("approving a control plane lists the namespace's machines", func(t *testing.T) {
		pending := testMachine()
		pending.Status.DiscoveredLinks = []ichorv1alpha1.TalosMachineDiscoveredLink{{Name: vipTestLink, PermanentMAC: testMAC, LinkUp: true}}
		c := failing(t, []client.Object{pending, vipCluster("vip"), firstControlPlane()}, interceptor.Funcs{
			List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return forbidden(resourceTalosMachines)
			},
		})

		_, err := (&Server{Client: c}).ApproveMachine(bg, connect.NewRequest(&pb.ApproveMachineRequest{
			Machine: objectRef(testNS, "m1"), Role: pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE,
		}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("code = %v: %v", connect.CodeOf(err), err)
		}
	})

	denyUpdate := interceptor.Funcs{
		Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
			return forbidden(resourceTalosMachines)
		},
	}

	t.Run("approving without the approve right in the message", func(t *testing.T) {
		_, err := (&Server{Client: failing(t, []client.Object{testMachine()}, denyUpdate)}).ApproveMachine(bg, connect.NewRequest(&pb.ApproveMachineRequest{
			Machine: objectRef(testNS, "m1"), Role: pb.MachineRole_MACHINE_ROLE_WORKER,
		}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("code = %v: %v", connect.CodeOf(err), err)
		}
	})

	t.Run("rejecting", func(t *testing.T) {
		_, err := (&Server{Client: failing(t, []client.Object{testMachine()}, denyUpdate)}).RejectMachine(bg, connect.NewRequest(&pb.RejectMachineRequest{
			Machine: objectRef(testNS, "m1"),
		}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("code = %v: %v", connect.CodeOf(err), err)
		}
	})

	t.Run("setting the install disk", func(t *testing.T) {
		m := testMachine()
		m.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateApproved
		m.Spec.Role = roleWorker

		_, err := (&Server{Client: failing(t, []client.Object{m, testInstallCluster("")}, denyUpdate)}).SetMachineInstall(bg,
			installReq(&pb.MachineInstall{Disk: &pb.MachineDisk{Wwid: "w"}}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("code = %v: %v", connect.CodeOf(err), err)
		}
	})
}

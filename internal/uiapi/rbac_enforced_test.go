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
	"os"
	"strings"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// envtest's kube-apiserver enforces RBAC, so these specs run handlers as the real ichor-ui
// ServiceAccount identity bound to the generated role (config/rbac/ichor-ui/role.yaml, from
// rbac_markers.go). A read the markers do not grant surfaces as Forbidden here instead of in a
// live cluster (docs/adr/0018, the missing talosstorages rule).
var _ = Describe("ichor-ui generated RBAC", func() {
	const user = "system:serviceaccount:ichor-system:ichor-ui"

	var saClient client.Client

	BeforeEach(func() {
		raw, err := os.ReadFile("../../config/rbac/ichor-ui/role.yaml")
		Expect(err).NotTo(HaveOccurred())

		for _, doc := range strings.Split(string(raw), "\n---\n") {
			var meta struct{ Kind string }
			Expect(yaml.Unmarshal([]byte(doc), &meta)).To(Succeed())

			switch meta.Kind {
			case "ClusterRole":
				var cr rbacv1.ClusterRole
				Expect(yaml.Unmarshal([]byte(doc), &cr)).To(Succeed())
				cr.Name = "ichor-ui-role-test"
				Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &cr))).To(Succeed())
				Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
					ObjectMeta: metav1.ObjectMeta{Name: "ichor-ui-binding-test"},
					RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: cr.Name},
					Subjects:   []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: "User", Name: user}},
				}))).To(Succeed())
			case "Role":
				// The Role lives in ichor-system; the specs run with the controller namespace
				// testNamespace, so the same rules are bound there.
				var r rbacv1.Role
				Expect(yaml.Unmarshal([]byte(doc), &r)).To(Succeed())
				r.Namespace, r.Name = testNamespace, "ichor-ui-role-test"
				Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &r))).To(Succeed())
				Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{Name: "ichor-ui-binding-test", Namespace: testNamespace},
					RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: r.Name},
					Subjects:   []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: "User", Name: user}},
				}))).To(Succeed())
			}
		}

		c := rest.CopyConfig(cfg)
		c.Impersonate = rest.ImpersonationConfig{UserName: user}
		saClient, err = client.New(c, client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())
	})

	It("lets GetEffectiveConfig render, including its TalosStorage and registry-secret reads", func() {
		secret := newTestSecret(newTestBundle())
		cluster := newTestCluster(secret.Name)
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-rbac"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)

		s := &Server{Client: saClient, ControllerNamespace: testNamespace}
		_, err := s.GetEffectiveConfig(ctx, connect.NewRequest(&pb.GetEffectiveConfigRequest{
			Machine: objectRef(m.Namespace, m.Name),
		}))
		Expect(err).NotTo(HaveOccurred())
	})
})

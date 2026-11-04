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

package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/release"
)

const (
	kindClusterRole = "ClusterRole"
	kindUser        = "User"
	verbGet         = "get"
)

// bindRole binds a ClusterRole with rules to the user (the webhook asks real RBAC, as in
// grantOperationRights).
func bindRole(name, user string, rules ...rbacv1.PolicyRule) {
	role := &rbacv1.ClusterRole{Name: name, Rules: rules}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, role))).To(Succeed())
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
		Name:     name,
		RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: kindClusterRole, Name: name},
		Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: kindUser, Name: user}},
	}))).To(Succeed())
}

// ADR 0094, Decision 4: changing spec.authentication needs update on talosclusters/authentication,
// and the new value must pass the apiserver's own validation on a v1.14 contract.
var _ = Describe("Authentication gate on TalosCluster", func() {
	const (
		editor    = "auth-gate-editor"
		authAdmin = "auth-gate-admin"
	)

	external := func(issuer string) ichorv1alpha1.TalosClusterExternalIssuer {
		return ichorv1alpha1.TalosClusterExternalIssuer{
			IssuerURL: issuer, Audiences: []string{"ichor"},
			UsernameClaim: "email", GroupsClaim: "groups", UsernamePrefix: "oidc:", GroupsPrefix: "oidc:",
		}
	}

	var cluster *ichorv1alpha1.TalosCluster

	BeforeEach(func() {
		clusters := []string{"talosclusters"}
		get := rbacv1.PolicyRule{APIGroups: []string{ichorGroup}, Resources: clusters, Verbs: []string{verbGet, verbUpdate, verbPatch}}
		bindRole("auth-gate-editor", editor, get)
		bindRole("auth-gate-admin", authAdmin, get, rbacv1.PolicyRule{
			APIGroups: []string{ichorGroup}, Resources: []string{"talosclusters/authentication"}, Verbs: []string{AuthenticationVerb},
		})
		bindRole("auth-gate-controller", testControllerUser, get)

		cluster = newTestCluster()
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
	})

	AfterEach(func() {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster); err != nil {
			return
		}

		cluster.Annotations = map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
		Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
	})

	set := func(user string, a *ichorv1alpha1.TalosClusterAuthentication) error {
		c := &ichorv1alpha1.TalosCluster{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), c)).To(Succeed())
		c.Spec.Authentication = a

		return asUser(user).Update(ctx, c)
	}

	It("lets an editor change other fields", func() {
		c := &ichorv1alpha1.TalosCluster{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), c)).To(Succeed())
		c.Spec.KubernetesVersion = release.Pinned.Kubernetes
		Expect(asUser(editor).Update(ctx, c)).To(Succeed())
	})

	It("refuses an editor who changes spec.authentication", func() {
		err := set(editor, &ichorv1alpha1.TalosClusterAuthentication{External: []ichorv1alpha1.TalosClusterExternalIssuer{external("https://idp.example.org")}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("talosclusters/authentication"))
	})

	It("admits a holder of update on talosclusters/authentication", func() {
		Expect(set(authAdmin, &ichorv1alpha1.TalosClusterAuthentication{
			External: []ichorv1alpha1.TalosClusterExternalIssuer{external("https://idp.example.org")},
		})).To(Succeed())
	})

	It("admits Ichor's own controller", func() {
		Expect(set(testControllerUser, &ichorv1alpha1.TalosClusterAuthentication{
			External: []ichorv1alpha1.TalosClusterExternalIssuer{external("https://idp.example.org")},
		})).To(Succeed())
	})

	It("refuses an issuer URL listed twice, with the apiserver's own error", func() {
		err := set(authAdmin, &ichorv1alpha1.TalosClusterAuthentication{
			Ichor:    &ichorv1alpha1.TalosClusterIchorIssuer{Enabled: true, URL: "https://ichor.example.com"},
			External: []ichorv1alpha1.TalosClusterExternalIssuer{external("https://ichor.example.com")},
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Duplicate"))
	})

	It("refuses spec.authentication below the v1.14 contract", func() {
		old := newTestCluster()
		old.Spec.VersionContract = "v1.13"
		Expect(k8sClient.Create(ctx, old)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(old), old)).To(Succeed())
			old.Annotations = map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue}
			Expect(k8sClient.Update(ctx, old)).To(Succeed())
			Expect(k8sClient.Delete(ctx, old)).To(Succeed())
		})

		old.Spec.Authentication = &ichorv1alpha1.TalosClusterAuthentication{
			External: []ichorv1alpha1.TalosClusterExternalIssuer{external("https://idp.example.org")},
		}

		err := asUser(authAdmin).Update(ctx, old)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("v1.14"))
	})

	It("keeps a prefix of ichor: for Ichor's own users (the CRD rule)", func() {
		e := external("https://idp.example.org")
		e.UsernamePrefix = "ichor:admin-"

		err := set(authAdmin, &ichorv1alpha1.TalosClusterAuthentication{External: []ichorv1alpha1.TalosClusterExternalIssuer{e}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("reserved"))
	})
})

// ADR 0094, Decision 4: starting a destructive TalosOperation needs the verb start on the
// subresource of its kind, whichever way the CR is written.
var _ = Describe("Start gate on TalosOperation", func() {
	const (
		creator = "start-gate-creator"
		starter = "start-gate-starter"
	)

	op := func(spec ichorv1alpha1.TalosOperationSpec) *ichorv1alpha1.TalosOperation {
		spec.ClusterRef = corev1.LocalObjectReference{Name: testClusterName}

		return &ichorv1alpha1.TalosOperation{GenerateName: "start-", Namespace: testNamespace, Spec: spec}
	}

	create := rbacv1.PolicyRule{APIGroups: []string{ichorGroup}, Resources: []string{"talosoperations"}, Verbs: []string{"create", verbGet}}

	BeforeEach(func() {
		bindRole("start-gate-creator", creator, create)
		bindRole("start-gate-starter", starter, create, rbacv1.PolicyRule{
			APIGroups: []string{ichorGroup}, Verbs: []string{ichorv1alpha1.OperationStartVerb},
			Resources: []string{"talosoperations/shutdown", "talosoperations/ca-rotation", "talosoperations/node-shutdown"},
		})
		bindRole("start-gate-controller", testControllerUser, create)
	})

	shutdown := func() *ichorv1alpha1.TalosOperation {
		return op(ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindShutdown})
	}

	nodeShutdown := func() *ichorv1alpha1.TalosOperation {
		return op(ichorv1alpha1.TalosOperationSpec{
			Kind: ichorv1alpha1.TalosOperationKindNode,
			Node: &ichorv1alpha1.TalosOperationNodeSpec{
				Action: ichorv1alpha1.NodeActionShutdown, MachineRef: corev1.LocalObjectReference{Name: "some-node"},
			},
		})
	}

	It("refuses a shutdown from a user without start on talosoperations/shutdown", func() {
		err := asUser(creator).Create(ctx, shutdown())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("talosoperations/shutdown"))
	})

	It("admits a shutdown from a user who holds start on it", func() {
		Expect(asUser(starter).Create(ctx, shutdown())).To(Succeed())
	})

	It("asks for node-shutdown on the node action shutdown", func() {
		err := asUser(creator).Create(ctx, nodeShutdown())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("talosoperations/node-shutdown"))
		Expect(asUser(starter).Create(ctx, nodeShutdown())).To(Succeed())
	})

	It("asks for ca-rotation on a CA rotation", func() {
		err := asUser(creator).Create(ctx, op(ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindCARotation}))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("talosoperations/ca-rotation"))
	})

	It("asks for key-rotation on a key rotation, a verb apart from ca-rotation", func() {
		keys := op(ichorv1alpha1.TalosOperationSpec{
			Kind:        ichorv1alpha1.TalosOperationKindKeyRotation,
			KeyRotation: &ichorv1alpha1.TalosOperationKeyRotationSpec{Targets: []string{ichorv1alpha1.KeyRotationTargetSecretbox}},
		})

		err := asUser(creator).Create(ctx, keys)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("talosoperations/key-rotation"))

		// start on ca-rotation is not start on key-rotation.
		err = asUser(starter).Create(ctx, keys.DeepCopy())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("talosoperations/key-rotation"))
	})

	It("does not ask for start on a non-destructive kind", func() {
		Expect(asUser(creator).Create(ctx, op(ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindTalos}))).To(Succeed())
	})

	It("admits Ichor's own controller", func() {
		Expect(asUser(testControllerUser).Create(ctx, shutdown())).To(Succeed())
	})
})

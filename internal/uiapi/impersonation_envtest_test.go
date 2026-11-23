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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	authenticationv1 "k8s.io/api/authentication/v1"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func userRule(name string) rbacv1.PolicyRule {
	return rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"users"}, Verbs: []string{"impersonate"}, ResourceNames: []string{name}}
}

// The impersonation grants ichor-ui holds (the verb `impersonate` on named users and groups) must let it
// act as a signed-in user and ask "may I?" for that user, on the Kubernetes the suite runs.
var _ = Describe("ichor-ui impersonation grants", func() {
	// saConfig makes a ServiceAccount whose only grant is rules (a ClusterRole named after it), and returns a
	// rest.Config that authenticates as it.
	saConfig := func(name string, rules ...rbacv1.PolicyRule) *rest.Config {
		sa := &corev1.ServiceAccount{Name: name, Namespace: testNamespace}
		Expect(k8sClient.Create(ctx, sa)).To(Succeed())
		Expect(k8sClient.Create(ctx, &rbacv1.ClusterRole{Name: name, Rules: rules})).To(Succeed())
		Expect(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
			Name:     name,
			RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: cniClusterRole, Name: name},
			Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: sa.Name, Namespace: sa.Namespace}},
		})).To(Succeed())

		admin, err := kubernetes.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())

		tok, err := admin.CoreV1().ServiceAccounts(sa.Namespace).CreateToken(ctx, sa.Name, &authenticationv1.TokenRequest{}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		c := rest.AnonymousClientConfig(cfg)
		c.BearerToken = tok.Status.Token

		return c
	}

	It("lets the ServiceAccount impersonate a named user and group and create a SelfSubjectAccessReview", func() {
		c := saConfig("impprobe", userRule("ichor:probe"),
			rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"groups"}, Verbs: []string{"impersonate"}, ResourceNames: []string{"ichor:viewer"}})
		c.Impersonate = rest.ImpersonationConfig{UserName: "ichor:probe", Groups: []string{"ichor:viewer"}}
		cs, err := kubernetes.NewForConfig(c)
		Expect(err).NotTo(HaveOccurred())

		_, err = cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authv1.SelfSubjectAccessReview{
			Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authv1.ResourceAttributes{Verb: cniVerbGet, Resource: "pods"}},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
	})

	// A user can sign in the moment its IchorUser exists, but ichor-ui-impersonate gains its rule only when
	// the controller reconciles. A call in that window must wait for the rule, then say "still being set
	// up", never fail as an internal error.
	Context("an IchorUser whose impersonation rule has not landed", func() {
		late := Identity{User: "ichor:late", Source: SourceIchor}

		It("refuses with a retryable message once the wait is over", func() {
			c := saConfig("impnorule", userRule("ichor:other"))

			_, err := newImpersonator(c, client.Options{Scheme: k8sscheme.Scheme}, 300*time.Millisecond)(late)
			Expect(err).To(MatchError(ContainSubstring("still being set up")))
		})

		It("succeeds when the rule lands during the wait", func() {
			c := saConfig("impdelayed")

			done := make(chan struct{})

			go func() {
				defer GinkgoRecover()
				defer close(done)
				time.Sleep(500 * time.Millisecond)

				var cr rbacv1.ClusterRole
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "impdelayed"}, &cr)).To(Succeed())
				cr.Rules = []rbacv1.PolicyRule{userRule("ichor:late")}
				Expect(k8sClient.Update(ctx, &cr)).To(Succeed())
			}()

			_, err := newImpersonator(c, client.Options{Scheme: k8sscheme.Scheme}, 10*time.Second)(late)
			Expect(err).NotTo(HaveOccurred())
			<-done
		})
	})
})

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
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/gitopstracking"
)

// The users the break-glass suite acts as. testControllerUser and
// testArgoUser are the exempt names the suite's webhook is set up with
// (see webhook_suite_test.go).
const (
	testControllerUser = "system:serviceaccount:ichor-system:ichor-controller"
	testArgoUser       = "system:serviceaccount:argocd:argocd-application-controller"
	plainUser          = "plain-user"
	glassUser          = "glass-user"
	testFinalizer      = "ichor.maesh.dev/machine-removal"
	testArgoNamespace  = "argocd"
	testClusterName    = "cluster"
)

// asUser returns a client that makes requests as name (impersonated, so the
// webhook sees that user in the admission request).
func asUser(name string) client.Client {
	c := rest.CopyConfig(cfg)
	c.Impersonate = rest.ImpersonationConfig{UserName: name}

	cl, err := client.New(c, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	return cl
}

// grant gives user the ordinary write rights, and (if glass) the break-glass
// verb, on machines and patches.
func grant(user string, glass bool) {
	verbs := []string{"get", "list", "update", "patch", "delete"}
	if glass {
		verbs = append(verbs, "break-glass")
	}

	role := &rbacv1.ClusterRole{
		Name: "bg-test-" + user,
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{ichorGroup},
			Resources: []string{"talosmachines", "talosconfigpatches"},
			Verbs:     verbs,
		}},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, role))).To(Succeed())

	binding := &rbacv1.ClusterRoleBinding{
		Name:     "bg-test-" + user,
		RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: role.Name},
		Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: "User", Name: user}},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, binding))).To(Succeed())
}

var _ = Describe("Break-glass webhook", func() {
	var machine *ichorv1alpha1.TalosMachine

	BeforeEach(func() {
		grant(plainUser, false)
		grant(glassUser, true)
		grant(testControllerUser, false)
		grant(testArgoUser, false)

		// Argo CD's settings, so the tracking marker is read as annotation.
		ns := &corev1.Namespace{Name: testArgoNamespace}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, ns))).To(Succeed())
		cm := &corev1.ConfigMap{
			Name: gitopstracking.ArgoConfigMapName, Namespace: testArgoNamespace,
			Data: map[string]string{gitopstracking.ResourceTrackingMethodKey: "annotation"},
		}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, cm))).To(Succeed())

		machine = newTestMachine()
		machine.Annotations = map[string]string{
			gitopstracking.ArgoTrackingAnnotation: "app:ichor.maesh.dev/TalosMachine:default/x",
			deleteguard.AllowDeleteAnnotation:     deleteguard.AllowDeleteValue,
		}
		Expect(k8sClient.Create(ctx, machine)).To(Succeed())
	})

	AfterEach(func() {
		var m ichorv1alpha1.TalosMachine
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(machine), &m); err != nil {
			return
		}
		m.Finalizers = nil
		_ = k8sClient.Update(ctx, &m)
		_ = k8sClient.Delete(ctx, &m)
	})

	update := func(cl client.Client) error {
		var m ichorv1alpha1.TalosMachine
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(machine), &m)).To(Succeed())
		m.Spec.Hostname = "changed-" + m.ResourceVersion

		return cl.Update(ctx, &m)
	}

	It("refuses a direct update of a tracked CR from a user without the verb", func() {
		err := update(asUser(plainUser))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("break-glass"))
	})

	It("admits the same update when the user holds the verb", func() {
		Expect(update(asUser(glassUser))).To(Succeed())
	})

	It("admits Ichor's own controller and Argo CD's application controller", func() {
		Expect(update(asUser(testControllerUser))).To(Succeed())
		Expect(update(asUser(testArgoUser))).To(Succeed())
	})

	It("admits a user without the verb when the CR is not tracked", func() {
		var m ichorv1alpha1.TalosMachine
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(machine), &m)).To(Succeed())
		delete(m.Annotations, gitopstracking.ArgoTrackingAnnotation)
		Expect(k8sClient.Update(ctx, &m)).To(Succeed())

		Expect(update(asUser(plainUser))).To(Succeed())
	})

	It("refuses deleting a tracked CR without the verb, admits it with the verb", func() {
		err := asUser(plainUser).Delete(ctx, machine)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("break-glass"))

		Expect(asUser(glassUser).Delete(ctx, machine)).To(Succeed())
	})

	It("guards TalosConfigPatch the same way", func() {
		patch := &ichorv1alpha1.TalosConfigPatch{
			GenerateName: "patch-", Namespace: testNamespace,
			Annotations: map[string]string{gitopstracking.ArgoTrackingAnnotation: "app:ichor.maesh.dev/TalosConfigPatch:default/x"},
			Spec: ichorv1alpha1.TalosConfigPatchSpec{
				ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Layer: testClusterName,
			},
		}
		Expect(k8sClient.Create(ctx, patch)).To(Succeed())

		err := asUser(plainUser).Delete(ctx, patch)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("break-glass"))
		Expect(asUser(glassUser).Delete(ctx, patch)).To(Succeed())
	})

	Context("deleting a TalosCluster", func() {
		var cluster *ichorv1alpha1.TalosCluster

		BeforeEach(func() {
			rule := rbacv1.PolicyRule{APIGroups: []string{ichorGroup}, Resources: []string{resClusters}, Verbs: []string{verbGet, AuthenticationVerb, "delete"}}
			bindRole("bg-cluster-plain", plainUser, rule)
			bindRole("bg-cluster-glass", glassUser, rule, rbacv1.PolicyRule{
				APIGroups: []string{ichorGroup}, Resources: []string{resClusters}, Verbs: []string{"break-glass"},
			})

			cluster = newTestCluster()
			cluster.Annotations = map[string]string{
				gitopstracking.ArgoTrackingAnnotation: "app:ichor.maesh.dev/TalosCluster:default/x",
				deleteguard.AllowDeleteAnnotation:     deleteguard.AllowDeleteValue,
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, cluster)
		})

		It("is refused for a tracked cluster without the verb, admitted with it", func() {
			err := asUser(plainUser).Delete(ctx, cluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("break-glass"))

			Expect(asUser(glassUser).Delete(ctx, cluster)).To(Succeed())
		})

		It("is not affected when the cluster is untracked", func() {
			var c ichorv1alpha1.TalosCluster
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &c)).To(Succeed())
			delete(c.Annotations, gitopstracking.ArgoTrackingAnnotation)
			Expect(k8sClient.Update(ctx, &c)).To(Succeed())

			Expect(asUser(plainUser).Delete(ctx, &c)).To(Succeed())
		})
	})

	Context("removing an ichor.maesh.dev finalizer from a deleting object", func() {
		BeforeEach(func() {
			// Untracked: the finalizer rule alone must refuse.
			var m ichorv1alpha1.TalosMachine
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(machine), &m)).To(Succeed())
			delete(m.Annotations, gitopstracking.ArgoTrackingAnnotation)
			m.Finalizers = []string{testFinalizer}
			Expect(k8sClient.Update(ctx, &m)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &m)).To(Succeed())
		})

		removeFinalizer := func(cl client.Client) error {
			var m ichorv1alpha1.TalosMachine
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(machine), &m)).To(Succeed())
			m.Finalizers = nil

			return cl.Update(ctx, &m)
		}

		It("is refused for a user without the verb", func() {
			err := removeFinalizer(asUser(plainUser))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("finalizer"))
		})

		It("is admitted with the verb", func() {
			Expect(removeFinalizer(asUser(glassUser))).To(Succeed())
		})

		It("is admitted for Ichor's controller", func() {
			Expect(removeFinalizer(asUser(testControllerUser))).To(Succeed())
		})
	})
})

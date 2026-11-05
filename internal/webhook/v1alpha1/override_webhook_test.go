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

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// The users the override suite acts as. testControllerUser is the exempt
// name the suite's webhook is set up with (see webhook_suite_test.go).
const (
	testControllerUser = "system:serviceaccount:ichor-system:ichor-controller"
	plainUser          = "plain-user"
	glassUser          = "glass-user"
	testFinalizer      = "ichor.maesh.dev/machine-removal"
	testClusterName    = "cluster"
)

// asUser returns a client that makes requests as name, in groups (impersonated, so the
// webhook sees that user in the admission request).
func asUser(name string, groups ...string) client.Client {
	c := rest.CopyConfig(cfg)
	c.Impersonate = rest.ImpersonationConfig{UserName: name, Groups: groups}

	cl, err := client.New(c, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	return cl
}

// forceDelete deletes obj with the allow-delete annotation, unless it is gone already.
func forceDelete(obj client.Object) {
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return
	}

	obj.SetAnnotations(map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue})
	Expect(k8sClient.Update(ctx, obj)).To(Succeed())
	Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
}

// grant gives user the ordinary write rights, and (if glass) the override
// verb, on machines and patches.
func grant(user string, glass bool) {
	bindRole("bg-test-"+user, user, rbacv1.PolicyRule{
		APIGroups: []string{ichorGroup}, Resources: []string{"talosmachines", "talosconfigpatches"}, Verbs: writeVerbs(glass),
	})
}

// writeVerbs are the ordinary read and write verbs, plus override when glass.
func writeVerbs(glass bool) []string {
	verbs := []string{verbGet, "list", verbUpdate, verbPatch, verbDelete}
	if glass {
		verbs = append(verbs, "override")
	}

	return verbs
}

var _ = Describe("Override webhook", func() {
	var machine *ichorv1alpha1.TalosMachine

	BeforeEach(func() {
		grant(plainUser, false)
		grant(glassUser, true)
		grant(testControllerUser, false)

		machine = newTestMachine()
		machine.Annotations = map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue}
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

	It("admits an ordinary update", func() {
		var m ichorv1alpha1.TalosMachine
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(machine), &m)).To(Succeed())

		m.Spec.Hostname = "changed"
		Expect(asUser(plainUser).Update(ctx, &m)).To(Succeed())
	})

	It("admits deleting a CR without the verb", func() {
		Expect(asUser(plainUser).Delete(ctx, machine)).To(Succeed())
	})

	Context("removing an ichor.maesh.dev finalizer from a deleting object", func() {
		BeforeEach(func() {
			var m ichorv1alpha1.TalosMachine
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(machine), &m)).To(Succeed())
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

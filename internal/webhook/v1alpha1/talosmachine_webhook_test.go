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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// testNamespace is the envtest default namespace; shared so the literal
// doesn't repeat across test helpers (goconst).
const testNamespace = "default"

func newTestMachine() *ichorv1alpha1.TalosMachine {
	return &ichorv1alpha1.TalosMachine{
		GenerateName: "machine-", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "cluster"},
			Identity:   ichorv1alpha1.TalosMachineIdentity{UUID: "00000000-0000-0000-0000-000000000001"},
			Endpoint:   "10.0.0.2:50000",
			Role:       "worker",
		},
	}
}

// newTestControlPlane returns an approved control-plane TalosMachine in its
// own cluster namespace-scope (clusterName), distinct per uuidSuffix so
// several can coexist and be counted by the quorum check.
func newTestControlPlane(clusterName, uuidSuffix string) *ichorv1alpha1.TalosMachine {
	return &ichorv1alpha1.TalosMachine{
		GenerateName: "cp-", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: clusterName},
			Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: fmt.Sprintf("00000000-0000-0000-0000-0000000000%s", uuidSuffix)},
			Endpoint:      fmt.Sprintf("10.0.1.%s:50000", uuidSuffix),
			Role:          "controlplane",
			ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
		},
	}
}

// allowDelete sets the annotation that lets CheckDelete pass, so a test can
// isolate whether the quorum/last-control-plane rule alone refuses deletion.
func allowDelete(obj client.Object) {
	ann := obj.GetAnnotations()
	if ann == nil {
		ann = map[string]string{}
	}
	ann[deleteguard.AllowDeleteAnnotation] = deleteguard.AllowDeleteValue
	obj.SetAnnotations(ann)
}

var _ = Describe("TalosMachine Webhook", func() {
	var obj *ichorv1alpha1.TalosMachine

	BeforeEach(func() {
		obj = newTestMachine()
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
	})

	AfterEach(func() {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return
		}
		obj.Annotations = map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue}
		Expect(k8sClient.Update(ctx, obj)).To(Succeed())
		Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
	})

	It("admits create", func() {
		// obj was already created in BeforeEach; nothing further to assert.
	})

	It("admits update", func() {
		obj.Spec.Hostname = "worker-1"
		Expect(k8sClient.Update(ctx, obj)).To(Succeed())
	})

	It("refuses deletion without the allow-delete annotation", func() {
		err := k8sClient.Delete(ctx, obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(deleteguard.AllowDeleteAnnotation))
	})

	It("allows deletion with ichor.maesh.dev/allow-delete: \"true\"", func() {
		obj.Annotations = map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue}
		Expect(k8sClient.Update(ctx, obj)).To(Succeed())
		Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
	})
})

var _ = Describe("TalosMachine Webhook control-plane quorum", func() {
	// Each It gets its own cluster name so the control-plane count the
	// webhook lists is exactly what this test created, regardless of what
	// other specs left behind.

	It("refuses deleting the last control plane even with the allow-delete annotation", func() {
		clusterName := "quorum-last"
		cp := newTestControlPlane(clusterName, "01")
		Expect(k8sClient.Create(ctx, cp)).To(Succeed())
		allowDelete(cp)
		Expect(k8sClient.Update(ctx, cp)).To(Succeed())

		err := k8sClient.Delete(ctx, cp)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("last control plane"))
		Expect(err.Error()).To(ContainSubstring("1 -> 0"))

		// Clean up without the finalizer-less resource lingering: it is not
		// deletable through this path, so remove it directly for test hygiene
		// is not possible either (same guard) -- nothing to delete, it stays
		// refused, which is exactly the behaviour under test.
	})

	It("refuses a deletion that would break quorum, and allows one that doesn't", func() {
		clusterName := "quorum-3to2to1"
		cp1 := newTestControlPlane(clusterName, "11")
		cp2 := newTestControlPlane(clusterName, "12")
		cp3 := newTestControlPlane(clusterName, "13")
		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2, cp3} {
			Expect(k8sClient.Create(ctx, cp)).To(Succeed())
			allowDelete(cp)
			Expect(k8sClient.Update(ctx, cp)).To(Succeed())
		}

		// 3 control planes -> 2: quorum of 3 is 2, so this is allowed.
		Expect(k8sClient.Delete(ctx, cp1)).To(Succeed())

		// 2 control planes -> 1: quorum of 2 is 2, so this is refused, even
		// though cp2 itself carries the allow-delete annotation.
		err := k8sClient.Delete(ctx, cp2)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("below etcd quorum"))
		Expect(err.Error()).To(ContainSubstring("2 -> 1"))

		// cp3 remains the lone survivor; deleting it would also be refused
		// (it's now the last control plane), so leave it and let the
		// envtest namespace teardown reclaim it.
	})

	It("does not count an unapproved control plane toward quorum", func() {
		clusterName := "quorum-unapproved"
		cp := newTestControlPlane(clusterName, "21")
		cp.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStatePending
		Expect(k8sClient.Create(ctx, cp)).To(Succeed())
		allowDelete(cp)
		Expect(k8sClient.Update(ctx, cp)).To(Succeed())

		// Never joined etcd, so deleting it is just the ordinary annotation check.
		Expect(k8sClient.Delete(ctx, cp)).To(Succeed())
	})
})

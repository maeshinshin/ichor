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
	"bytes"
	"context"
	"errors"
	"io"
	"os"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/uiapi"
)

// ReclaimService (unit 15) against the real MachineReclaim webhook, the committed reclaimer ClusterRole and the
// committed credential policy (unit 14), in envtest.
var _ = Describe("ReclaimService with the MachineReclaim webhook", Ordered, func() {
	const (
		ns     = "ichor-system"
		holder = "reclaimer-alice"
		plain  = "plain-bob"
		addr   = "10.9.9.9"
	)

	var s *uiapi.Server

	as := func(user string) context.Context {
		return uiapi.ContextWithIdentity(ctx, uiapi.Identity{User: user})
	}

	decodeFile := func(path string, into func(map[string]any)) {
		data, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())

		dec := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
		for {
			var obj map[string]any
			if err := dec.Decode(&obj); err != nil {
				if errors.Is(err, io.EOF) {
					return
				}

				Expect(err).NotTo(HaveOccurred())
			}

			if obj != nil {
				into(obj)
			}
		}
	}

	BeforeAll(func() {
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{Name: ns}))).To(Succeed())

		// The committed ClusterRole, bound to one user only.
		raw, err := os.ReadFile("../../../config/rbac/machine_reclaimer_role.yaml")
		Expect(err).NotTo(HaveOccurred())

		var role rbacv1.ClusterRole
		Expect(yaml.Unmarshal(raw, &role)).To(Succeed())
		// The base name gets kustomize's ichor- prefix on install; the webhook refusal and the docs name the result.
		Expect("ichor-" + role.Name).To(Equal("ichor-machine-reclaimer"))
		Expect(k8sClient.Create(ctx, &role)).To(Succeed())
		Expect(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
			Name:     "reclaim-e2e",
			RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: kindClusterRole, Name: role.Name},
			Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: holder}},
		})).To(Succeed())

		// The committed credential policy.
		decodeFile("../../../config/admission/reclaim-credential-policy.yaml", func(obj map[string]any) {
			Expect(k8sClient.Create(ctx, &unstructured.Unstructured{Object: obj})).To(Succeed())
		})

		// ichor-ui's own identity, as the policy names it. system:masters stands in for the RBAC the chart grants it.
		ui := asUser("system:serviceaccount:ichor-system:ichor-ui", "system:masters")
		s = &uiapi.Server{
			Client: ui, ControllerNamespace: ns,
			Impersonate: uiapi.NewImpersonator(cfg, client.Options{Scheme: scheme.Scheme}),
		}

		// The policy is bound asynchronously: wait until it denies a Secret of the wrong type.
		Eventually(func() error {
			return ui.Create(ctx, &corev1.Secret{GenerateName: "probe-", Namespace: ns})
		}, "30s", "100ms").ShouldNot(Succeed())
	})

	It("admits a create by a holder, with the audit annotation and the credential Secret the policy accepts", func() {
		_, err := s.CreateReclaim(as(holder), connect.NewRequest(&pb.CreateReclaimRequest{
			Address: addr, Talosconfig: []byte("tc"), Context: "ctx-1",
		}))
		Expect(err).NotTo(HaveOccurred())

		var list ichorv1alpha1.MachineReclaimList
		Expect(k8sClient.List(ctx, &list, client.InNamespace(ns))).To(Succeed())
		Expect(list.Items).To(HaveLen(1))

		mr := list.Items[0]
		Expect(mr.Annotations).To(HaveKeyWithValue(ichorv1alpha1.ReclaimRequestedByAnnotation, holder))
		Expect(mr.Spec.ContextName).To(Equal("ctx-1"))

		var sec corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: mr.Spec.CredentialSecretRef}, &sec)).To(Succeed())
		Expect(sec.Type).To(Equal(ichorv1alpha1.ReclaimCredentialSecretType))
	})

	It("refuses a holder who may not update the named cluster, and writes nothing", func() {
		cluster := &pb.ObjectRef{Namespace: ns, Name: "some-cluster"}

		res, err := s.CanReclaim(as(holder), connect.NewRequest(&pb.CanReclaimRequest{Cluster: cluster}))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Msg.GetAllowed()).To(BeTrue())
		Expect(res.Msg.GetClusterAllowed()).To(BeFalse())

		// The namespace is shared with the other specs: compare against what is there before.
		var before ichorv1alpha1.MachineReclaimList
		Expect(k8sClient.List(ctx, &before, client.InNamespace(ns))).To(Succeed())

		var secretsBefore corev1.SecretList
		Expect(k8sClient.List(ctx, &secretsBefore, client.InNamespace(ns))).To(Succeed())

		_, err = s.CreateReclaim(as(holder), connect.NewRequest(&pb.CreateReclaimRequest{
			Address: addr, Talosconfig: []byte("tc"), Cluster: cluster,
		}))
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied))
		Expect(err).To(MatchError(ContainSubstring("not add addresses to cluster")))

		var list ichorv1alpha1.MachineReclaimList
		Expect(k8sClient.List(ctx, &list, client.InNamespace(ns))).To(Succeed())
		Expect(list.Items).To(HaveLen(len(before.Items)))

		var secrets corev1.SecretList
		Expect(k8sClient.List(ctx, &secrets, client.InNamespace(ns))).To(Succeed())
		Expect(secrets.Items).To(HaveLen(len(secretsBefore.Items)))
	})

	It("denies a create by a user without the reclaim role", func() {
		_, err := s.CreateReclaim(as(plain), connect.NewRequest(&pb.CreateReclaimRequest{Address: addr, Talosconfig: []byte("tc")}))
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied))
	})

	It("denies a create that does not carry the audit annotation", func() {
		mr := &ichorv1alpha1.MachineReclaim{
			GenerateName: "bare-", Namespace: ns,
			Spec: ichorv1alpha1.MachineReclaimSpec{Address: addr, CredentialSecretRef: "x"},
		}
		Expect(asUser(holder).Create(ctx, mr)).To(MatchError(ContainSubstring(ichorv1alpha1.ReclaimRequestedByAnnotation)))
	})

	Describe("confirming", func() {
		var key types.NamespacedName

		BeforeEach(func() {
			mr := &ichorv1alpha1.MachineReclaim{
				GenerateName: "conf-", Namespace: ns,
				Annotations: map[string]string{ichorv1alpha1.ReclaimRequestedByAnnotation: holder},
				Spec:        ichorv1alpha1.MachineReclaimSpec{Address: addr, CredentialSecretRef: "x"},
			}
			Expect(asUser(holder).Create(ctx, mr)).To(Succeed())

			// Stand in for the controller's inspection.
			mr.Status.Phase = ichorv1alpha1.MachineReclaimAwaitingConfirmation
			mr.Status.Inspection = &ichorv1alpha1.MachineReclaimInspection{UUID: "uuid-1"}
			Expect(k8sClient.Status().Update(ctx, mr)).To(Succeed())

			key = client.ObjectKeyFromObject(mr)
		})

		confirm := func(user, uuid, cluster string) error {
			_, err := s.ConfirmReclaim(as(user), connect.NewRequest(&pb.ConfirmReclaimRequest{
				Reclaim: &pb.ObjectRef{Namespace: key.Namespace, Name: key.Name}, Uuid: uuid, ClusterName: cluster,
			}))

			return err
		}

		It("admits the right UUID and records who confirmed", func() {
			Expect(confirm(holder, "uuid-1", "old-cluster")).To(Succeed())

			var got ichorv1alpha1.MachineReclaim
			Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
			Expect(got.Spec.ConfirmUUID).To(Equal("uuid-1"))
			Expect(got.Spec.ConfirmClusterName).To(Equal("old-cluster"))
			Expect(got.Annotations).To(HaveKeyWithValue(ichorv1alpha1.ReclaimConfirmedByAnnotation, holder))
		})

		It("denies a wrong UUID", func() {
			err := confirm(holder, "uuid-2", "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("exactly as inspected"))
		})

		It("denies a user without the role", func() {
			Expect(connect.CodeOf(confirm(plain, "uuid-1", ""))).To(Equal(connect.CodePermissionDenied))
		})
	})
})

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
	"encoding/json"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"connectrpc.com/connect"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// storageFixture is a TalosCluster with one TalosStorage selecting nodes labelled key=value, and
// one approved machine carrying that label and one that does not.
type storageFixture struct {
	cluster          *ichorv1alpha1.TalosCluster
	storage          *ichorv1alpha1.TalosStorage
	governed, others *ichorv1alpha1.TalosMachine
}

func newStorageFixture(key, value string) storageFixture {
	cluster := newTestCluster(newTestSecret(newTestBundle()).Name)

	storage := &ichorv1alpha1.TalosStorage{
		GenerateName: "storage-", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosStorageSpec{
			ClusterRef:   corev1.LocalObjectReference{Name: cluster.Name},
			Nodes:        metav1.LabelSelector{MatchLabels: map[string]string{key: value}},
			DiskPreset:   "longhorn-v1",
			DiskSelector: `disk.transport == "nvme"`,
		},
	}
	Expect(k8sClient.Create(ctx, storage)).To(Succeed())

	governed := newTestMachine(cluster.Name, strongTestIdentity("uuid-"+value), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)
	governed.Spec.NodeLabels = map[string]string{key: value}
	Expect(k8sClient.Update(ctx, governed)).To(Succeed())

	others := newTestMachine(cluster.Name, strongTestIdentity("uuid-other-"+value), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)

	return storageFixture{cluster: cluster, storage: storage, governed: governed, others: others}
}

// signedInAs returns a Server and a context for a signed-in user who holds exactly rules. Calls go through
// Impersonate and the identity on the context, as AuthInterceptor and cl(ctx) make them in production.
func signedInAs(user string, rules []rbacv1.PolicyRule) (*Server, context.Context) {
	role := &rbacv1.ClusterRole{GenerateName: "storage-test-", Rules: rules}
	Expect(k8sClient.Create(ctx, role)).To(Succeed())
	Expect(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
		GenerateName: "storage-test-",
		RoleRef:      rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: cniClusterRole, Name: role.Name},
		Subjects:     []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: user}},
	})).To(Succeed())

	// The API server's RBAC authorizer reads roles from an informer, so a new binding is not seen at once. Wait
	// until the first rule is granted, or the impersonated call is refused for a role it already holds.
	Eventually(func(g Gomega) {
		sar := &authv1.SubjectAccessReview{Spec: authv1.SubjectAccessReviewSpec{User: user, ResourceAttributes: &authv1.ResourceAttributes{
			Group: rules[0].APIGroups[0], Resource: rules[0].Resources[0], Verb: rules[0].Verbs[0], Namespace: testNamespace,
		}}}
		g.Expect(k8sClient.Create(ctx, sar)).To(Succeed())
		g.Expect(sar.Status.Allowed).To(BeTrue())
	}).Should(Succeed())

	s := &Server{
		Client: k8sClient, ControllerNamespace: testNamespace,
		Impersonate: NewImpersonator(cfg, client.Options{Scheme: scheme.Scheme}),
	}

	return s, ContextWithIdentity(ctx, Identity{User: user, Source: SourceIchor})
}

var _ = Describe("StorageService.ListStorages", func() {
	It("returns the TalosStorage that governs each machine, with its preset", func() {
		f := newStorageFixture("storage-spec", "longhorn-a")

		s := &Server{Client: k8sClient, ControllerNamespace: testNamespace}
		resp, err := s.ListStorages(ctx, connect.NewRequest(&pb.ListStoragesRequest{
			Cluster: objectRef(f.cluster.Namespace, f.cluster.Name),
		}))
		Expect(err).NotTo(HaveOccurred())

		var got *pb.Storage
		for _, st := range resp.Msg.GetStorages() {
			if st.GetRef().GetName() == f.storage.Name {
				got = st
			}
		}
		Expect(got).NotTo(BeNil(), "the TalosStorage of the cluster is listed")
		Expect(got.GetDiskPreset()).To(Equal("longhorn-v1"))
		Expect(got.GetRequiredExtensions()).To(ConsistOf("siderolabs/iscsi-tools", "siderolabs/util-linux-tools"))
		Expect(got.GetPresetError()).To(BeEmpty())
		Expect(got.GetNodeSelector()).To(Equal("storage-spec=longhorn-a"))
		Expect(got.GetGovernedMachines()).To(HaveLen(1))
		Expect(got.GetGovernedMachines()[0].GetName()).To(Equal(f.governed.Name))
		Expect(got.GetGovernedMachines()).NotTo(ContainElement(WithTransform(
			func(r *pb.ObjectRef) string { return r.GetName() }, Equal(f.others.Name))))
	})

	It("reads as the signed-in user, so a user without the talosstorages grant is refused", func() {
		f := newStorageFixture("storage-spec", "longhorn-denied")

		// The user can read the cluster and its machines, but not the TalosStorages: the refusal names them.
		s, signedIn := signedInAs("ichor:storage-no-list", []rbacv1.PolicyRule{
			{APIGroups: []string{ichorv1alpha1.GroupVersion.Group}, Resources: []string{resourceTalosClusters}, Verbs: []string{cniVerbGet}},
			{APIGroups: []string{ichorv1alpha1.GroupVersion.Group}, Resources: []string{resourceTalosMachines}, Verbs: []string{cniVerbGet, cniVerbList}},
		})
		// The API server's authorizer takes a moment to see the new role, so until then the first read
		// (the cluster) is refused instead. Wait for the refusal that names the TalosStorages.
		var err error
		Eventually(func() error {
			_, err = s.ListStorages(signedIn, connect.NewRequest(&pb.ListStoragesRequest{
				Cluster: objectRef(f.cluster.Namespace, f.cluster.Name),
			}))
			return err
		}).Should(MatchError(ContainSubstring("talosstorages")))
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied))
	})

	It("lists the TalosStorages for a signed-in user with the viewer's read rules", func() {
		f := newStorageFixture("storage-spec", "longhorn-viewer")

		s, signedIn := signedInAs("ichor:storage-viewer", []rbacv1.PolicyRule{
			{
				APIGroups: []string{ichorv1alpha1.GroupVersion.Group},
				Resources: []string{resourceTalosClusters, resourceTalosMachines, "talosstorages"},
				Verbs:     []string{cniVerbGet, cniVerbList, "watch"},
			},
		})
		var resp *connect.Response[pb.ListStoragesResponse]
		Eventually(func() error {
			var err error
			resp, err = s.ListStorages(signedIn, connect.NewRequest(&pb.ListStoragesRequest{
				Cluster: objectRef(f.cluster.Namespace, f.cluster.Name),
			}))
			return err
		}).Should(Succeed())

		names := make([]string, 0, len(resp.Msg.GetStorages()))
		for _, st := range resp.Msg.GetStorages() {
			names = append(names, st.GetRef().GetName())
		}
		Expect(names).To(ContainElement(f.storage.Name))
	})
})

// rookPreset is the one preset the page creates; rookLabelKey is the node label the rook specs select by.
const (
	rookPreset   = "rook-v1"
	rookLabelKey = "storage"
)

// builtinRoleRules returns the rules of one built-in Ichor role from its copy in config/rbac. The chart test
// holds that copy equal to the chart's ClusterRole, so the user signed in here holds what a real one would.
func builtinRoleRules(name string) []rbacv1.PolicyRule {
	raw, err := os.ReadFile("../../config/rbac/ichor_" + name + "_role.yaml")
	Expect(err).NotTo(HaveOccurred())

	var cr rbacv1.ClusterRole
	Expect(yaml.Unmarshal(raw, &cr)).To(Succeed())

	return cr.Rules
}

// rookStorageRequest is the create the page makes for a rook-v1 TalosStorage: a name, a cluster, a node
// label selector and the preset. It has no disk selector, because the request has no field for one.
func rookStorageRequest(cluster *ichorv1alpha1.TalosCluster, name string) *connect.Request[pb.CreateStorageRequest] {
	return connect.NewRequest(&pb.CreateStorageRequest{
		Cluster: objectRef(cluster.Namespace, cluster.Name), Name: name, DiskPreset: rookPreset, NodeSelector: rookLabelKey + "=rook",
	})
}

var _ = Describe("StorageService.CreateStorage", func() {
	It("creates a rook-v1 TalosStorage as an ichor-admin user", func() {
		cluster := newTestCluster(newTestSecret(newTestBundle()).Name)
		s, signedIn := signedInAs("ichor:storage-admin", builtinRoleRules("admin"))

		resp, err := s.CreateStorage(signedIn, rookStorageRequest(cluster, "rook-admin"))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetStorage().GetDiskPreset()).To(Equal(rookPreset))
		Expect(resp.Msg.GetAudit().GetActor()).To(Equal("ichor:storage-admin"))

		var got ichorv1alpha1.TalosStorage
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: "rook-admin"}, &got)).To(Succeed())
		Expect(got.Spec.ClusterRef.Name).To(Equal(cluster.Name))
		Expect(got.Spec.DiskPreset).To(Equal(rookPreset))
		Expect(got.Spec.DiskSelector).To(BeEmpty(), "the page writes no disk selector")
		Expect(got.Spec.Nodes.MatchLabels).To(Equal(map[string]string{rookLabelKey: "rook"}))
	})

	It("refuses a rook-v1 TalosStorage create for an ichor-operator user, and writes nothing", func() {
		cluster := newTestCluster(newTestSecret(newTestBundle()).Name)
		s, signedIn := signedInAs("ichor:storage-operator", builtinRoleRules("operator"))

		_, err := s.CreateStorage(signedIn, rookStorageRequest(cluster, "rook-operator"))
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied))
		Expect(err).To(MatchError(ContainSubstring("talosstorages")))

		err = k8sClient.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: "rook-operator"}, &ichorv1alpha1.TalosStorage{})
		Expect(err).To(MatchError(apierrors.IsNotFound, "the refused create must not have written the TalosStorage"))
	})

	It("refuses a rook-v1 TalosStorage that carries a diskSelector", func() {
		cluster := newTestCluster(newTestSecret(newTestBundle()).Name)

		// The page cannot send a selector. This is the write it would have to make, made by the envtest admin so
		// that only the CEL rule, not RBAC, can refuse it.
		ts := &ichorv1alpha1.TalosStorage{
			GenerateName: "rook-selector-", Namespace: cluster.Namespace,
			Spec: ichorv1alpha1.TalosStorageSpec{
				ClusterRef:   corev1.LocalObjectReference{Name: cluster.Name},
				Nodes:        metav1.LabelSelector{MatchLabels: map[string]string{rookLabelKey: "rook"}},
				DiskPreset:   rookPreset,
				DiskSelector: `disk.transport == "nvme"`,
			},
		}
		err := k8sClient.Create(ctx, ts)
		Expect(err).To(MatchError(apierrors.IsInvalid, "the API server refuses it"))
		Expect(err).To(MatchError(ContainSubstring("rook-v1 writes no disk config")))
	})

	It("refuses a longhorn-v1 create with the reason, and writes nothing", func() {
		cluster := newTestCluster(newTestSecret(newTestBundle()).Name)
		s, signedIn := signedInAs("ichor:storage-admin-longhorn", builtinRoleRules("admin"))

		_, err := s.CreateStorage(signedIn, connect.NewRequest(&pb.CreateStorageRequest{
			Cluster: objectRef(cluster.Namespace, cluster.Name), Name: "longhorn-page", DiskPreset: "longhorn-v1", NodeSelector: "storage=longhorn",
		}))
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("needs a disk selector")))

		err = k8sClient.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: "longhorn-page"}, &ichorv1alpha1.TalosStorage{})
		Expect(err).To(MatchError(apierrors.IsNotFound, "the refused create writes nothing"))
	})

	It("refuses an empty node selector, which would select every node", func() {
		cluster := newTestCluster(newTestSecret(newTestBundle()).Name)
		s, signedIn := signedInAs("ichor:storage-admin-empty", builtinRoleRules("admin"))

		_, err := s.CreateStorage(signedIn, connect.NewRequest(&pb.CreateStorageRequest{
			Cluster: objectRef(cluster.Namespace, cluster.Name), Name: "rook-everywhere", DiskPreset: rookPreset,
		}))
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("at least one label")))
	})

	It("offers the CRD's disk presets, with only rook-v1 creatable and longhorn-v1 explained", func() {
		cluster := newTestCluster(newTestSecret(newTestBundle()).Name)
		s, signedIn := signedInAs("ichor:storage-admin-presets", builtinRoleRules("admin"))

		resp, err := s.ListStorages(signedIn, connect.NewRequest(&pb.ListStoragesRequest{
			Cluster: objectRef(cluster.Namespace, cluster.Name),
		}))
		Expect(err).NotTo(HaveOccurred())

		got := map[string]*pb.StoragePreset{}
		names := make([]string, 0, len(resp.Msg.GetPresets()))
		for _, p := range resp.Msg.GetPresets() {
			got[p.GetName()] = p
			names = append(names, p.GetName())
		}
		Expect(names).To(Equal(crdDiskPresetEnum()), "the presets are the CRD's diskPreset enum, in its order")
		Expect(got[rookPreset].GetCreatable()).To(BeTrue())
		Expect(got["longhorn-v1"].GetCreatable()).To(BeFalse())
		Expect(got["longhorn-v1"].GetUnavailableReason()).To(ContainSubstring("kubectl"))
	})
})

// crdDiskPresetEnum reads spec.diskPreset's enum from the CRD in config/crd/bases, the file envtest installs.
func crdDiskPresetEnum() []string {
	raw, err := os.ReadFile("../../config/crd/bases/ichor.maesh.dev_talosstorages.yaml")
	Expect(err).NotTo(HaveOccurred())

	var crd apiextv1.CustomResourceDefinition
	Expect(yaml.Unmarshal(raw, &crd)).To(Succeed())

	spec := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
	out := make([]string, 0, len(spec.Properties["diskPreset"].Enum))
	for _, e := range spec.Properties["diskPreset"].Enum {
		var v string
		Expect(json.Unmarshal(e.Raw, &v)).To(Succeed())
		out = append(out, v)
	}

	return out
}

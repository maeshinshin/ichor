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
	"testing"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	cniViewerUser   = "cni-viewer"
	cniOperatorUser = "cni-operator"
	cniVerbGet      = "get"
	cniVerbList     = "list"
	cniVerbCreate   = "create"
	cniClusterRole  = "ClusterRole"
	cniDebugOn      = "debug:\n  enabled: true\n"
)

func asCniUser(user string) context.Context {
	return context.WithValue(ctx, identityKey{}, Identity{User: user})
}

var _ = Describe("CniService", func() {
	var (
		s       *Server
		cluster *ichorv1alpha1.TalosCluster
		ref     *pb.ObjectRef
	)

	BeforeEach(func() {
		read := rbacv1.PolicyRule{
			APIGroups: []string{ichorv1alpha1.GroupVersion.Group},
			Resources: []string{resourceTalosClusters, resourceTalosMachines, "talosconfigpatches", "talosstorages"},
			Verbs:     []string{cniVerbGet, cniVerbList},
		}
		write := rbacv1.PolicyRule{
			APIGroups: []string{ichorv1alpha1.GroupVersion.Group}, Resources: []string{resourceTalosClusters}, Verbs: []string{"update"},
		}

		createOps := rbacv1.PolicyRule{
			APIGroups: []string{ichorv1alpha1.GroupVersion.Group}, Resources: []string{caOpsResource}, Verbs: []string{cniVerbCreate, cniVerbList},
		}

		for user, rules := range map[string][]rbacv1.PolicyRule{
			cniViewerUser:   {read, {APIGroups: createOps.APIGroups, Resources: createOps.Resources, Verbs: []string{cniVerbList}}},
			cniOperatorUser: {read, write, createOps},
		} {
			name := "cni-test-" + user
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRole{Name: name, Rules: rules}))).To(Succeed())
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
				Name:     name,
				RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: cniClusterRole, Name: name},
				Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: user}},
			}))).To(Succeed())
		}

		s = &Server{Client: k8sClient, ControllerNamespace: testNamespace, Impersonate: NewImpersonator(cfg, client.Options{Scheme: scheme.Scheme})}
		cluster = newTestClusterWithBundle()
		_ = newTestMachine(cluster.Name, strongTestIdentity("uuid-cni-cp-"+cluster.Name), roleControlPlane, ichorv1alpha1.MachineApprovalStateApproved)
		ref = objectRef(cluster.Namespace, cluster.Name)
	})

	cniOperations := func(c *ichorv1alpha1.TalosCluster) []ichorv1alpha1.TalosOperation {
		var list ichorv1alpha1.TalosOperationList
		Expect(k8sClient.List(ctx, &list, client.InNamespace(c.Namespace))).To(Succeed())

		var out []ichorv1alpha1.TalosOperation

		for _, o := range list.Items {
			if o.Spec.ClusterRef.Name == c.Name {
				out = append(out, o)
			}
		}

		return out
	}

	stored := func() map[string]any {
		var got ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &got)).To(Succeed())

		if got.Spec.CNI.Cilium == nil || got.Spec.CNI.Cilium.Values == nil {
			return nil
		}

		var m map[string]any
		Expect(json.Unmarshal(got.Spec.CNI.Cilium.Values.Raw, &m)).To(Succeed())

		return m
	}

	set := func(user, yaml string) error {
		_, err := s.SetCiliumValues(asCniUser(user), connect.NewRequest(&pb.SetCiliumValuesRequest{Cluster: ref, UserYaml: yaml}))

		return err
	}

	It("lets a viewer read the layers but not write", func() {
		resp, err := s.GetCiliumValues(asCniUser(cniViewerUser), connect.NewRequest(&pb.GetCiliumValuesRequest{Cluster: ref}))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetUserYaml()).To(BeEmpty())
		Expect(resp.Msg.GetEffectiveYaml()).To(ContainSubstring("ipam"))
		Expect(resp.Msg.GetDefaultsYaml()).NotTo(BeEmpty())

		paths := make([]string, 0, len(resp.Msg.GetOwned()))
		for _, o := range resp.Msg.GetOwned() {
			paths = append(paths, o.GetPath())
			Expect(o.GetReason()).NotTo(BeEmpty())
		}

		Expect(paths).To(ContainElements("ipam.mode", "k8sServicePort", "tunnelProtocol"))
		Expect(resp.Msg.GetDefaultsYaml()).NotTo(ContainSubstring("k8sServicePort"))

		err = set(cniViewerUser, cniDebugOn)
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied))
		Expect(stored()).To(BeNil())
	})

	It("returns every validation error, not the first", func() {
		resp, err := s.ValidateCiliumValues(asCniUser(cniViewerUser), connect.NewRequest(&pb.ValidateCiliumValuesRequest{
			Cluster:  ref,
			UserYaml: "ipam:\n  mode: cluster-pool\nk8sServicePort: 1\ngatewayAPI:\n  enabled: true\ndebugg:\n  enabled: true\n",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetValid()).To(BeFalse())
		Expect(len(resp.Msg.GetErrors())).To(BeNumerically(">=", 4))

		all := ""
		for _, e := range resp.Msg.GetErrors() {
			all += e + "\n"
		}

		Expect(all).To(ContainSubstring("ipam.mode"))
		Expect(all).To(ContainSubstring("k8sServicePort"))
		Expect(all).To(ContainSubstring("Gateway API"))
		Expect(all).To(ContainSubstring("debugg"))
	})

	It("reads the Talos facts server-side: bpf.masquerade needs forwardKubeDNSToHost=false", func() {
		resp, err := s.ValidateCiliumValues(asCniUser(cniViewerUser), connect.NewRequest(&pb.ValidateCiliumValuesRequest{
			Cluster: ref, UserYaml: "bpf:\n  masquerade: true\n",
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetErrors()).To(ContainElement(ContainSubstring("forwardKubeDNSToHost")))
	})

	It("refuses an owned key and writes nothing", func() {
		err := set(cniOperatorUser, "ipam:\n  mode: cluster-pool\n")
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))
		Expect(err.Error()).To(ContainSubstring("ipam.mode"))
		Expect(stored()).To(BeNil())
	})

	It("refuses a document that is not a mapping", func() {
		Expect(connect.CodeOf(set(cniOperatorUser, "- a\n- b\n"))).To(Equal(connect.CodeInvalidArgument))
	})

	It("lets an operator set the values, shows them in the diff, and clears them again", func() {
		diff, err := s.DiffCiliumValues(asCniUser(cniViewerUser), connect.NewRequest(&pb.DiffCiliumValuesRequest{
			Cluster: ref, UserYaml: cniDebugOn,
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(diff.Msg.GetDiff()).To(ContainSubstring("+  enabled: true"))

		resp, err := s.SetCiliumValues(asCniUser(cniOperatorUser), connect.NewRequest(&pb.SetCiliumValuesRequest{
			Cluster: ref, UserYaml: cniDebugOn,
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetAudit().GetActor()).To(Equal(cniOperatorUser))
		Expect(resp.Msg.GetEffectiveYaml()).To(ContainSubstring("debug"))
		Expect(stored()).To(HaveKeyWithValue("debug", map[string]any{"enabled": true}))

		got, err := s.GetCiliumValues(asCniUser(cniViewerUser), connect.NewRequest(&pb.GetCiliumValuesRequest{Cluster: ref}))
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Msg.GetUserYaml()).To(ContainSubstring("enabled: true"))

		same, err := s.DiffCiliumValues(asCniUser(cniViewerUser), connect.NewRequest(&pb.DiffCiliumValuesRequest{
			Cluster: ref, UserYaml: cniDebugOn,
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(same.Msg.GetDiff()).To(BeEmpty())

		Expect(set(cniOperatorUser, "")).To(Succeed())
		Expect(stored()).To(BeNil())
	})

	It("checks an upgrade is on offer before anything else", func() {
		_, err := s.StartCniUpgrade(asCniUser(cniOperatorUser), connect.NewRequest(&pb.StartCniUpgradeRequest{Cluster: ref}))
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition))

		cluster.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{AvailableChartVersion: "1.19.0"}
		Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

		// A viewer may not create the operation, and nothing is created.
		_, err = s.StartCniUpgrade(asCniUser(cniViewerUser), connect.NewRequest(&pb.StartCniUpgradeRequest{Cluster: ref}))
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied))
		Expect(cniOperations(cluster)).To(BeEmpty())

		resp, err := s.StartCniUpgrade(asCniUser(cniOperatorUser), connect.NewRequest(&pb.StartCniUpgradeRequest{Cluster: ref}))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetToVersion()).To(Equal("1.19.0"))
		Expect(resp.Msg.GetAudit().GetActor()).To(Equal(cniOperatorUser))

		ops := cniOperations(cluster)
		Expect(ops).To(HaveLen(1))
		Expect(resp.Msg.GetOperation().GetName()).To(Equal(ops[0].Name))
		Expect(ops[0].Spec.Kind).To(Equal(ichorv1alpha1.TalosOperationKindCNI))
		Expect(ops[0].Spec.CNI).To(Equal(&ichorv1alpha1.TalosOperationCNISpec{
			Action: ichorv1alpha1.TalosOperationCNIActionUpgrade, ChartVersion: "1.19.0",
		}))

		// One unfinished operation blocks another.
		_, err = s.StartCniUpgrade(asCniUser(cniOperatorUser), connect.NewRequest(&pb.StartCniUpgradeRequest{Cluster: ref}))
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition))
		Expect(cniOperations(cluster)).To(HaveLen(1))
	})
})

// A TalosCluster that Argo CD tracks is read-only here: nothing is written.
func TestSetCiliumValuesRefusedWhenArgoCDTracks(t *testing.T) {
	cluster := &ichorv1alpha1.TalosCluster{
		Name: "c", Namespace: trackedTestNS, Annotations: map[string]string{argoTrackingAnnot: argoTrackedBy},
		Spec: ichorv1alpha1.TalosClusterSpec{CNI: ichorv1alpha1.TalosClusterCNI{KubeProxy: "enabled"}},
	}
	k8s := fake.NewClientBuilder().WithScheme(trackedScheme(t)).WithObjects(cluster, argoCM()).Build()

	_, err := (&Server{Client: k8s}).SetCiliumValues(context.Background(),
		connect.NewRequest(&pb.SetCiliumValuesRequest{Cluster: objectRef(trackedTestNS, "c"), UserYaml: cniDebugOn}))
	wantRefused(t, err, "Argo CD")

	var got ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(cluster), &got); err != nil {
		t.Fatal(err)
	}

	if got.Spec.CNI.Cilium != nil {
		t.Fatalf("the values must not be written: %+v", got.Spec.CNI.Cilium)
	}

	resp, err := (&Server{Client: k8s}).GetCiliumValues(context.Background(),
		connect.NewRequest(&pb.GetCiliumValuesRequest{Cluster: objectRef(trackedTestNS, "c")}))
	if err != nil || resp.Msg.GetTrackedBy() == "" {
		t.Fatalf("a read must say who tracks it: %v, %v", resp, err)
	}
}

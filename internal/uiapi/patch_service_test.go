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
	"encoding/json"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// sysctlPatchWire builds a CreatePatchRequest.patch for a single SysctlConfig
// document -- the simplest of the six allowed kinds (patch.proto's own doc
// comment on PatchDocument), opaque JSON on the wire, matching exactly what
// TalosConfigPatchDocument.SysctlConfig would unmarshal.
func sysctlPatchWire(name, clusterName string) *pb.Patch {
	return &pb.Patch{
		Ref:      objectRef(testNamespace, name),
		Cluster:  objectRef(testNamespace, clusterName),
		Layer:    pb.ConfigLayer_CONFIG_LAYER_CLUSTER,
		Priority: 0,
		Documents: []*pb.PatchDocument{
			{Kind: "SysctlConfig", Document: `{"params":{"net.core.somaxconn":"1024"}}`},
		},
	}
}

var _ = Describe("PatchService", func() {
	Describe("CreatePatch, GetPatch, ListPatches, DeletePatch", func() {
		It("round-trips layer, cluster, documents and the generated name through the wire", func() {
			cluster := newTestCluster("does-not-exist")

			s := &Server{Client: k8sClient}
			created, err := s.CreatePatch(ctx, connect.NewRequest(&pb.CreatePatchRequest{
				Patch: sysctlPatchWire("patch-crud-1", cluster.Name),
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(created.Msg.GetPatch().GetRef().GetName()).To(Equal("patch-crud-1"))
			Expect(created.Msg.GetAudit().GetAction()).To(Equal("create"))
			Expect(created.Msg.GetAudit().GetActor()).NotTo(BeEmpty())

			got, err := s.GetPatch(ctx, connect.NewRequest(&pb.GetPatchRequest{
				Patch: objectRef(testNamespace, "patch-crud-1"),
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Msg.GetPatch().GetLayer()).To(Equal(pb.ConfigLayer_CONFIG_LAYER_CLUSTER))
			Expect(got.Msg.GetPatch().GetCluster().GetName()).To(Equal(cluster.Name))
			Expect(got.Msg.GetPatch().GetDocuments()).To(HaveLen(1))
			Expect(got.Msg.GetPatch().GetDocuments()[0].GetKind()).To(Equal("SysctlConfig"))
			Expect(got.Msg.GetPatch().GetDocuments()[0].GetDocument()).To(ContainSubstring("net.core.somaxconn"))

			list, err := s.ListPatches(ctx, connect.NewRequest(&pb.ListPatchesRequest{
				Cluster: objectRef(cluster.Namespace, cluster.Name),
			}))
			Expect(err).NotTo(HaveOccurred())

			names := make([]string, 0, len(list.Msg.GetPatches()))
			for _, p := range list.Msg.GetPatches() {
				names = append(names, p.GetRef().GetName())
			}
			Expect(names).To(ContainElement("patch-crud-1"))

			_, err = s.DeletePatch(ctx, connect.NewRequest(&pb.DeletePatchRequest{
				Patch: objectRef(testNamespace, "patch-crud-1"),
			}))
			Expect(err).NotTo(HaveOccurred())

			_, err = s.GetPatch(ctx, connect.NewRequest(&pb.GetPatchRequest{
				Patch: objectRef(testNamespace, "patch-crud-1"),
			}))
			Expect(err).To(HaveOccurred())
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
		})
	})

	Describe("UpdatePatch", func() {
		It("replaces an existing patch's spec wholesale", func() {
			cluster := newTestCluster("does-not-exist")

			s := &Server{Client: k8sClient}
			_, err := s.CreatePatch(ctx, connect.NewRequest(&pb.CreatePatchRequest{
				Patch: sysctlPatchWire("patch-update-1", cluster.Name),
			}))
			Expect(err).NotTo(HaveOccurred())

			updated := sysctlPatchWire("patch-update-1", cluster.Name)
			updated.Priority = 5
			updated.Documents[0].Document = `{"params":{"net.core.somaxconn":"2048"}}`

			resp, err := s.UpdatePatch(ctx, connect.NewRequest(&pb.UpdatePatchRequest{Patch: updated}))
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Msg.GetPatch().GetPriority()).To(Equal(int32(5)))
			Expect(resp.Msg.GetAudit().GetAction()).To(Equal("update"))

			got, err := s.GetPatch(ctx, connect.NewRequest(&pb.GetPatchRequest{
				Patch: objectRef(testNamespace, "patch-update-1"),
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Msg.GetPatch().GetDocuments()[0].GetDocument()).To(ContainSubstring("2048"))
		})
	})

	Describe("the CRD's own CEL rules, surfaced as CodeInvalidArgument", func() {
		It("refuses a cluster-layer patch that also sets a target", func() {
			cluster := newTestCluster("does-not-exist")

			wire := sysctlPatchWire("patch-invalid-1", cluster.Name)
			wire.Target = &pb.PatchTarget{Role: "worker"} // layer "cluster" requires target absent.

			s := &Server{Client: k8sClient}
			_, err := s.CreatePatch(ctx, connect.NewRequest(&pb.CreatePatchRequest{Patch: wire}))
			Expect(err).To(HaveOccurred())
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))
		})

		It("refuses a target whose kind does not match the layer, naming the rule", func() {
			cluster := newTestCluster("does-not-exist")

			wire := sysctlPatchWire("patch-invalid-mismatch", cluster.Name)
			wire.Layer = pb.ConfigLayer_CONFIG_LAYER_ROLE
			wire.Target = &pb.PatchTarget{MachineRef: "m1"}

			s := &Server{Client: k8sClient}
			_, err := s.CreatePatch(ctx, connect.NewRequest(&pb.CreatePatchRequest{Patch: wire}))
			Expect(err).To(HaveOccurred())
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))
			Expect(err.Error()).To(ContainSubstring("the target must match the layer"))
		})

		It("refuses a layer that isn't cluster, role, selector or machine", func() {
			cluster := newTestCluster("does-not-exist")

			wire := sysctlPatchWire("patch-invalid-2", cluster.Name)
			wire.Layer = pb.ConfigLayer_CONFIG_LAYER_SYSTEM // never user-writable.

			s := &Server{Client: k8sClient}
			_, err := s.CreatePatch(ctx, connect.NewRequest(&pb.CreatePatchRequest{Patch: wire}))
			Expect(err).To(HaveOccurred())
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))
		})
	})

	Describe("strict field validation on every write", func() {
		It("CreatePatch and UpdatePatch both request FieldValidationStrict", func() {
			cluster := newTestCluster("does-not-exist")

			rec := &recordingUpdateClient{Client: k8sClient}
			s := &Server{Client: rec}

			_, err := s.CreatePatch(ctx, connect.NewRequest(&pb.CreatePatchRequest{
				Patch: sysctlPatchWire("patch-strict-1", cluster.Name),
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.lastCreateOptions.FieldValidation).To(Equal("Strict"))

			updated := sysctlPatchWire("patch-strict-1", cluster.Name)
			updated.Priority = 1
			_, err = s.UpdatePatch(ctx, connect.NewRequest(&pb.UpdatePatchRequest{Patch: updated}))
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.lastUpdateOptions.FieldValidation).To(Equal("Strict"))
		})
	})

	Describe("PreviewImpact", func() {
		It("reports no impact for a field that is both safe and supported by the pinned contract", func() {
			cluster := newTestCluster("does-not-exist")
			cluster.Spec.VersionContract = "v1.14" // newer than SysctlConfig's 1.13 floor.
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

			s := &Server{Client: k8sClient}
			resp, err := s.PreviewImpact(ctx, connect.NewRequest(&pb.PreviewImpactRequest{
				Patch: sysctlPatchWire("unused", cluster.Name),
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Msg.GetRebootRequired()).To(BeFalse())
			Expect(resp.Msg.GetInstallTimeOnly()).To(BeEmpty())
			Expect(resp.Msg.GetUnsupported()).To(BeEmpty())
		})

		It("reports a field as unsupported when the cluster's pinned contract predates it", func() {
			cluster := newTestCluster("does-not-exist")
			cluster.Spec.VersionContract = "v1.9" // older than SysctlConfig's 1.13 floor.
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

			s := &Server{Client: k8sClient}
			resp, err := s.PreviewImpact(ctx, connect.NewRequest(&pb.PreviewImpactRequest{
				Patch: sysctlPatchWire("unused", cluster.Name),
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Msg.GetUnsupported()).NotTo(BeEmpty())
		})

		// install-time-only is reported separately from reboot-required, never folded into
		// it: a reboot does nothing for a field Talos only reads during install/imaging
		// (pkg/fieldmeta's own doc comment), so a caller that only checked reboot_required
		// would wrongly tell an operator a reboot is all this patch needs.
		It("reports install-time-only fields separately from reboot-required", func() {
			cluster := newTestCluster("does-not-exist")

			cfg := minimalConfigSkeleton(roleWorker)
			cfg.MachineConfig.MachineInstall = &typedconfig.InstallConfig{InstallDisk: "/dev/sda"}

			raw, err := json.Marshal(cfg)
			Expect(err).NotTo(HaveOccurred())

			wire := &pb.Patch{
				Ref:     objectRef(testNamespace, "unused"),
				Cluster: objectRef(testNamespace, cluster.Name),
				Layer:   pb.ConfigLayer_CONFIG_LAYER_CLUSTER,
				Documents: []*pb.PatchDocument{
					{Kind: kindConfig, Document: string(raw)},
				},
			}

			s := &Server{Client: k8sClient}
			resp, err := s.PreviewImpact(ctx, connect.NewRequest(&pb.PreviewImpactRequest{Patch: wire}))
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Msg.GetInstallTimeOnly()).To(ContainElement("machine.install.disk"))
		})
	})
})

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
	"errors"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/siderolabs/talos/pkg/machinery/resources/block"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	"google.golang.org/protobuf/encoding/protojson"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	fakeSecret = "s3cr3t-value"
	argQuiet   = "quiet"
)

var _ = Describe("redactKernelArgs", func() {
	It("masks the value of any argument that could carry a credential and keeps the rest", func() {
		got := redactKernelArgs([]string{
			"console=ttyS0", argQuiet, "talos.platform=metal",
			"talos.config=https://host/cfg?token=" + fakeSecret,
			"siderolink.api=https://h?jointoken=" + fakeSecret,
			"talos.config.auth.token=" + fakeSecret,
		})
		Expect(got).To(Equal([]string{
			"console=ttyS0", argQuiet, "talos.platform=metal",
			"talos.config=***", "siderolink.api=***", "talos.config.auth.token=***",
		}))
	})
})

var _ = Describe("Talos details mapping", func() {
	It("never copies key material out of a link or a volume", func() {
		link := &network.LinkStatusSpec{Kind: "wireguard", MTU: 1420}
		link.Wireguard.PrivateKey = fakeSecret
		link.Wireguard.PublicKey = "pub"

		vol := &block.VolumeStatusSpec{ConfiguredEncryptionKeys: []string{fakeSecret}, EncryptionProvider: block.EncryptionProviderLUKS2}

		out, err := protojson.Marshal(&pb.TalosStorageSection{Volumes: []*pb.TalosVolume{volumeFromSpec("EPHEMERAL", vol)}})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out)).NotTo(ContainSubstring(fakeSecret))
		Expect(string(out)).To(ContainSubstring(`"encrypted":true`))

		out, err = protojson.Marshal(linkFromSpec("wg0", link))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out)).NotTo(ContainSubstring(fakeSecret))
	})

	It("classifies disks and finds the system disk", func() {
		spec := &block.DiskSpec{Transport: "nvme", Size: 10}
		d := diskFromSpec("nvme0n1", spec, "nvme0n1")
		Expect(d.GetType()).To(Equal("nvme"))
		Expect(d.GetSystemDisk()).To(BeTrue())
		Expect(diskFromSpec("sda", &block.DiskSpec{Transport: "sata", Rotational: true}, "nvme0n1").GetType()).To(Equal("hdd"))
		Expect(diskFromSpec("sr0", &block.DiskSpec{CDROM: true}, "").GetType()).To(Equal("cdrom"))
	})
})

var _ = Describe("NodeService.GetNodeTalosDetails", func() {
	newMachine := func(uuid, approval string) *ichorv1alpha1.TalosMachine {
		cluster := newTestClusterWithBundle()
		role := roleWorker
		if approval != ichorv1alpha1.MachineApprovalStateApproved {
			role = ""
		}

		return newTestMachine(cluster.Name, strongTestIdentity(uuid), role, approval)
	}

	It("returns every section and redacts credential-shaped kernel arguments", func() {
		m := newMachine("uuid-td-ok", ichorv1alpha1.MachineApprovalStateApproved)
		fake := &fakeNodeTalosClient{
			talosSystem: &pb.TalosSystemSection{
				Version: fixtureTalos, Stage: "running", Ready: true,
				KernelArgs: []string{argQuiet, "talos.config=https://x?token=" + fakeSecret},
			},
			talosNetwork:    &pb.TalosNetworkSection{DefaultGateways: []string{"10.0.0.1"}, TimeSynced: true},
			talosStorage:    &pb.TalosStorageSection{Disks: []*pb.TalosDisk{{Id: "sda", SystemDisk: true}}},
			talosServices:   &pb.TalosServicesSection{Services: []*pb.TalosService{{Id: "kubelet", State: "Running", Healthy: true}}},
			talosExtensions: &pb.TalosExtensionsSection{Extensions: []*pb.TalosExtension{{Name: "iscsi-tools"}}},
		}

		client, closeFn := newTestNodeServer(fake, nil)
		defer closeFn()

		resp, err := client.GetNodeTalosDetails(ctx, connect.NewRequest(&pb.GetNodeTalosDetailsRequest{Machine: objectRef(m.Namespace, m.Name)}))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetMaintenanceMode()).To(BeFalse())
		Expect(resp.Msg.GetSystem().GetStage()).To(Equal("running"))
		Expect(resp.Msg.GetNetwork().GetDefaultGateways()).To(Equal([]string{"10.0.0.1"}))
		Expect(resp.Msg.GetStorage().GetDisks()).To(HaveLen(1))
		Expect(resp.Msg.GetServices().GetServices()).To(HaveLen(1))
		Expect(resp.Msg.GetExtensions().GetExtensions()).To(HaveLen(1))
		Expect(fake.closed).To(BeTrue())

		raw, err := protojson.Marshal(resp.Msg)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring(fakeSecret), "no secret-shaped value may reach the response")
	})

	It("reports a failing section inline and still serves the others", func() {
		m := newMachine("uuid-td-fail", ichorv1alpha1.MachineApprovalStateApproved)
		fake := &fakeNodeTalosClient{
			talosSystem: &pb.TalosSystemSection{Stage: "running"},
			talosStorage: &pb.TalosStorageSection{
				Disks: []*pb.TalosDisk{{Id: "partial-should-be-dropped"}},
			},
			talosErr: map[string]error{"storage": errors.New("listing disks: boom")},
		}

		client, closeFn := newTestNodeServer(fake, nil)
		defer closeFn()

		resp, err := client.GetNodeTalosDetails(ctx, connect.NewRequest(&pb.GetNodeTalosDetailsRequest{Machine: objectRef(m.Namespace, m.Name)}))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetStorage().GetError()).To(ContainSubstring("boom"))
		Expect(resp.Msg.GetStorage().GetDisks()).To(BeEmpty())
		Expect(resp.Msg.GetSystem().GetStage()).To(Equal("running"))
	})

	It("flags maintenance mode for a machine that is not approved", func() {
		m := newMachine("uuid-td-pending", ichorv1alpha1.MachineApprovalStatePending)
		client, closeFn := newTestNodeServer(&fakeNodeTalosClient{talosSystem: &pb.TalosSystemSection{Stage: "maintenance"}}, nil)
		defer closeFn()

		resp, err := client.GetNodeTalosDetails(ctx, connect.NewRequest(&pb.GetNodeTalosDetailsRequest{Machine: objectRef(m.Namespace, m.Name)}))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetMaintenanceMode()).To(BeTrue())
	})

	It("reports CodeUnavailable when no NodeTalosClient is configured", func() {
		m := newMachine("uuid-td-none", ichorv1alpha1.MachineApprovalStateApproved)
		client, closeFn := newTestNodeServer(nil, nil)
		defer closeFn()

		_, err := client.GetNodeTalosDetails(ctx, connect.NewRequest(&pb.GetNodeTalosDetailsRequest{Machine: objectRef(m.Namespace, m.Name)}))
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnavailable))
	})
})

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
	"errors"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// untrustedNodeClient stands in for a node in maintenance mode dialed with
// the bundle identity: every read fails the way the real one does.
type untrustedNodeClient struct{ *fakeNodeTalosClient }

var errUntrusted = errors.New("x509: certificate signed by unknown authority")

func (untrustedNodeClient) Hardware(context.Context) (*pb.Hardware, error) {
	return nil, errUntrusted
}

// ADR 0076: an approved machine that never converged falls back to the
// maintenance API when the authenticated read fails.
var _ = Describe("NodeService reads of an approved machine still in maintenance mode", func() {
	var (
		m            *ichorv1alpha1.TalosMachine
		maintenance  *fakeNodeTalosClient
		secureDialed int
		insecure     int
	)

	dial := func(b *TalosIdentity) (NodeTalosClient, error) { //nolint:unparam // matches the factory signature
		if b != nil {
			secureDialed++

			return untrustedNodeClient{&fakeNodeTalosClient{}}, nil
		}

		insecure++

		return maintenance, nil
	}

	BeforeEach(func() {
		secureDialed, insecure = 0, 0
		maintenance = &fakeNodeTalosClient{
			hardware:    &pb.Hardware{Manufacturer: "QEMU"},
			disks:       []*pb.Disk{{Wwid: "wwid-m"}},
			talosSystem: &pb.TalosSystemSection{Stage: "maintenance"},
		}
		cluster := newTestClusterWithBundle()
		m = newTestMachine(cluster.Name, strongTestIdentity("uuid-maint-approved"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)
	})

	It("GetNode falls back to the maintenance API", func() {
		client, closeFn := newTestNodeServerFn(dial, nil)
		defer closeFn()

		resp, err := client.GetNode(ctx, connect.NewRequest(&pb.GetNodeRequest{Machine: objectRef(m.Namespace, m.Name)}))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetNode().GetDisks()).To(HaveLen(1))
		Expect(secureDialed).To(Equal(1))
		Expect(insecure).To(Equal(1))
	})

	It("GetNodeTalosDetails falls back and reports maintenance_mode", func() {
		client, closeFn := newTestNodeServerFn(dial, nil)
		defer closeFn()

		resp, err := client.GetNodeTalosDetails(ctx, connect.NewRequest(&pb.GetNodeTalosDetailsRequest{Machine: objectRef(m.Namespace, m.Name)}))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetMaintenanceMode()).To(BeTrue())
		Expect(resp.Msg.GetSystem().GetStage()).To(Equal("maintenance"))
	})

	It("does not fall back for a machine that has converged", func() {
		m.Status.AppliedConfigHash = "sha256:abc"
		Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())

		client, closeFn := newTestNodeServerFn(dial, nil)
		defer closeFn()

		_, err := client.GetNode(ctx, connect.NewRequest(&pb.GetNodeRequest{Machine: objectRef(m.Namespace, m.Name)}))
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnavailable))
		Expect(insecure).To(BeZero())
	})
})

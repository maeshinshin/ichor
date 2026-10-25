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
	"net/http"
	"net/http/httptest"
	"time"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

// fakeNodeTalosClient is the test double for NodeTalosClient (unit rule:
// no Talos call in a test ever reaches a real node, the same convention
// internal/controller's fakeTalosClient follows).
type fakeNodeTalosClient struct {
	hardware *pb.Hardware
	disks    []*pb.Disk
	ifaces   []*pb.NetworkInterface
	usage    *pb.ResourceUsage

	lines []LogLine
	// block, if set, makes StreamLogs wait for ctx to be cancelled after
	// sending lines, standing in for a real Dmesg follow stream that never
	// ends on its own -- the shape StreamNodeLogs' graceful-shutdown test
	// needs to have something to interrupt.
	block bool

	closed bool
}

func (f *fakeNodeTalosClient) Hardware(context.Context) (*pb.Hardware, error) { return f.hardware, nil }
func (f *fakeNodeTalosClient) Disks(context.Context) ([]*pb.Disk, error)      { return f.disks, nil }
func (f *fakeNodeTalosClient) NetworkInterfaces(context.Context) ([]*pb.NetworkInterface, error) {
	return f.ifaces, nil
}
func (f *fakeNodeTalosClient) ResourceUsage(context.Context) (*pb.ResourceUsage, error) {
	return f.usage, nil
}
func (f *fakeNodeTalosClient) Close() error { f.closed = true; return nil }

func (f *fakeNodeTalosClient) StreamLogs(ctx context.Context, since time.Time, onLine func(LogLine) error) error {
	for _, l := range f.lines {
		if !l.Time.After(since) {
			continue
		}

		if err := onLine(l); err != nil {
			return err
		}
	}

	if f.block {
		<-ctx.Done()

		return ctx.Err()
	}

	return nil
}

func newTestNodeServer(fake *fakeNodeTalosClient, draining <-chan struct{}) (ichorv1alpha1connect.NodeServiceClient, func()) {
	s := &Server{
		Client: k8sClient,
		NodeTalosClient: func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) (NodeTalosClient, error) {
			if fake == nil {
				return nil, errNoTalosClient
			}

			return fake, nil
		},
		Draining: draining,
	}

	mux := http.NewServeMux()
	path, handler := ichorv1alpha1connect.NewNodeServiceHandler(s)
	mux.Handle(path, handler)

	srv := httptest.NewServer(mux)
	client := ichorv1alpha1connect.NewNodeServiceClient(http.DefaultClient, srv.URL)

	return client, srv.Close
}

var _ = Describe("NodeService", func() {
	It("returns hardware, disks, network interfaces, usage and Kubernetes status", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-getnode"), roleWorker, true)
		m.Spec.Hostname = "node-getnode"
		Expect(k8sClient.Update(ctx, m)).To(Succeed())

		node := &corev1.Node{
			Name: m.Spec.Hostname,
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
				NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: "v1.31.0"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())

		fake := &fakeNodeTalosClient{
			hardware: &pb.Hardware{Manufacturer: "QEMU", CpuCores: 4},
			disks:    []*pb.Disk{{Wwid: "wwid-1", SizeBytes: 1024}},
			ifaces:   []*pb.NetworkInterface{{Name: "eth0", Up: true}},
			usage:    &pb.ResourceUsage{MemoryTotalBytes: 2048},
		}

		client, closeFn := newTestNodeServer(fake, nil)
		defer closeFn()

		resp, err := client.GetNode(ctx, connect.NewRequest(&pb.GetNodeRequest{Machine: objectRef(m.Namespace, m.Name)}))
		Expect(err).NotTo(HaveOccurred())

		got := resp.Msg.GetNode()
		Expect(got.GetHardware().GetManufacturer()).To(Equal("QEMU"))
		Expect(got.GetDisks()).To(HaveLen(1))
		Expect(got.GetNetworkInterfaces()).To(HaveLen(1))
		Expect(got.GetResourceUsage().GetMemoryTotalBytes()).To(Equal(int64(2048)))
		Expect(got.GetKubernetes().GetReady()).To(BeTrue())
		Expect(got.GetKubernetes().GetKubeletVersion()).To(Equal("v1.31.0"))
		Expect(fake.closed).To(BeTrue(), "GetNode must close the connection it opened")
	})

	It("reports CodeUnavailable when no NodeTalosClient is configured", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-notalos"), roleWorker, true)

		client, closeFn := newTestNodeServer(nil, nil)
		defer closeFn()

		_, err := client.GetNode(ctx, connect.NewRequest(&pb.GetNodeRequest{Machine: objectRef(m.Namespace, m.Name)}))
		Expect(err).To(HaveOccurred())
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnavailable))
	})

	It("streams logs from multiple machines merged onto one stream, and resumes from a token", func() {
		cluster := newTestClusterWithBundle()
		m1 := newTestMachine(cluster.Name, strongTestIdentity("uuid-log1"), roleWorker, true)
		m2 := newTestMachine(cluster.Name, strongTestIdentity("uuid-log2"), roleWorker, true)

		t0 := time.Now().Add(-time.Minute)
		fake := &fakeNodeTalosClient{lines: []LogLine{
			{Time: t0, Message: "first"},
			{Time: t0.Add(time.Second), Message: "second"},
		}}

		client, closeFn := newTestNodeServer(fake, nil)
		defer closeFn()

		stream, err := client.StreamNodeLogs(ctx, connect.NewRequest(&pb.StreamNodeLogsRequest{
			Machines: []*pb.ObjectRef{objectRef(m1.Namespace, m1.Name), objectRef(m2.Namespace, m2.Name)},
		}))
		Expect(err).NotTo(HaveOccurred())

		var (
			messages  []string
			lastToken string
		)

		for stream.Receive() {
			messages = append(messages, stream.Msg().GetMessage())
			lastToken = stream.Msg().GetResumeToken()
		}
		Expect(stream.Err()).NotTo(HaveOccurred())
		Expect(messages).To(HaveLen(4), "two lines from each of two machines")
		Expect(lastToken).NotTo(BeEmpty())

		// Resuming with the last token must not replay anything already seen.
		resumed, err := client.StreamNodeLogs(ctx, connect.NewRequest(&pb.StreamNodeLogsRequest{
			Machines:    []*pb.ObjectRef{objectRef(m1.Namespace, m1.Name), objectRef(m2.Namespace, m2.Name)},
			ResumeToken: lastToken,
		}))
		Expect(err).NotTo(HaveOccurred())

		var replayed []string
		for resumed.Receive() {
			replayed = append(replayed, resumed.Msg().GetMessage())
		}
		Expect(resumed.Err()).NotTo(HaveOccurred())
		Expect(replayed).To(BeEmpty(), "resuming from the latest token must not replay already-seen lines")
	})

	It("ends an in-flight stream and tells the client to reconnect once draining starts", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-drain"), roleWorker, true)

		fake := &fakeNodeTalosClient{block: true}
		draining := make(chan struct{})

		client, closeFn := newTestNodeServer(fake, draining)
		defer closeFn()

		streamCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		// The client's own call to establish the stream does not return
		// until the server actually writes something (a header flush on
		// first Send, or the handler returning) -- with nothing ever sent
		// here, it blocks for exactly as long as the handler does. Firing
		// draining from a separate goroutine, instead of after the call
		// below, is what actually exercises "a stream already in flight
		// when draining starts" rather than deadlocking the test itself.
		go func() {
			time.Sleep(100 * time.Millisecond)
			close(draining)
		}()

		stream, err := client.StreamNodeLogs(streamCtx, connect.NewRequest(&pb.StreamNodeLogsRequest{
			Machines: []*pb.ObjectRef{objectRef(m.Namespace, m.Name)},
		}))
		Expect(err).NotTo(HaveOccurred())

		for stream.Receive() { //nolint:revive // draining the stream to reach its terminal error
		}

		Expect(stream.Err()).To(HaveOccurred())
		Expect(connect.CodeOf(stream.Err())).To(Equal(connect.CodeUnavailable))
	})
})

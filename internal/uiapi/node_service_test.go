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
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"google.golang.org/protobuf/proto"
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
	usage    *pb.ResourceUsage

	// Talos details sections; talosErr fails one section by name.
	talosSystem     *pb.TalosSystemSection
	talosNetwork    *pb.TalosNetworkSection
	talosStorage    *pb.TalosStorageSection
	talosServices   *pb.TalosServicesSection
	talosExtensions *pb.TalosExtensionsSection
	talosErr        map[string]error

	// Runtime sections (GetNodeTalosRuntime).
	runtimeEtcd       *pb.TalosEtcdSection
	runtimeProcesses  *pb.TalosProcessesSection
	runtimeContainers *pb.TalosContainersSection
	runtimeNetwork    *pb.TalosNetworkStateSection
	runtimeModules    *pb.TalosKernelModulesSection

	lines []LogLine
	// block, if set, makes StreamLogs wait for ctx to be cancelled after
	// sending lines, standing in for a real Dmesg follow stream that never
	// ends on its own -- the shape StreamNodeLogs' graceful-shutdown test
	// needs to have something to interrupt.
	block bool

	// gotService records the last service StreamLogs was called with, so a
	// test can assert the request's service field reached NodeTalosClient
	// unchanged.
	gotService string
	// gotOpts records the tail/follow options the last StreamLogs got.
	gotOpts LogOptions

	closed bool
}

func (f *fakeNodeTalosClient) Hardware(context.Context) (*pb.Hardware, error) { return f.hardware, nil }
func (f *fakeNodeTalosClient) Disks(context.Context) ([]*pb.Disk, error)      { return f.disks, nil }
func (f *fakeNodeTalosClient) ResourceUsage(context.Context) (*pb.ResourceUsage, error) {
	return f.usage, nil
}
func (f *fakeNodeTalosClient) Close() error { f.closed = true; return nil }

func (f *fakeNodeTalosClient) TalosSystem(context.Context) (*pb.TalosSystemSection, error) {
	return f.talosSystem, f.talosErr["system"]
}

func (f *fakeNodeTalosClient) TalosNetwork(context.Context) (*pb.TalosNetworkSection, error) {
	return f.talosNetwork, f.talosErr["network"]
}

func (f *fakeNodeTalosClient) TalosStorage(context.Context) (*pb.TalosStorageSection, error) {
	return f.talosStorage, f.talosErr["storage"]
}

func (f *fakeNodeTalosClient) TalosServices(context.Context) (*pb.TalosServicesSection, error) {
	return f.talosServices, f.talosErr["services"]
}

func (f *fakeNodeTalosClient) TalosExtensions(context.Context) (*pb.TalosExtensionsSection, error) {
	return f.talosExtensions, f.talosErr["extensions"]
}

func (f *fakeNodeTalosClient) TalosEtcd(context.Context) (*pb.TalosEtcdSection, error) {
	return f.runtimeEtcd, f.talosErr["etcd"]
}

func (f *fakeNodeTalosClient) TalosProcesses(context.Context) (*pb.TalosProcessesSection, error) {
	return f.runtimeProcesses, f.talosErr["processes"]
}

func (f *fakeNodeTalosClient) TalosContainers(context.Context) (*pb.TalosContainersSection, error) {
	return f.runtimeContainers, f.talosErr["containers"]
}

func (f *fakeNodeTalosClient) TalosNetworkState(context.Context) (*pb.TalosNetworkStateSection, error) {
	return f.runtimeNetwork, f.talosErr["networkstate"]
}

func (f *fakeNodeTalosClient) TalosKernelModules(context.Context) (*pb.TalosKernelModulesSection, error) {
	return f.runtimeModules, f.talosErr["modules"]
}

func (f *fakeNodeTalosClient) StreamLogs(ctx context.Context, service string, since time.Time, opts LogOptions, onLine func(LogLine) error) error {
	f.gotService = service
	f.gotOpts = opts

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
	return newTestNodeServerFn(func(*TalosIdentity) (NodeTalosClient, error) {
		if fake == nil {
			return nil, errNoTalosClient
		}

		return fake, nil
	}, draining)
}

// newTestNodeServerFn is newTestNodeServer with a factory that sees the
// bundle, so a test can tell the authenticated dial (bundle set) from the
// maintenance one (nil).
func newTestNodeServerFn(dial func(*TalosIdentity) (NodeTalosClient, error), draining <-chan struct{}) (ichorv1alpha1connect.NodeServiceClient, func()) {
	s := &Server{
		Client:              k8sClient,
		ControllerNamespace: testNamespace,
		NodeTalosClient: func(_ context.Context, _ *ichorv1alpha1.TalosCluster, _ *ichorv1alpha1.TalosMachine, b *TalosIdentity) (NodeTalosClient, error) {
			return dial(b)
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
	It("returns hardware, disks, usage and Kubernetes status", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-getnode"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)
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
			usage:    &pb.ResourceUsage{MemoryTotalBytes: 2048},
		}

		client, closeFn := newTestNodeServer(fake, nil)
		defer closeFn()

		resp, err := client.GetNode(ctx, connect.NewRequest(&pb.GetNodeRequest{Machine: objectRef(m.Namespace, m.Name)}))
		Expect(err).NotTo(HaveOccurred())

		got := resp.Msg.GetNode()
		Expect(got.GetHardware().GetManufacturer()).To(Equal("QEMU"))
		Expect(got.GetDisks()).To(HaveLen(1))
		Expect(got.GetResourceUsage().GetMemoryTotalBytes()).To(Equal(int64(2048)))
		Expect(got.GetKubernetes().GetReady()).To(BeTrue())
		Expect(got.GetKubernetes().GetKubeletVersion()).To(Equal("v1.31.0"))
		Expect(fake.closed).To(BeTrue(), "GetNode must close the connection it opened")
	})

	It("reports CodeUnavailable when no NodeTalosClient is configured", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-notalos"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)

		client, closeFn := newTestNodeServer(nil, nil)
		defer closeFn()

		_, err := client.GetNode(ctx, connect.NewRequest(&pb.GetNodeRequest{Machine: objectRef(m.Namespace, m.Name)}))
		Expect(err).To(HaveOccurred())
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnavailable))
	})

	It("streams logs from multiple machines merged onto one stream, and resumes from a token", func() {
		cluster := newTestClusterWithBundle()
		m1 := newTestMachine(cluster.Name, strongTestIdentity("uuid-log1"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)
		m2 := newTestMachine(cluster.Name, strongTestIdentity("uuid-log2"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)

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

	It("streams a machine named twice once, rejects a long machine list, and masks the boot command line", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-logdup"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)

		fake := &fakeNodeTalosClient{lines: []LogLine{
			{Time: time.Now().Add(-time.Minute), Message: "[0.0] Command line: talos.platform=metal talos.config=https://x?token=hunter2"},
			{Time: time.Now().Add(-time.Second), Message: `[0.1] Unknown kernel command line parameters "talos.config=https://x?token=hunter2"`},
		}}
		client, closeFn := newTestNodeServer(fake, nil)
		defer closeFn()

		ref := objectRef(m.Namespace, m.Name)

		stream, err := client.StreamNodeLogs(ctx, connect.NewRequest(&pb.StreamNodeLogsRequest{Machines: []*pb.ObjectRef{ref, ref}}))
		Expect(err).NotTo(HaveOccurred())

		var messages []string
		for stream.Receive() {
			messages = append(messages, stream.Msg().GetMessage())
		}
		Expect(stream.Err()).NotTo(HaveOccurred())
		Expect(messages).To(HaveLen(2), "one stream, not two")

		for _, msg := range messages {
			Expect(msg).NotTo(ContainSubstring("hunter2"))
			Expect(msg).To(ContainSubstring("talos.config="))
		}

		many := make([]*pb.ObjectRef, maxLogMachines+1)
		for i := range many {
			many[i] = objectRef(m.Namespace, fmt.Sprintf("m%d", i))
		}

		stream, err = client.StreamNodeLogs(ctx, connect.NewRequest(&pb.StreamNodeLogsRequest{Machines: many}))
		Expect(err).NotTo(HaveOccurred())
		Expect(stream.Receive()).To(BeFalse())
		Expect(connect.CodeOf(stream.Err())).To(Equal(connect.CodeInvalidArgument))
	})

	It("refuses log streams past the concurrency limit and frees the slots on release", func() {
		s := &Server{}

		release, err := s.acquireLogSlots(maxConcurrentLogStreams)
		Expect(err).NotTo(HaveOccurred())

		_, err = s.acquireLogSlots(1)
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeResourceExhausted))

		release()

		_, err = s.acquireLogSlots(1)
		Expect(err).NotTo(HaveOccurred())
	})

	It("starts from the tail and follows by default, and honours tail_lines and follow=false", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-logtail"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)

		fake := &fakeNodeTalosClient{}
		client, closeFn := newTestNodeServer(fake, nil)
		defer closeFn()

		drain := func(req *pb.StreamNodeLogsRequest) {
			req.Machines = []*pb.ObjectRef{objectRef(m.Namespace, m.Name)}
			stream, err := client.StreamNodeLogs(ctx, connect.NewRequest(req))
			Expect(err).NotTo(HaveOccurred())

			for stream.Receive() { //nolint:revive // drain to the end
			}
			Expect(stream.Err()).NotTo(HaveOccurred())
		}

		drain(&pb.StreamNodeLogsRequest{})
		Expect(fake.gotOpts).To(Equal(LogOptions{Follow: true, TailLines: DefaultTailLines}))

		drain(&pb.StreamNodeLogsRequest{TailLines: 20, Follow: proto.Bool(false)})
		Expect(fake.gotOpts).To(Equal(LogOptions{Follow: false, TailLines: 20}))

		drain(&pb.StreamNodeLogsRequest{TailLines: 1_000_000})
		Expect(fake.gotOpts.TailLines).To(Equal(int32(MaxTailLines)))

		drain(&pb.StreamNodeLogsRequest{TailLines: -1})
		Expect(fake.gotOpts.TailLines).To(Equal(int32(-1)))
	})

	It("sendLast delivers only the last n lines", func() {
		lines := []LogLine{{Message: "a"}, {Message: "b"}, {Message: "c"}}

		var got []string

		Expect(sendLast(lines, 2, func(l LogLine) error { got = append(got, l.Message); return nil })).To(Succeed())
		Expect(got).To(Equal([]string{"b", "c"}))
	})

	It("passes the requested service through to NodeTalosClient, empty meaning the kernel log", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-logsvc"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)

		fake := &fakeNodeTalosClient{}
		client, closeFn := newTestNodeServer(fake, nil)
		defer closeFn()

		stream, err := client.StreamNodeLogs(ctx, connect.NewRequest(&pb.StreamNodeLogsRequest{
			Machines: []*pb.ObjectRef{objectRef(m.Namespace, m.Name)},
			Service:  "kubelet",
		}))
		Expect(err).NotTo(HaveOccurred())

		for stream.Receive() { //nolint:revive // no lines to drain; just reach the stream's end
		}
		Expect(stream.Err()).NotTo(HaveOccurred())
		Expect(fake.gotService).To(Equal("kubelet"))

		// Unset must reach NodeTalosClient as "" (the kernel log), not some
		// other default this handler might have been tempted to substitute.
		fake.gotService = "sentinel-untouched"

		stream2, err := client.StreamNodeLogs(ctx, connect.NewRequest(&pb.StreamNodeLogsRequest{
			Machines: []*pb.ObjectRef{objectRef(m.Namespace, m.Name)},
		}))
		Expect(err).NotTo(HaveOccurred())

		for stream2.Receive() { //nolint:revive // no lines to drain; just reach the stream's end
		}
		Expect(stream2.Err()).NotTo(HaveOccurred())
		Expect(fake.gotService).To(BeEmpty())
	})

	It("ends an in-flight stream and tells the client to reconnect once draining starts", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-drain"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)

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

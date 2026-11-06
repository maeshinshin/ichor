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
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

const (
	diagAdminUser  = "diag-admin"
	diagNobodyUser = "diag-nobody"
	diagUserHeader = "X-Test-User"
	diagSecret     = "TOP-SECRET-MARKER"
	diagIface      = "eth0"
)

// fakeDownloadClient stands in for the Talos API: no call reaches a node.
type fakeDownloadClient struct {
	entries  map[string][]byte
	problems []string
	snapshot []byte
	openErr  error
	// pcap, when set, makes the capture write the header and then these bytes.
	pcap     []byte
	pcapHold bool

	gotIface  string
	gotFilter []*machineapi.BPFInstruction
	closed    atomic.Bool
}

func (f *fakeDownloadClient) Close() error { f.closed.Store(true); return nil }

func (f *fakeDownloadClient) CollectSupport(_ context.Context, add func(string, []byte) error) ([]string, error) {
	for n, d := range f.entries {
		if err := add(n, d); err != nil {
			return nil, err
		}
	}

	return f.problems, nil
}

func (f *fakeDownloadClient) EtcdSnapshot(context.Context) (io.ReadCloser, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}

	return io.NopCloser(bytes.NewReader(f.snapshot)), nil
}

func (f *fakeDownloadClient) PacketCapture(ctx context.Context, iface string, filter []*machineapi.BPFInstruction) (io.ReadCloser, error) {
	f.gotIface, f.gotFilter = iface, filter
	pr, pw := io.Pipe()

	go func() {
		_, _ = pw.Write(f.pcap)

		if f.pcapHold {
			<-ctx.Done()
			// A real stream ends with a cancelled status when its context ends.
			_ = pw.CloseWithError(context.Canceled)

			return
		}

		_ = pw.Close()
	}()

	return pr, nil
}

// eventFailingClient refuses to create Events, to test the fail-closed audit.
type eventFailingClient struct{ client.Client }

func (c eventFailingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*corev1.Event); ok {
		return errors.New("events are unavailable")
	}

	return c.Client.Create(ctx, obj, opts...)
}

// testIdentity puts the user named in a header into the handler's context, as
// the OIDC interceptor would.
type testIdentity struct{}

func (testIdentity) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if u := req.Header().Get(diagUserHeader); u != "" {
			ctx = context.WithValue(ctx, identityKey{}, Identity{User: u})
		}

		return next(ctx, req)
	}
}

func (testIdentity) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (testIdentity) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if u := conn.RequestHeader().Get(diagUserHeader); u != "" {
			ctx = context.WithValue(ctx, identityKey{}, Identity{User: u})
		}

		return next(ctx, conn)
	}
}

func newDiagnosticsServer(fake *fakeDownloadClient, c client.Client, dials *atomic.Int32) (ichorv1alpha1connect.DiagnosticsServiceClient, *Server, func()) {
	s := &Server{
		Client: c, ControllerNamespace: testNamespace,
		Impersonate: NewImpersonator(cfg, client.Options{Scheme: scheme.Scheme}),
		DownloadTalosClient: func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *TalosIdentity, DownloadScope) (DownloadTalosClient, error) {
			if dials != nil {
				dials.Add(1)
			}

			return fake, nil
		},
	}

	mux := http.NewServeMux()
	path, handler := ichorv1alpha1connect.NewDiagnosticsServiceHandler(s, connect.WithInterceptors(testIdentity{}))
	mux.Handle(path, handler)

	srv := httptest.NewServer(mux)

	return ichorv1alpha1connect.NewDiagnosticsServiceClient(http.DefaultClient, srv.URL), s, srv.Close
}

// randomBytes is incompressible, so a size cap on a zip is reached.
func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)

	return b
}

func asDiagUser[T any](user string, msg *T) *connect.Request[T] {
	r := connect.NewRequest(msg)
	r.Header().Set(diagUserHeader, user)

	return r
}

// collect drains a download stream into its file name and bytes.
// Each streaming RPC has its own response type, so this takes any of them.
func collect[T any, P interface {
	*T
	GetFilename() string
	GetData() []byte
}](stream *connect.ServerStreamForClient[T]) (string, []byte, error) {
	var (
		name string
		buf  bytes.Buffer
	)

	for stream.Receive() {
		msg := P(stream.Msg())
		if f := msg.GetFilename(); f != "" {
			name = f
		}

		buf.Write(msg.GetData())
	}

	return name, buf.Bytes(), stream.Err()
}

func downloadEvents(m *ichorv1alpha1.TalosMachine) []corev1.Event {
	var list corev1.EventList
	Expect(k8sClient.List(ctx, &list, client.InNamespace(m.Namespace))).To(Succeed())

	var out []corev1.Event

	for _, e := range list.Items {
		if e.InvolvedObject.Name == m.Name && e.Reason == "DiagnosticsDownload" {
			out = append(out, e)
		}
	}

	return out
}

var _ = Describe("DiagnosticsService", func() {
	var (
		cluster *ichorv1alpha1.TalosCluster
		cp      *ichorv1alpha1.TalosMachine
		worker  *ichorv1alpha1.TalosMachine
	)

	BeforeEach(func() {
		read := rbacv1.PolicyRule{
			// Reading the machine is ordinary RBAC, separate from the download verb.
			APIGroups: []string{ichorv1alpha1.GroupVersion.Group},
			Resources: []string{"talosmachines", resourceTalosClusters},
			Verbs:     []string{"get", "list"},
		}
		download := rbacv1.PolicyRule{
			APIGroups: []string{ichorv1alpha1.GroupVersion.Group},
			Resources: []string{"talosmachines/support-bundle", "talosmachines/etcd-snapshot", "talosmachines/packet-capture"},
			Verbs:     []string{DownloadVerb},
		}

		mint := rbacv1.PolicyRule{
			APIGroups: []string{ichorv1alpha1.GroupVersion.Group}, Resources: []string{"taloscredentialrequests"}, Verbs: []string{cniVerbCreate},
		}

		// The admin user may read and download; the nobody user may only read.
		for user, rules := range map[string][]rbacv1.PolicyRule{diagAdminUser: {read, download, mint}, diagNobodyUser: {read}} {
			name := "diag-test-" + user
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRole{Name: name, Rules: rules}))).To(Succeed())
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
				Name:     name,
				RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: reflect.TypeFor[rbacv1.ClusterRole]().Name(), Name: name},
				Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: user}},
			}))).To(Succeed())
		}

		cluster = newTestClusterWithBundle()
		cp = newTestMachine(cluster.Name, strongTestIdentity("uuid-diag-cp"), roleControlPlane, ichorv1alpha1.MachineApprovalStateApproved)
		worker = newTestMachine(cluster.Name, strongTestIdentity("uuid-diag-w"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)
	})

	Describe("authorization", func() {
		It("refuses a user without the download verb, before dialing Talos or recording anything", func() {
			var dials atomic.Int32

			c, _, closeFn := newDiagnosticsServer(&fakeDownloadClient{snapshot: []byte("x")}, k8sClient, &dials)
			defer closeFn()

			for name, call := range map[string]func() error{
				"etcd snapshot": func() error {
					s, err := c.DownloadEtcdSnapshot(ctx, asDiagUser(diagNobodyUser, &pb.DownloadEtcdSnapshotRequest{Machine: objectRef(cp.Namespace, cp.Name)}))
					if err != nil {
						return err
					}

					_, _, err = collect(s)

					return err
				},
				"support bundle": func() error {
					s, err := c.DownloadSupportBundle(ctx, asDiagUser(diagNobodyUser, &pb.DownloadSupportBundleRequest{
						Target: &pb.DownloadSupportBundleRequest_Machine{Machine: objectRef(cp.Namespace, cp.Name)},
					}))
					if err != nil {
						return err
					}

					_, _, err = collect(s)

					return err
				},
				"packet capture": func() error {
					s, err := c.CapturePackets(ctx, asDiagUser(diagNobodyUser, &pb.CapturePacketsRequest{
						Machine: objectRef(cp.Namespace, cp.Name), Interface: diagIface,
					}))
					if err != nil {
						return err
					}

					_, _, err = collect(s)

					return err
				},
			} {
				err := call()
				Expect(err).To(HaveOccurred(), name)
				Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), name+": %v", err)
			}

			Expect(dials.Load()).To(BeZero())
			Expect(downloadEvents(cp)).To(BeEmpty())
		})

		It("reports each permission separately", func() {
			c, _, closeFn := newDiagnosticsServer(&fakeDownloadClient{}, k8sClient, nil)
			defer closeFn()

			req := &pb.GetMyPermissionsRequest{Machine: objectRef(cp.Namespace, cp.Name), Cluster: objectRef(cp.Namespace, "any-cluster")}

			resp, err := c.GetMyPermissions(ctx, asDiagUser(diagAdminUser, req))
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Msg.GetSupportBundle()).To(BeTrue())
			Expect(resp.Msg.GetEtcdSnapshot()).To(BeTrue())
			Expect(resp.Msg.GetPacketCapture()).To(BeTrue())

			resp, err = c.GetMyPermissions(ctx, asDiagUser(diagNobodyUser, req))
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Msg.GetSupportBundle()).To(BeFalse())
			Expect(resp.Msg.GetEtcdSnapshot()).To(BeFalse())
			Expect(resp.Msg.GetPacketCapture()).To(BeFalse())
			Expect(resp.Msg.GetClusterCredentials()).To(BeFalse())

			// Without a machine or a cluster there is nothing to ask about.
			resp, err = c.GetMyPermissions(ctx, asDiagUser(diagAdminUser, &pb.GetMyPermissionsRequest{}))
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Msg.GetSupportBundle() || resp.Msg.GetClusterCredentials()).To(BeFalse())
		})
	})

	Describe("etcd snapshot", func() {
		It("streams the snapshot, names the file and records an Event without content", func() {
			fake := &fakeDownloadClient{snapshot: bytes.Repeat([]byte("SNAPDATA"), 25000)}
			c, _, closeFn := newDiagnosticsServer(fake, k8sClient, nil)
			defer closeFn()

			s, err := c.DownloadEtcdSnapshot(ctx, asDiagUser(diagAdminUser, &pb.DownloadEtcdSnapshotRequest{Machine: objectRef(cp.Namespace, cp.Name)}))
			Expect(err).NotTo(HaveOccurred())

			name, data, err := collect(s)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(Equal(fake.snapshot))
			Expect(name).To(HavePrefix("etcd-" + cp.Name + "-"))
			Expect(name).To(HaveSuffix(".snapshot.db"))
			Expect(fake.closed.Load()).To(BeTrue(), "the Talos connection must be closed")

			events := downloadEvents(cp)
			Expect(events).To(HaveLen(1))
			Expect(events[0].Type).To(Equal(corev1.EventTypeWarning))
			Expect(events[0].Message).To(ContainSubstring("etcd-snapshot"))
			Expect(events[0].Message).To(ContainSubstring(diagAdminUser))
			Expect(events[0].Message).NotTo(ContainSubstring("SNAPDATA"))
		})

		It("refuses a worker node", func() {
			c, _, closeFn := newDiagnosticsServer(&fakeDownloadClient{}, k8sClient, nil)
			defer closeFn()

			s, err := c.DownloadEtcdSnapshot(ctx, asDiagUser(diagAdminUser, &pb.DownloadEtcdSnapshotRequest{Machine: objectRef(worker.Namespace, worker.Name)}))
			Expect(err).NotTo(HaveOccurred())

			_, _, err = collect(s)
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))
		})

		It("stops at the size cap and says the file is incomplete", func() {
			old := etcdSnapshotCap
			etcdSnapshotCap = 1000

			DeferCleanup(func() { etcdSnapshotCap = old })

			c, _, closeFn := newDiagnosticsServer(&fakeDownloadClient{snapshot: bytes.Repeat([]byte("x"), 5000)}, k8sClient, nil)
			defer closeFn()

			s, err := c.DownloadEtcdSnapshot(ctx, asDiagUser(diagAdminUser, &pb.DownloadEtcdSnapshotRequest{Machine: objectRef(cp.Namespace, cp.Name)}))
			Expect(err).NotTo(HaveOccurred())

			_, data, err := collect(s)
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeResourceExhausted))
			Expect(len(data)).To(BeNumerically("<=", 1000))
		})

		It("bounds the text of a Talos error", func() {
			fake := &fakeDownloadClient{openErr: errors.New(strings.Repeat("e", 2000))}
			c, _, closeFn := newDiagnosticsServer(fake, k8sClient, nil)
			defer closeFn()

			s, err := c.DownloadEtcdSnapshot(ctx, asDiagUser(diagAdminUser, &pb.DownloadEtcdSnapshotRequest{Machine: objectRef(cp.Namespace, cp.Name)}))
			Expect(err).NotTo(HaveOccurred())

			_, _, err = collect(s)
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnavailable))
			Expect(len(err.Error())).To(BeNumerically("<", 500))
		})

		It("is refused when its audit Event cannot be recorded", func() {
			var dials atomic.Int32

			c, _, closeFn := newDiagnosticsServer(&fakeDownloadClient{snapshot: []byte(diagSecret)}, eventFailingClient{k8sClient}, &dials)
			defer closeFn()

			s, err := c.DownloadEtcdSnapshot(ctx, asDiagUser(diagAdminUser, &pb.DownloadEtcdSnapshotRequest{Machine: objectRef(cp.Namespace, cp.Name)}))
			Expect(err).NotTo(HaveOccurred())

			_, data, err := collect(s)
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeInternal))
			Expect(data).To(BeEmpty())
			Expect(dials.Load()).To(BeZero())
		})

		It("refuses an unapproved machine", func() {
			pending := newTestMachine(cluster.Name, strongTestIdentity("uuid-diag-pending"), roleControlPlane, ichorv1alpha1.MachineApprovalStatePending)
			c, _, closeFn := newDiagnosticsServer(&fakeDownloadClient{}, k8sClient, nil)
			defer closeFn()

			s, err := c.DownloadEtcdSnapshot(ctx, asDiagUser(diagAdminUser, &pb.DownloadEtcdSnapshotRequest{Machine: objectRef(pending.Namespace, pending.Name)}))
			Expect(err).NotTo(HaveOccurred())

			_, _, err = collect(s)
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition))
		})

		It("limits how many downloads run at once", func() {
			_, s, closeFn := newDiagnosticsServer(&fakeDownloadClient{}, k8sClient, nil)
			defer closeFn()

			releases := make([]func(), 0, maxConcurrentDownloads)

			for range maxConcurrentDownloads {
				r, err := s.acquireDownloadSlot()
				Expect(err).NotTo(HaveOccurred())

				releases = append(releases, r)
			}

			_, err := s.acquireDownloadSlot()
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeResourceExhausted))

			releases[0]()

			r, err := s.acquireDownloadSlot()
			Expect(err).NotTo(HaveOccurred())
			r()
		})
	})

	Describe("packet capture", func() {
		capture := func(c ichorv1alpha1connect.DiagnosticsServiceClient, req *pb.CapturePacketsRequest) (string, []byte, error) {
			s, err := c.CapturePackets(ctx, asDiagUser(diagAdminUser, req))
			if err != nil {
				return "", nil, err
			}

			return collect(s)
		}

		It("rejects out-of-range or malformed requests before anything else happens", func() {
			var dials atomic.Int32

			c, _, closeFn := newDiagnosticsServer(&fakeDownloadClient{}, k8sClient, &dials)
			defer closeFn()

			ref := objectRef(cp.Namespace, cp.Name)

			for name, req := range map[string]*pb.CapturePacketsRequest{
				"duration above the maximum": {Machine: ref, Interface: diagIface, DurationSeconds: 301},
				"negative duration":          {Machine: ref, Interface: diagIface, DurationSeconds: -1},
				"size above the maximum":     {Machine: ref, Interface: diagIface, MaxBytes: MaxCaptureBytes + 1},
				"empty interface":            {Machine: ref},
				"interface with a slash":     {Machine: ref, Interface: "../eth0"},
				"garbage filter":             {Machine: ref, Interface: diagIface, BpfFilter: "tcp port 80"},
			} {
				_, _, err := capture(c, req)
				Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument), name)
			}

			Expect(dials.Load()).To(BeZero())
			Expect(downloadEvents(cp)).To(BeEmpty(), "a request that was never valid leaves no download Event")
		})

		It("passes the interface and validated filter, and stops at max_bytes", func() {
			fake := &fakeDownloadClient{pcap: bytes.Repeat([]byte("p"), 5000)}
			c, _, closeFn := newDiagnosticsServer(fake, k8sClient, nil)
			defer closeFn()

			name, data, err := capture(c, &pb.CapturePacketsRequest{
				Machine: objectRef(cp.Namespace, cp.Name), Interface: diagIface, MaxBytes: 1234,
				BpfFilter: "{ 0x6, 0, 0, 0x00040000 },",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(HaveLen(1234))
			Expect(name).To(HavePrefix("capture-" + cp.Name + "-eth0-"))
			Expect(name).To(HaveSuffix(".pcap"))
			Expect(fake.gotIface).To(Equal(diagIface))
			Expect(fake.gotFilter).To(HaveLen(1))

			events := downloadEvents(cp)
			Expect(events).NotTo(BeEmpty())
			Expect(events[len(events)-1].Message).To(ContainSubstring("packet-capture"))
		})

		It("ends at the duration as a normal, complete file", func() {
			fake := &fakeDownloadClient{pcap: []byte("header"), pcapHold: true}
			c, _, closeFn := newDiagnosticsServer(fake, k8sClient, nil)
			defer closeFn()

			start := time.Now()
			_, data, err := capture(c, &pb.CapturePacketsRequest{Machine: objectRef(cp.Namespace, cp.Name), Interface: diagIface, DurationSeconds: 1})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal("header"))
			Expect(time.Since(start)).To(BeNumerically("<", 10*time.Second))
		})
	})

	Describe("support bundle", func() {
		unzip := func(raw []byte) map[string]string {
			zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
			Expect(err).NotTo(HaveOccurred())

			out := map[string]string{}

			for _, f := range zr.File {
				rc, err := f.Open()
				Expect(err).NotTo(HaveOccurred())

				b, err := io.ReadAll(rc)
				Expect(err).NotTo(HaveOccurred())
				Expect(rc.Close()).To(Succeed())

				out[f.Name] = string(b)
			}

			return out
		}

		It("zips one node's files under its name, with problems listed", func() {
			fake := &fakeDownloadClient{
				entries:  map[string][]byte{"dmesg.log": []byte("kernel"), "logs/kubelet.log": []byte("kubelet")},
				problems: []string{"log of etcd: not running"},
			}
			c, _, closeFn := newDiagnosticsServer(fake, k8sClient, nil)
			defer closeFn()

			s, err := c.DownloadSupportBundle(ctx, asDiagUser(diagAdminUser, &pb.DownloadSupportBundleRequest{
				Target: &pb.DownloadSupportBundleRequest_Machine{Machine: objectRef(cp.Namespace, cp.Name)},
			}))
			Expect(err).NotTo(HaveOccurred())

			name, data, err := collect(s)
			Expect(err).NotTo(HaveOccurred())
			Expect(name).To(HavePrefix("support-" + cp.Name + "-"))
			Expect(name).To(HaveSuffix(".zip"))

			files := unzip(data)
			Expect(files).To(HaveKeyWithValue(cp.Name+"/dmesg.log", "kernel"))
			Expect(files).To(HaveKeyWithValue(cp.Name+"/logs/kubelet.log", "kubelet"))
			Expect(files[cp.Name+"/collection-errors.txt"]).To(ContainSubstring("not running"))
			Expect(files["README.txt"]).To(ContainSubstring("confidential"))
			Expect(downloadEvents(cp)).To(HaveLen(1))
		})

		It("covers every approved machine of a cluster", func() {
			fake := &fakeDownloadClient{entries: map[string][]byte{"version.json": []byte("{}")}}
			c, _, closeFn := newDiagnosticsServer(fake, k8sClient, nil)
			defer closeFn()

			s, err := c.DownloadSupportBundle(ctx, asDiagUser(diagAdminUser, &pb.DownloadSupportBundleRequest{
				Target: &pb.DownloadSupportBundleRequest_Cluster{Cluster: objectRef(cluster.Namespace, cluster.Name)},
			}))
			Expect(err).NotTo(HaveOccurred())

			name, data, err := collect(s)
			Expect(err).NotTo(HaveOccurred())
			Expect(name).To(HavePrefix("support-" + cluster.Name + "-"))

			files := unzip(data)
			Expect(files).To(HaveKey(cp.Name + "/version.json"))
			Expect(files).To(HaveKey(worker.Name + "/version.json"))
			Expect(downloadEvents(cp)).To(HaveLen(1))
			Expect(downloadEvents(worker)).To(HaveLen(1))
		})

		It("stops at the size cap", func() {
			old := supportBundleCap
			supportBundleCap = 200

			DeferCleanup(func() { supportBundleCap = old })

			fake := &fakeDownloadClient{entries: map[string][]byte{"big.log": randomBytes(10000)}}
			c, _, closeFn := newDiagnosticsServer(fake, k8sClient, nil)
			defer closeFn()

			s, err := c.DownloadSupportBundle(ctx, asDiagUser(diagAdminUser, &pb.DownloadSupportBundleRequest{
				Target: &pb.DownloadSupportBundleRequest_Machine{Machine: objectRef(cp.Namespace, cp.Name)},
			}))
			Expect(err).NotTo(HaveOccurred())

			_, _, err = collect(s)
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeResourceExhausted))
		})
	})
})

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
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"golang.org/x/net/bpf"
	"golang.org/x/sync/errgroup"
	authv1 "k8s.io/api/authorization/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// DownloadVerb is the Kubernetes RBAC verb that lets a user start one of the
// three diagnostic downloads (docs/adr/0084), on the subresource named by the
// DownloadScope of talosmachines. It follows the override verb's pattern
// (docs/adr/0065): checked with a SelfSubjectAccessReview as the caller, so
// Kubernetes decides and Ichor keeps no list of who may.
const DownloadVerb = "download"

const resourceTalosMachines = "talosmachines"

// Caps, enforced here whatever the caller asks for.
const (
	DefaultCaptureDuration = 30 * time.Second
	MaxCaptureDuration     = 5 * time.Minute
	MaxCaptureBytes        = 100 << 20
	MaxSupportBundleBytes  = 256 << 20
	MaxEtcdSnapshotBytes   = 8 << 30

	supportBundleTimeout   = 10 * time.Minute
	etcdSnapshotTimeout    = 30 * time.Minute
	maxBPFInstructions     = 4096
	downloadChunkSize      = 64 << 10
	maxConcurrentDownloads = 2

	pcapContentType = "application/vnd.tcpdump.pcap"
)

// The caps as the handlers read them, variables so a test can lower them.
var (
	etcdSnapshotCap  int64 = MaxEtcdSnapshotBytes
	supportBundleCap int64 = MaxSupportBundleBytes
)

var interfaceNameRE = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,32}$`)

// DownloadTalosClientFactory opens the Talos connection for one download,
// with the Talos role scope needs.
type DownloadTalosClientFactory func(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, id *TalosIdentity, scope DownloadScope,
) (DownloadTalosClient, error)

// diagnostics is the Server's state for the downloads: a counting semaphore.
type diagnostics struct{ slots chan struct{} }

func (s *Server) acquireDownloadSlot() (func(), error) {
	s.diagOnce.Do(func() { s.diag.slots = make(chan struct{}, maxConcurrentDownloads) })

	select {
	case s.diag.slots <- struct{}{}:
		return func() { <-s.diag.slots }, nil
	default:
		return nil, connect.NewError(connect.CodeResourceExhausted,
			fmt.Errorf("%d downloads are already running; wait for one to finish", maxConcurrentDownloads))
	}
}

// GetMyPermissions answers every download flag the UI needs in one call, each a
// SelfSubjectAccessReview as the caller (run concurrently): the three machine
// downloads when a machine is given, the cluster credentials download when a
// cluster is given. The answer only decides what the UI shows; each download
// asks again.
func (s *Server) GetMyPermissions(
	ctx context.Context, req *connect.Request[pb.GetMyPermissionsRequest],
) (*connect.Response[pb.GetMyPermissionsResponse], error) {
	var (
		out pb.GetMyPermissionsResponse
		g   errgroup.Group
	)

	if m := req.Msg.GetMachine(); m != nil {
		if m.GetNamespace() == "" || m.GetName() == "" {
			return nil, invalid("machine needs a namespace and a name")
		}

		for scope, dst := range map[DownloadScope]*bool{
			ScopeSupportBundle: &out.SupportBundle, ScopeEtcdSnapshot: &out.EtcdSnapshot, ScopePacketCapture: &out.PacketCapture,
		} {
			g.Go(func() (err error) {
				*dst, err = s.mayDownload(ctx, m.GetNamespace(), resourceTalosMachines, string(scope), m.GetName())

				return err
			})
		}
	}

	if c := req.Msg.GetCluster(); c != nil {
		if c.GetNamespace() == "" || c.GetName() == "" {
			return nil, invalid("cluster needs a namespace and a name")
		}

		g.Go(func() (err error) {
			out.ClusterCredentials, err = s.mayDownload(ctx, c.GetNamespace(), resourceTalosClusters, CredentialsScope, c.GetName())

			return err
		})
		g.Go(func() (err error) {
			out.TalosconfigReader, err = s.mayDownload(ctx, c.GetNamespace(), resourceTalosClusters, TalosconfigReaderScope, c.GetName())

			return err
		})
		g.Go(func() (err error) {
			out.ClusterUpdate, err = s.may(ctx, "update", c.GetNamespace(), resourceTalosClusters, "", c.GetName())

			return err
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return connect.NewResponse(&out), nil
}

// mayDownload asks Kubernetes whether the caller holds the download verb on
// resource/subresource of the named object. The caller is the OIDC user the
// backend impersonates; with OIDC off it is the backend's own ServiceAccount (or,
// in the CLI's in-process fallback, whoever the kubeconfig is), so a default
// install, whose ServiceAccount has no such grant, refuses every download.
func (s *Server) mayDownload(ctx context.Context, namespace, resource, subresource, name string) (bool, error) {
	return s.may(ctx, DownloadVerb, namespace, resource, subresource, name)
}

// may asks the same question for any verb.
func (s *Server) may(ctx context.Context, verb, namespace, resource, subresource, name string) (bool, error) {
	c, err := s.cl(ctx)
	if err != nil {
		return false, connect.NewError(connect.CodeUnavailable, fmt.Errorf("acting as the user: %w", err))
	}

	review := &authv1.SelfSubjectAccessReview{Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authv1.ResourceAttributes{
		Namespace: namespace, Verb: verb, Group: ichorv1alpha1.GroupVersion.Group,
		Resource: resource, Subresource: subresource, Name: name,
	}}}

	if err := c.Create(ctx, review); err != nil {
		return false, connect.NewError(connect.CodeUnavailable, fmt.Errorf("asking Kubernetes whether you may: %w", err))
	}

	return review.Status.Allowed, nil
}

func (s *Server) authorizeDownload(ctx context.Context, m *ichorv1alpha1.TalosMachine, scope DownloadScope) error {
	allowed, err := s.mayDownload(ctx, m.Namespace, resourceTalosMachines, string(scope), m.Name)
	if err != nil {
		return err
	}

	if !allowed {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("you may not download a %s from %q: Kubernetes RBAC does not grant you the %q verb on talosmachines/%s "+
				"(it is not granted to anyone by default; see ichorUI.diagnostics in the chart)", scope, m.Name, DownloadVerb, scope))
	}

	return nil
}

// emitDownloadEvent records who started which download on m. It carries no
// content. It uses the backend's own client (the users have no right to create
// Events), like override.
func (s *Server) emitDownloadEvent(ctx context.Context, m *ichorv1alpha1.TalosMachine, scope DownloadScope, detail string) error {
	msg := fmt.Sprintf("%s download started by %s at %s", scope, s.actorName(ctx), s.now().UTC().Format(time.RFC3339))

	if detail != "" {
		msg += " (" + detail + ")"
	}

	return s.emitWarningEvent(ctx, m, kindTalosMachine, "DiagnosticsDownload", msg)
}

// startDownload runs everything that must hold before a byte is read: the
// machine is approved, the caller may, the Event is recorded (a download that
// cannot be audited is refused), the secrets bundle loads and the Talos
// connection opens with the scope's role. close must always be called.
func (s *Server) startDownload(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, cluster *ichorv1alpha1.TalosCluster, scope DownloadScope, detail string,
) (DownloadTalosClient, func(), error) {
	if s.DownloadTalosClient == nil {
		return nil, nil, connect.NewError(connect.CodeUnavailable, errNoTalosClient)
	}

	if !m.Spec.IsApproved() {
		return nil, nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("machine %q is not approved, so it has no Talos credentials to download with", m.Name))
	}

	if err := s.authorizeDownload(ctx, m, scope); err != nil {
		return nil, nil, err
	}

	if err := s.emitDownloadEvent(ctx, m, scope, detail); err != nil {
		return nil, nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("the download was refused because its audit Event could not be recorded: %w", err))
	}

	// The certificate is asked for as the signed-in user: the webhook checks the same download verb again
	// and the controller signs it for the least role the call needs, for minutes (ADR 0108).
	id, err := s.issueAsCaller(ctx, cluster, ichorv1alpha1.TalosCredentialPurpose(scope), m.Name, 0, "")
	if err != nil {
		return nil, nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	tc, err := s.DownloadTalosClient(ctx, cluster, m, id, scope)
	if err != nil {
		return nil, nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("connecting to machine's Talos API: %w", err))
	}

	return tc, closeFunc(tc), nil
}

// closeFunc returns tc's Close when it is an io.Closer, else a no-op.
func closeFunc(tc any) func() {
	if closer, ok := tc.(io.Closer); ok {
		return func() { _ = closer.Close() }
	}

	return func() {}
}

// withDrain cancels ctx when the backend starts draining, so a long download
// ends instead of holding the shutdown.
func (s *Server) withDrain(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)

	if s.Draining != nil {
		go func() {
			select {
			case <-s.Draining:
				cancel()
			case <-ctx.Done():
			}
		}()
	}

	return ctx, cancel
}

var errSizeCap = errors.New("size cap reached")

// chunkWriter sends what is written as chunks, the first one carrying the file
// name, and stops with errSizeCap past max bytes. send wraps the RPC's own
// response message type (each streaming RPC has one, see ADR 0016).
type chunkWriter struct {
	send        func(filename, contentType string, data []byte) error
	filename    string
	contentType string
	max, n      int64
	started     bool
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	written := 0

	for len(p) > 0 {
		part := p
		if len(part) > downloadChunkSize {
			part = part[:downloadChunkSize]
		}

		if w.n+int64(len(part)) > w.max {
			part = part[:max(w.max-w.n, 0)]
			if len(part) == 0 {
				return written, errSizeCap
			}
		}

		var filename, contentType string
		if !w.started {
			filename, contentType, w.started = w.filename, w.contentType, true
		}

		if err := w.send(filename, contentType, part); err != nil {
			return written, err
		}

		w.n += int64(len(part))
		written += len(part)
		p = p[len(part):]

		if w.n >= w.max && len(p) > 0 {
			return written, errSizeCap
		}
	}

	return written, nil
}

// begin sends the file name even when no data follows (an empty capture).
func (w *chunkWriter) finish() error {
	if w.started {
		return nil
	}

	w.started = true

	return w.send(w.filename, w.contentType, nil)
}

func stamp(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

// DownloadEtcdSnapshot streams an etcd snapshot from one control plane node.
func (s *Server) DownloadEtcdSnapshot(
	ctx context.Context, req *connect.Request[pb.DownloadEtcdSnapshotRequest], stream *connect.ServerStream[pb.DownloadEtcdSnapshotResponse],
) error {
	m, cluster, err := s.getMachineAndCluster(ctx, req.Msg.GetMachine())
	if err != nil {
		return err
	}

	if m.Spec.Role != opRoleControlPlane {
		return invalid("machine %q is not a control plane node; an etcd snapshot comes from a control plane node", m.Name)
	}

	release, err := s.acquireDownloadSlot()
	if err != nil {
		return err
	}

	defer release()

	ctx, cancel := s.withDrain(ctx)
	defer cancel()

	ctx, cancelT := context.WithTimeout(ctx, etcdSnapshotTimeout)
	defer cancelT()

	tc, closeFn, err := s.startDownload(ctx, m, cluster, ScopeEtcdSnapshot, "")
	if err != nil {
		return err
	}

	defer closeFn()

	rc, err := tc.EtcdSnapshot(ctx)
	if err != nil {
		return talosCallError("etcd snapshot", err)
	}

	defer func() { _ = rc.Close() }()

	w := &chunkWriter{
		send: func(f, c string, d []byte) error {
			return stream.Send(&pb.DownloadEtcdSnapshotResponse{Filename: f, ContentType: c, Data: d})
		},
		max: etcdSnapshotCap, contentType: "application/octet-stream",
		filename: fmt.Sprintf("etcd-%s-%s.snapshot.db", m.Name, stamp(s.now())),
	}

	if _, err := io.Copy(w, rc); err != nil {
		return downloadCopyError("etcd snapshot", ctx, err, etcdSnapshotCap)
	}

	return w.finish()
}

// CapturePackets streams a bounded pcap from one node's interface.
func (s *Server) CapturePackets(
	ctx context.Context, req *connect.Request[pb.CapturePacketsRequest], stream *connect.ServerStream[pb.CapturePacketsResponse],
) error {
	m, cluster, err := s.getMachineAndCluster(ctx, req.Msg.GetMachine())
	if err != nil {
		return err
	}

	r := req.Msg

	if !interfaceNameRE.MatchString(r.GetInterface()) {
		return invalid("interface must be a link name such as eth0")
	}

	duration := time.Duration(r.GetDurationSeconds()) * time.Second

	switch {
	case duration < 0 || duration > MaxCaptureDuration:
		return invalid("duration_seconds must be between 1 and %d", int(MaxCaptureDuration.Seconds()))
	case duration == 0:
		duration = DefaultCaptureDuration
	}

	maxBytes := r.GetMaxBytes()

	switch {
	case maxBytes < 0 || maxBytes > MaxCaptureBytes:
		return invalid("max_bytes must be between 1 and %d", MaxCaptureBytes)
	case maxBytes == 0:
		maxBytes = MaxCaptureBytes
	}

	filter, err := ParseBPFFilter(r.GetBpfFilter())
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	release, err := s.acquireDownloadSlot()
	if err != nil {
		return err
	}

	defer release()

	ctx, cancel := s.withDrain(ctx)
	defer cancel()

	detail := fmt.Sprintf("interface %s, %s, filter %t", r.GetInterface(), duration, len(filter) > 0)

	tc, closeFn, err := s.startDownload(ctx, m, cluster, ScopePacketCapture, detail)
	if err != nil {
		return err
	}

	defer closeFn()

	// The capture ends when the time is up: a cancelled stream is the normal end.
	cctx, cancelCapture := context.WithTimeout(ctx, duration)
	defer cancelCapture()

	rc, err := tc.PacketCapture(cctx, r.GetInterface(), filter)
	if err != nil {
		return talosCallError("packet capture", err)
	}

	defer func() { _ = rc.Close() }()

	w := &chunkWriter{
		send: func(f, c string, d []byte) error {
			return stream.Send(&pb.CapturePacketsResponse{Filename: f, ContentType: c, Data: d})
		},
		max: maxBytes, contentType: pcapContentType,
		filename: fmt.Sprintf("capture-%s-%s-%s.pcap", m.Name, r.GetInterface(), stamp(s.now())),
	}

	_, err = io.Copy(w, rc)

	switch {
	case err == nil, errors.Is(err, errSizeCap):
		// Done: the node closed the stream, or the byte cap ended the capture.
	case cctx.Err() != nil && ctx.Err() == nil:
		// The time limit is the ordinary end of a capture.
	default:
		return downloadCopyError("packet capture", ctx, err, maxBytes)
	}

	return w.finish()
}

// DownloadSupportBundle streams a zip of one node's, or every approved node of a
// cluster's, diagnostics.
func (s *Server) DownloadSupportBundle(
	ctx context.Context, req *connect.Request[pb.DownloadSupportBundleRequest], stream *connect.ServerStream[pb.DownloadSupportBundleResponse],
) error {
	var (
		machines []*ichorv1alpha1.TalosMachine
		cluster  *ichorv1alpha1.TalosCluster
		name     string
	)

	switch t := req.Msg.GetTarget().(type) {
	case *pb.DownloadSupportBundleRequest_Machine:
		m, c, err := s.getMachineAndCluster(ctx, t.Machine)
		if err != nil {
			return err
		}

		machines, cluster, name = []*ichorv1alpha1.TalosMachine{m}, c, m.Name
	case *pb.DownloadSupportBundleRequest_Cluster:
		c, err := s.loadCluster(ctx, namespacedName(t.Cluster))
		if err != nil {
			return err
		}

		var list ichorv1alpha1.TalosMachineList
		if err := s.List(ctx, &list, client.InNamespace(c.Namespace)); err != nil {
			return mapListError("TalosMachine", err)
		}

		for i := range list.Items {
			if list.Items[i].Spec.ClusterRef.Name == c.Name && list.Items[i].Spec.IsApproved() {
				machines = append(machines, &list.Items[i])
			}
		}

		if len(machines) == 0 {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("cluster %q has no approved machines", c.Name))
		}

		cluster, name = c, c.Name
	default:
		return invalid("name a machine or a cluster")
	}

	release, err := s.acquireDownloadSlot()
	if err != nil {
		return err
	}

	defer release()

	ctx, cancel := s.withDrain(ctx)
	defer cancel()

	ctx, cancelT := context.WithTimeout(ctx, supportBundleTimeout)
	defer cancelT()

	// Authorise and audit every machine before anything is sent.
	type node struct {
		m     *ichorv1alpha1.TalosMachine
		tc    DownloadTalosClient
		close func()
	}

	var (
		nodes    []node
		failures = map[string]string{}
	)

	defer func() {
		for _, n := range nodes {
			n.close()
		}
	}()

	for _, m := range machines {
		tc, closeFn, err := s.startDownload(ctx, m, cluster, ScopeSupportBundle, "")
		if err != nil {
			// One machine you may not read, or cannot reach, stops a
			// single-node download; in a cluster bundle it is noted in the archive.
			if len(machines) == 1 || connect.CodeOf(err) == connect.CodePermissionDenied {
				return err
			}

			failures[m.Name] = truncateText(err.Error(), 300)

			continue
		}

		nodes = append(nodes, node{m, tc, closeFn})
	}

	if len(nodes) == 0 {
		return connect.NewError(connect.CodeUnavailable, errors.New("no machine could be reached"))
	}

	w := &chunkWriter{
		send: func(f, c string, d []byte) error {
			return stream.Send(&pb.DownloadSupportBundleResponse{Filename: f, ContentType: c, Data: d})
		},
		max: supportBundleCap, contentType: "application/zip",
		filename: fmt.Sprintf("support-%s-%s.zip", name, stamp(s.now())),
	}
	zw := zip.NewWriter(w)

	addFile := func(entry string, data []byte) error {
		h := &zip.FileHeader{Name: entry, Method: zip.Deflate, Modified: s.now()}

		f, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}

		_, err = f.Write(data)

		return err
	}

	fail := func(err error) error { return downloadCopyError("support bundle", ctx, err, supportBundleCap) }

	if err := addFile("README.txt", []byte(supportBundleReadme)); err != nil {
		return fail(err)
	}

	for machineName, msg := range failures {
		if err := addFile(machineName+"/collection-errors.txt", []byte(msg+"\n")); err != nil {
			return fail(err)
		}
	}

	for _, n := range nodes {
		prefix := n.m.Name + "/"

		problems, err := n.tc.CollectSupport(ctx, func(entry string, data []byte) error { return addFile(prefix+entry, data) })
		if err != nil {
			return fail(err)
		}

		if len(problems) > 0 {
			if err := addFile(prefix+"collection-errors.txt", []byte(strings.Join(problems, "\n")+"\n")); err != nil {
				return fail(err)
			}
		}
	}

	if err := zw.Close(); err != nil {
		return fail(err)
	}

	return w.finish()
}

const supportBundleReadme = `Ichor support bundle.

Per node: version.json, dmesg.log (kernel log, credential-shaped kernel arguments masked),
services.yaml, logs/<service>.log (last 5000 lines of each Talos service), processes.json (names only, no
command lines), mounts.json, memory.json, containers.json and resources/<namespace>/<type>.yaml (every
Talos resource that Talos does not mark sensitive, except the machine configuration, the secrets
namespace and the kernel command line). collection-errors.txt lists what could not be read.

Not included: the machine configuration, certificates and keys, the secrets bundle, Kubernetes objects.

Service logs and resource dumps are free text and can still contain hostnames, addresses and, if a
program printed one, a credential. Treat the file as confidential.
`

// talosCallError maps a failure to open a Talos stream.
func talosCallError(what string, err error) error {
	code := connect.CodeUnavailable
	if strings.Contains(err.Error(), "PermissionDenied") {
		code = connect.CodePermissionDenied
	}

	return connect.NewError(code, fmt.Errorf("%s: %s", what, truncateText(err.Error(), 300)))
}

// downloadCopyError maps a failure while bytes were flowing.
func downloadCopyError(what string, ctx context.Context, err error, maxBytes int64) error {
	switch {
	case errors.Is(err, errSizeCap):
		return connect.NewError(connect.CodeResourceExhausted,
			fmt.Errorf("%s stopped at the %d byte limit; the file is incomplete", what, maxBytes))
	case ctx.Err() != nil:
		return connect.NewError(connect.CodeDeadlineExceeded,
			fmt.Errorf("%s did not finish in time or the backend is shutting down; the file is incomplete", what))
	default:
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("%s: %s", what, truncateText(err.Error(), 300)))
	}
}

var bpfLineRE = regexp.MustCompile(`^\{\s*0x([0-9a-fA-F]{1,4}),\s*(\d{1,3}),\s*(\d{1,3}),\s*0x([0-9a-fA-F]{1,8})\s*\},?$`)

// ParseBPFFilter reads a classic BPF program in `tcpdump -dd` format, the form
// Talos takes, and validates it: every line must parse, the program is at most
// 4096 instructions (the kernel's limit) and must pass x/net/bpf's checks
// (jump targets inside the program, ending in a return). Empty means no filter.
func ParseBPFFilter(in string) ([]*machineapi.BPFInstruction, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return nil, nil
	}

	var (
		out []*machineapi.BPFInstruction
		raw []bpf.RawInstruction
	)

	for line := range strings.SplitSeq(in, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if len(out) >= maxBPFInstructions {
			return nil, fmt.Errorf("the filter has more than %d instructions", maxBPFInstructions)
		}

		g := bpfLineRE.FindStringSubmatch(line)
		if g == nil {
			return nil, fmt.Errorf("filter line %d is not in `tcpdump -dd` format ({ 0x28, 0, 0, 0x0000000c },)", len(out)+1)
		}

		op, _ := strconv.ParseUint(g[1], 16, 16)
		jt, _ := strconv.ParseUint(g[2], 10, 8)
		jf, _ := strconv.ParseUint(g[3], 10, 8)
		k, _ := strconv.ParseUint(g[4], 16, 32)

		out = append(out, &machineapi.BPFInstruction{Op: uint32(op), Jt: uint32(jt), Jf: uint32(jf), K: uint32(k)})
		raw = append(raw, bpf.RawInstruction{Op: uint16(op), Jt: uint8(jt), Jf: uint8(jf), K: uint32(k)})
	}

	insts, ok := bpf.Disassemble(raw)
	if !ok {
		return nil, errors.New("the filter contains an instruction that is not valid classic BPF")
	}

	if _, err := bpf.NewVM(insts); err != nil {
		return nil, fmt.Errorf("the filter is not a valid BPF program: %w", err)
	}

	return out, nil
}

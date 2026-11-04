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

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
	"github.com/maeshinshin/ichor/internal/uiapi"
)

// The commands below download a support bundle, an etcd snapshot or a packet
// capture (docs/adr/0084). All three files can hold secrets. They go through
// ichor-ui when it can serve them, and otherwise through the same handlers in
// this process on the caller's own kubeconfig, whose RBAC then decides.

var downloadOpts struct {
	Output    string
	Cluster   string
	Node      string
	Interface string
	Duration  time.Duration
	Filter    string
	MaxSize   int64
}

// localDiagnostics serves the download RPCs in this process. A Connect server
// stream cannot be handed to a client directly, so a loopback listener carries
// it; it starts on first use and is closed with the command.
type localDiagnostics struct {
	srv  *uiapi.Server
	once sync.Once
	c    ichorv1alpha1connect.DiagnosticsServiceClient
	http *http.Server
	err  error
}

func (l *localDiagnostics) client() (ichorv1alpha1connect.DiagnosticsServiceClient, error) {
	l.once.Do(func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			l.err = err

			return
		}

		mux := http.NewServeMux()
		mux.Handle(ichorv1alpha1connect.NewDiagnosticsServiceHandler(l.srv))
		l.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

		go func() { _ = l.http.Serve(ln) }()

		l.c = ichorv1alpha1connect.NewDiagnosticsServiceClient(http.DefaultClient, "http://"+ln.Addr().String())
	})

	return l.c, l.err
}

func (l *localDiagnostics) close() {
	if l.http != nil {
		_ = l.http.Close()
	}
}

func (l *localDiagnostics) GetMyPermissions(
	ctx context.Context, req *connect.Request[pb.GetMyPermissionsRequest],
) (*connect.Response[pb.GetMyPermissionsResponse], error) {
	return l.srv.GetMyPermissions(ctx, req)
}

func (l *localDiagnostics) DownloadSupportBundle(
	ctx context.Context, req *connect.Request[pb.DownloadSupportBundleRequest],
) (*connect.ServerStreamForClient[pb.DownloadSupportBundleResponse], error) {
	c, err := l.client()
	if err != nil {
		return nil, err
	}

	return c.DownloadSupportBundle(ctx, req)
}

func (l *localDiagnostics) DownloadEtcdSnapshot(
	ctx context.Context, req *connect.Request[pb.DownloadEtcdSnapshotRequest],
) (*connect.ServerStreamForClient[pb.DownloadEtcdSnapshotResponse], error) {
	c, err := l.client()
	if err != nil {
		return nil, err
	}

	return c.DownloadEtcdSnapshot(ctx, req)
}

func (l *localDiagnostics) CapturePackets(
	ctx context.Context, req *connect.Request[pb.CapturePacketsRequest],
) (*connect.ServerStreamForClient[pb.CapturePacketsResponse], error) {
	c, err := l.client()
	if err != nil {
		return nil, err
	}

	return c.CapturePackets(ctx, req)
}

func (l *localDiagnostics) DownloadReaderTalosconfig(
	ctx context.Context, req *connect.Request[pb.DownloadReaderTalosconfigRequest],
) (*connect.ServerStreamForClient[pb.DownloadReaderTalosconfigResponse], error) {
	c, err := l.client()
	if err != nil {
		return nil, err
	}

	return c.DownloadReaderTalosconfig(ctx, req)
}

// chunk is one of the three streaming responses (each RPC has its own message
// type, ADR 0016); they all carry the same fields.
type chunk interface {
	*pb.DownloadSupportBundleResponse | *pb.DownloadEtcdSnapshotResponse | *pb.CapturePacketsResponse
	GetFilename() string
	GetData() []byte
}

// chunkStream is what *connect.ServerStreamForClient[M] offers.
type chunkStream[M chunk] interface {
	Receive() bool
	Msg() M
	Err() error
	Close() error
}

// saveDownload writes the stream to output and returns the path written. An
// empty output uses the file name the server suggests, in the current
// directory; a directory gets that name inside it; "-" is standard output. The
// file is created with mode 0600 under a temporary name and renamed once the
// stream ended cleanly, so a cut-off download never looks like a complete one.
func saveDownload[M chunk](stream chunkStream[M], output string, stdout io.Writer) (string, int64, error) {
	if output == "-" {
		var n int64

		for stream.Receive() {
			w, err := stdout.Write(stream.Msg().GetData())
			n += int64(w)

			if err != nil {
				return "-", n, err
			}
		}

		return "-", n, stream.Err()
	}

	var (
		tmp  *os.File
		path string
		n    int64
	)

	cleanup := func() {
		if tmp != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}

	for stream.Receive() {
		msg := stream.Msg()

		if tmp == nil {
			var err error

			if path, err = targetPath(output, msg.GetFilename()); err != nil {
				return "", 0, err
			}

			if tmp, err = os.CreateTemp(filepath.Dir(path), ".ichor-download-*"); err != nil {
				return "", 0, err
			}
		}

		w, err := tmp.Write(msg.GetData())
		n += int64(w)

		if err != nil {
			cleanup()

			return "", n, err
		}
	}

	if err := stream.Err(); err != nil {
		cleanup()

		return "", n, err
	}

	if tmp == nil {
		return "", 0, errors.New("the server sent no data")
	}

	if err := tmp.Close(); err != nil {
		cleanup()

		return "", n, err
	}

	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		cleanup()

		return "", n, err
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		cleanup()

		return "", n, err
	}

	return path, n, nil
}

func targetPath(output, suggested string) (string, error) {
	name := filepath.Base(suggested)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "", errors.New("the server suggested no file name; pass -o FILE")
	}

	if output == "" {
		return name, nil
	}

	if st, err := os.Stat(output); err == nil && st.IsDir() {
		return filepath.Join(output, name), nil
	}

	return output, nil
}

type downloadCall[M chunk] func(ctx context.Context, c ichorv1alpha1connect.DiagnosticsServiceClient) (chunkStream[M], error)

// download connects and runs one streaming call.
func download[M chunk](cmd *cobra.Command, call downloadCall[M]) error {
	return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
		return runDownload(ctx, c, downloadOpts.Output, cmd.OutOrStdout(), cmd.ErrOrStderr(), call)
	})
}

// runDownload runs call and saves the stream. A refusal from ichor-ui says how
// to use the caller's own credentials instead.
func runDownload[M chunk](ctx context.Context, c *apiClients, output string, stdout, stderr io.Writer, call downloadCall[M]) error {
	if output == "-" { // the bytes go to stdout, and stderr stays free of anything but errors
		_, _, err := fetchDownload(ctx, c, output, stdout, call, nil)

		return err
	}

	p := newProgress(stderr)
	step := p.Start(downloadStep)

	path, n, err := fetchDownload(ctx, c, output, stdout, call, step)
	if err != nil {
		return step.Fail(err)
	}

	step.Done(fmt.Sprintf("%s to %s (mode 0600)", byteSize(n), path))
	p.Warn("the file is NOT encrypted and may contain secrets; keep it safe")

	return nil
}

// downloadStep is the name of the progress step of a download to a file.
const downloadStep = "Download"

// fetchDownload runs call and saves the stream, telling step (if any) how much has arrived.
func fetchDownload[M chunk](ctx context.Context, c *apiClients, output string, stdout io.Writer, call downloadCall[M], step *step) (string, int64, error) {
	stream, err := call(ctx, c.Diagnostics)
	if err != nil {
		return "", 0, explainDownloadError(err, c.Local)
	}

	defer func() { _ = stream.Close() }()

	if step != nil {
		stream = &countingStream[M]{chunkStream: stream, last: time.Now(), report: func(n int64) { step.Update(byteSize(n)) }}
	}

	path, n, err := saveDownload(stream, output, stdout)
	if err != nil {
		return "", n, explainDownloadError(err, c.Local)
	}

	return path, n, nil
}

// downloadReportEvery is how often a running download tells its step the size so far: a log of a
// large download stays short.
const downloadReportEvery = time.Second

// countingStream tells report how many bytes the stream has delivered, at most once per
// downloadReportEvery.
type countingStream[M chunk] struct {
	chunkStream[M]
	n      int64
	last   time.Time
	report func(int64)
}

func (s *countingStream[M]) Receive() bool {
	ok := s.chunkStream.Receive()
	if ok {
		s.n += int64(len(s.Msg().GetData()))

		if time.Since(s.last) >= downloadReportEvery {
			s.last = time.Now()
			s.report(s.n)
		}
	}

	return ok
}

func supportBundleRequest(namespace string, args []string, cluster string) (*pb.DownloadSupportBundleRequest, error) {
	switch {
	case len(args) == 1 && cluster == "":
		return &pb.DownloadSupportBundleRequest{Target: &pb.DownloadSupportBundleRequest_Machine{Machine: objectRef(namespace, args[0])}}, nil
	case len(args) == 0 && cluster != "":
		return &pb.DownloadSupportBundleRequest{Target: &pb.DownloadSupportBundleRequest_Cluster{Cluster: objectRef(namespace, cluster)}}, nil
	default:
		return nil, errors.New("name exactly one of NODE and --cluster")
	}
}

// pcapRequest builds the capture request. The duration is sent in whole
// seconds; the server rejects anything over five minutes, and a filter given
// as @FILE is read here.
func pcapRequest(namespace, node, iface, filter string, duration time.Duration, maxSize int64) (*pb.CapturePacketsRequest, error) {
	if file, ok := strings.CutPrefix(filter, "@"); ok {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("reading the filter: %w", err)
		}

		filter = string(raw)
	}

	if duration < 0 || duration > time.Hour {
		return nil, fmt.Errorf("--duration %s is out of range (at most %s)", duration, uiapi.MaxCaptureDuration)
	}

	return &pb.CapturePacketsRequest{
		Machine: objectRef(namespace, node), Interface: iface, BpfFilter: filter,
		DurationSeconds: int32(duration / time.Second), //nolint:gosec // bounded to an hour above
		MaxBytes:        maxSize,
	}, nil
}

func explainDownloadError(err error, local bool) error {
	if connect.CodeOf(err) == connect.CodePermissionDenied && !local {
		return fmt.Errorf("%w\nichor-ui acts as its own ServiceAccount here, which holds no download rights by default; "+
			"run again with --local to use your own kubeconfig's RBAC, or grant the download verb (chart: ichorUI.diagnostics)", err)
	}

	return err
}

var nodeSupportBundleCmd = &cobra.Command{
	Use:   "support-bundle [NODE]",
	Short: "Download a zip of a node's (or every node of a cluster's) logs, processes, mounts and Talos resources",
	Long: `support-bundle downloads a zip with, per node: the kernel log, the log of each Talos
service, the service list, process names (no command lines), mounts, memory, containers and every
Talos resource that Talos does not mark sensitive. It leaves out the machine configuration,
certificates and keys. Logs are free text and can still contain a credential a program printed,
so treat the file as confidential.

Name one NODE, or --cluster for every approved node of a cluster. Needs the "download" verb on
talosmachines/support-bundle. The file is written with mode 0600; -o - writes to standard output.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		req, err := supportBundleRequest(apiOpts.Namespace, args, downloadOpts.Cluster)
		if err != nil {
			return err
		}

		return download(cmd, func(ctx context.Context, c ichorv1alpha1connect.DiagnosticsServiceClient) (chunkStream[*pb.DownloadSupportBundleResponse], error) {
			return c.DownloadSupportBundle(ctx, connect.NewRequest(req))
		})
	},
}

var nodePcapCmd = &cobra.Command{
	Use:   "pcap NODE",
	Short: "Capture packets on a node's interface into a pcap file, for a bounded time and size",
	Long: `pcap captures packets on one interface of a node and downloads them as a pcap file. The
capture ends after --duration (default 30s, at most 5m) or --max-size (at most 100 MiB), whichever
comes first, and the file is complete either way. Packets on the wire can contain secrets.

--filter takes a classic BPF program in "tcpdump -dd" format (the form Talos takes), or @FILE to
read it from a file, for example:
  ichor node pcap worker-1 --interface eth0 --filter "$(tcpdump -dd -y EN10MB 'tcp port 80')" -o web.pcap

Needs the "download" verb on talosmachines/packet-capture. The file is written with mode 0600.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		req, err := pcapRequest(apiOpts.Namespace, args[0], downloadOpts.Interface, downloadOpts.Filter, downloadOpts.Duration, downloadOpts.MaxSize)
		if err != nil {
			return err
		}

		return download(cmd, func(ctx context.Context, c ichorv1alpha1connect.DiagnosticsServiceClient) (chunkStream[*pb.CapturePacketsResponse], error) {
			return c.CapturePackets(ctx, connect.NewRequest(req))
		})
	},
}

// etcdCmd groups the etcd commands: reading a snapshot (here) and the maintenance actions (node_actions.go).
var etcdCmd = &cobra.Command{
	Use:   "etcd",
	Short: "Download an etcd snapshot from, or run an etcd maintenance action on, a control plane node",
}

var etcdSnapshotCmd = &cobra.Command{
	Use:   "snapshot --node CONTROL_PLANE",
	Short: "Download an etcd snapshot from a control plane node",
	Long: `snapshot downloads an etcd snapshot taken from one control plane node. THE SNAPSHOT CONTAINS
EVERY KUBERNETES SECRET in the cluster; keep it as you would the secrets themselves. For scheduled,
encrypted backups use a TalosEtcdBackup instead. The file is plaintext (not encrypted). Needs the "download" verb on
talosmachines/etcd-snapshot. The file is written with mode 0600; -o - writes to standard output.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if downloadOpts.Node == "" {
			return errors.New("--node is required")
		}

		req := &pb.DownloadEtcdSnapshotRequest{Machine: objectRef(apiOpts.Namespace, downloadOpts.Node)}

		return download(cmd, func(ctx context.Context, c ichorv1alpha1connect.DiagnosticsServiceClient) (chunkStream[*pb.DownloadEtcdSnapshotResponse], error) {
			return c.DownloadEtcdSnapshot(ctx, connect.NewRequest(req))
		})
	},
}

func init() {
	nodeCmd.AddCommand(nodeSupportBundleCmd, nodePcapCmd)

	// -o means the output file for these commands, not the yaml/json format of the
	// others: a local flag of the same name replaces the inherited one.
	const outputHelp = "file to write (mode 0600); default the name the server suggests; - for standard output"

	for _, c := range []*cobra.Command{nodeSupportBundleCmd, nodePcapCmd, etcdSnapshotCmd} {
		c.Flags().StringVarP(&downloadOpts.Output, "output", "o", "", outputHelp)
	}

	nodeSupportBundleCmd.Flags().StringVar(&downloadOpts.Cluster, "cluster", "", "download for every approved node of this cluster instead of one NODE")

	f := nodePcapCmd.Flags()
	f.StringVar(&downloadOpts.Interface, "interface", "eth0", "interface to capture on")
	f.DurationVar(&downloadOpts.Duration, "duration", uiapi.DefaultCaptureDuration, "capture time (at most 5m)")
	f.StringVar(&downloadOpts.Filter, "filter", "", `BPF filter in "tcpdump -dd" format, or @FILE`)
	f.Int64Var(&downloadOpts.MaxSize, "max-size", 0, "stop after this many bytes (default and maximum 100 MiB)")

	etcdSnapshotCmd.Flags().StringVar(&downloadOpts.Node, "node", "", "control plane TalosMachine to take the snapshot from")

	rootCmd.AddCommand(etcdCmd)
	addAPIFlags(etcdCmd)
	etcdCmd.AddCommand(etcdSnapshotCmd)
}

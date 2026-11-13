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
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

// fakeDiagnostics serves canned chunks, or fails after some, or refuses.
type fakeDiagnostics struct {
	ichorv1alpha1connect.UnimplementedDiagnosticsServiceHandler

	// chunks are what the etcd snapshot download streams (the tests use only that one).
	chunks []*pb.DownloadEtcdSnapshotResponse
	// failAfter, when set, ends the stream with this error after the chunks.
	failAfter error
	// deny refuses the call.
	deny bool

	gotPcap *pb.CapturePacketsRequest
}

func (f *fakeDiagnostics) refusal() error {
	if f.deny {
		return connect.NewError(connect.CodePermissionDenied, errors.New("no"))
	}

	return nil
}

func (f *fakeDiagnostics) DownloadEtcdSnapshot(_ context.Context, _ *connect.Request[pb.DownloadEtcdSnapshotRequest], s *connect.ServerStream[pb.DownloadEtcdSnapshotResponse]) error {
	if err := f.refusal(); err != nil {
		return err
	}

	for _, c := range f.chunks {
		if err := s.Send(c); err != nil {
			return err
		}
	}

	return f.failAfter
}

func (f *fakeDiagnostics) DownloadSupportBundle(_ context.Context, _ *connect.Request[pb.DownloadSupportBundleRequest], _ *connect.ServerStream[pb.DownloadSupportBundleResponse]) error {
	return f.refusal()
}

func (f *fakeDiagnostics) CapturePackets(_ context.Context, r *connect.Request[pb.CapturePacketsRequest], _ *connect.ServerStream[pb.CapturePacketsResponse]) error {
	f.gotPcap = r.Msg

	return f.refusal()
}

func diagClients(t *testing.T, f *fakeDiagnostics, local bool) *apiClients {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(ichorv1alpha1connect.NewDiagnosticsServiceHandler(f))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &apiClients{Diagnostics: ichorv1alpha1connect.NewDiagnosticsServiceClient(http.DefaultClient, srv.URL), Local: local, Close: func() {}}
}

func call(ctx context.Context, c ichorv1alpha1connect.DiagnosticsServiceClient) (chunkStream[*pb.DownloadEtcdSnapshotResponse], error) {
	return c.DownloadEtcdSnapshot(ctx, connect.NewRequest(&pb.DownloadEtcdSnapshotRequest{}))
}

func TestRunDownloadWritesAPrivateFile(t *testing.T) {
	t.Parallel()

	f := &fakeDiagnostics{chunks: []*pb.DownloadEtcdSnapshotResponse{{Filename: "etcd.snapshot.db", Data: []byte("abc")}, {Data: []byte("def")}}}
	c := diagClients(t, f, false)
	out := filepath.Join(t.TempDir(), "snap.db")

	var stderr bytes.Buffer
	if err := runDownload(context.Background(), c, out, nil, &stderr, call); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(out)
	if err != nil || string(got) != "abcdef" {
		t.Fatalf("file = %q, %v", got, err)
	}

	if st, _ := os.Stat(out); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}

	if !strings.Contains(stderr.String(), "secrets") {
		t.Errorf("no warning in %q", stderr.String())
	}
}

func TestRunDownloadUsesTheServersNameInADirectory(t *testing.T) {
	t.Parallel()

	f := &fakeDiagnostics{chunks: []*pb.DownloadEtcdSnapshotResponse{{Filename: "../../evil/support-x.zip", Data: []byte("z")}}}
	dir := t.TempDir()

	if err := runDownload(context.Background(), diagClients(t, f, false), dir, nil, &bytes.Buffer{}, call); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "support-x.zip")); err != nil {
		t.Fatalf("the suggested name must be reduced to its base name inside the directory: %v", err)
	}
}

func TestRunDownloadLeavesNoFileWhenTheStreamBreaks(t *testing.T) {
	t.Parallel()

	f := &fakeDiagnostics{
		chunks:    []*pb.DownloadEtcdSnapshotResponse{{Filename: "x.db", Data: []byte("partial")}},
		failAfter: connect.NewError(connect.CodeResourceExhausted, errors.New("size cap")),
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "x.db")

	err := runDownload(context.Background(), diagClients(t, f, false), out, nil, &bytes.Buffer{}, call)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("err = %v", err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("a broken download must leave nothing behind, found %v", entries)
	}
}

func TestRunDownloadToStdout(t *testing.T) {
	t.Parallel()

	f := &fakeDiagnostics{chunks: []*pb.DownloadEtcdSnapshotResponse{{Filename: "x", Data: []byte("hello")}}}

	var out, errw bytes.Buffer
	if err := runDownload(context.Background(), diagClients(t, f, false), "-", &out, &errw, call); err != nil {
		t.Fatal(err)
	}

	if out.String() != "hello" || errw.Len() != 0 {
		t.Fatalf("stdout %q stderr %q", out.String(), errw.String())
	}
}

func TestRunDownloadExplainsADenialFromTheBackendOnly(t *testing.T) {
	t.Parallel()

	f := &fakeDiagnostics{deny: true}

	err := runDownload(context.Background(), diagClients(t, f, false), filepath.Join(t.TempDir(), "x"), nil, &bytes.Buffer{}, call)
	if connect.CodeOf(err) != connect.CodePermissionDenied || !strings.Contains(err.Error(), "--local") {
		t.Errorf("through ichor-ui the denial must point at --local, got %v", err)
	}

	err = runDownload(context.Background(), diagClients(t, f, true), filepath.Join(t.TempDir(), "x"), nil, &bytes.Buffer{}, call)
	if connect.CodeOf(err) != connect.CodePermissionDenied || strings.Contains(err.Error(), "--local") {
		t.Errorf("a denial of your own credentials must not say that, got %v", err)
	}
}

func TestPcapRequest(t *testing.T) {
	t.Parallel()

	req, err := pcapRequest("ns", "w1", "eth1", "{ 0x6, 0, 0, 0x0 },", 90*time.Second, 1234)
	if err != nil {
		t.Fatal(err)
	}

	if req.GetMachine().GetName() != "w1" || req.GetInterface() != "eth1" || req.GetDurationSeconds() != 90 ||
		req.GetMaxBytes() != 1234 || req.GetBpfFilter() == "" {
		t.Errorf("request = %v", req)
	}

	file := filepath.Join(t.TempDir(), "f.bpf")
	if err := os.WriteFile(file, []byte("{ 0x6, 0, 0, 0x0 },"), 0o600); err != nil {
		t.Fatal(err)
	}

	if req, err = pcapRequest("ns", "w1", "eth0", "@"+file, 0, 0); err != nil || req.GetBpfFilter() != "{ 0x6, 0, 0, 0x0 }," {
		t.Errorf("@FILE: %v, %v", req, err)
	}

	if _, err = pcapRequest("ns", "w1", "eth0", "@/nonexistent/filter", 0, 0); err == nil {
		t.Error("a missing filter file must be an error")
	}

	if _, err = pcapRequest("ns", "w1", "eth0", "", -time.Second, 0); err == nil {
		t.Error("a negative duration must be an error")
	}
}

func TestSupportBundleRequestNeedsExactlyOneTarget(t *testing.T) {
	t.Parallel()

	if _, err := supportBundleRequest("ns", nil, ""); err == nil {
		t.Error("no target")
	}

	if _, err := supportBundleRequest("ns", []string{"a"}, "c"); err == nil {
		t.Error("both targets")
	}

	if r, err := supportBundleRequest("ns", []string{"a"}, ""); err != nil || r.GetMachine().GetName() != "a" {
		t.Errorf("node: %v, %v", r, err)
	}

	if r, err := supportBundleRequest("ns", nil, "c"); err != nil || r.GetCluster().GetName() != "c" {
		t.Errorf("cluster: %v, %v", r, err)
	}
}

// The three downloads write a file, which --out names; -o stays the global yaml/json format, so
// `-o web.pcap` is refused as an invalid format instead of silently naming a file.
func TestDownloadCommandsTakeTheirFileAsOut(t *testing.T) {
	t.Parallel()

	for _, line := range paths("support-bundle\npcap\nsnapshot etcd") {
		path := strings.Fields(line)

		c, _, err := rootCmd.Find(path)
		if err != nil || c == rootCmd {
			t.Fatalf("ichor %v is missing: %v", path, err)
		}

		if f := c.Flags().Lookup("out"); f == nil || f.Shorthand != "" || f.DefValue != "" {
			t.Errorf("%v: --out must be the output file, got %+v", path, f)
		}

		if c.LocalNonPersistentFlags().Lookup("output") != nil {
			t.Errorf("%v: -o must be the global output format, not redefined", path)
		}

		if f := c.InheritedFlags().Lookup("output"); f == nil || f.Shorthand != "o" || f.DefValue != outputHuman {
			t.Errorf("%v: -o must be the inherited output format, got %+v", path, f)
		}
	}
}

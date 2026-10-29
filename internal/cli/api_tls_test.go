package cli

import (
	"context"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func portOf(srv *httptest.Server) int32 {
	return int32(srv.Listener.Addr().(*net.TCPAddr).Port) //nolint:errcheck,forcetypeassert // httptest listens on tcp
}

func TestTunnelClientTLSWithCAFile(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)

	ca := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})

	if err := os.WriteFile(ca, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	_, base, err := tunnelClient(context.Background(), portOf(srv), ca, nil, "example.com")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(base, "https://") {
		t.Fatalf("base = %s, want https", base)
	}
}

func TestTunnelClientTLSUnverifiableNamesCAFileAndLocal(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)

	_, _, err := tunnelClient(context.Background(), portOf(srv), "", nil, "")
	if err == nil || !strings.Contains(err.Error(), "--ca-file") || !strings.Contains(err.Error(), "--local") {
		t.Fatalf("err = %v, want it to name --ca-file and --local", err)
	}
}

func TestTunnelClientPlainHTTPFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)

	_, base, err := tunnelClient(context.Background(), portOf(srv), "", nil, "")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(base, "http://") {
		t.Fatalf("base = %s, want http", base)
	}
}

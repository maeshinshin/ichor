package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	_ "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1" // registers the services
	"github.com/maeshinshin/ichor/internal/uiapi"
)

// TestNothingServesAnAnonymousCaller sends every method of every service ichor-ui registers, found in the
// protobuf registry and not listed here, to the handlers mountRPC mounts with no authenticator: the
// state ichor-ui can no longer be started in. Each must be refused by the interceptor, reads included. The
// answer is checked for the interceptor's own words, so a 404 or a malformed request cannot pass for a refusal.
func TestNothingServesAnAnonymousCaller(t *testing.T) {
	log.SetOutput(io.Discard) // one access-log line per call
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	mux := http.NewServeMux()
	mountRPC(mux, &uiapi.Server{}, nil)

	var n int

	protoregistry.GlobalFiles.RangeFilesByPackage("ichor.v1alpha1", func(fd protoreflect.FileDescriptor) bool {
		for i := range fd.Services().Len() {
			svc := fd.Services().Get(i)

			for j := range svc.Methods().Len() {
				m := svc.Methods().Get(j)
				n++

				path := "/" + string(svc.FullName()) + "/" + string(m.Name())

				var req *http.Request
				if m.IsStreamingClient() || m.IsStreamingServer() {
					// A Connect stream: one envelope (flags 0, length 2) carrying {}.
					req = httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte{0, 0, 0, 0, 2, '{', '}'}))
					req.Header.Set("Content-Type", "application/connect+json")
				} else {
					req = httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
					req.Header.Set("Content-Type", "application/json")
				}

				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)

				if !strings.Contains(rec.Body.String(), "serves nobody") || !strings.Contains(rec.Body.String(), "permission_denied") {
					t.Errorf("%s: status %d, body %q: not refused by the interceptor", path, rec.Code, rec.Body.String())
				}
			}
		}

		return true
	})

	if n < 40 {
		t.Fatalf("only %d methods found: the registry walk is not seeing the services", n)
	}
}

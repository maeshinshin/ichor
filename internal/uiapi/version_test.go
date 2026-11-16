package uiapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/maeshinshin/ichor/internal/version"
)

const testVersion = "v9.9.9-version-test"

// The version header reaches an authenticated caller on every outcome of the call, and never an anonymous one.
func TestVersionHeaderGoesOnlyToAuthenticatedCalls(t *testing.T) {
	saved := version.Version
	t.Cleanup(func() { version.Version = saved })
	version.Version = testVersion

	p := newFakeIdP(t)
	auth := AuthInterceptor(newTestAuthenticator(t, p))
	ok := func(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
		return connect.NewResponse(&emptypb.Empty{}), nil
	}
	failing := func(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	call := func(fn func(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error), bearer string) *httptest.ResponseRecorder {
		h := connect.NewUnaryHandler("/ichor.test.v1.Ping/Ping", fn, connect.WithInterceptors(auth, VersionHeaderInterceptor()))
		req := httptest.NewRequest(http.MethodPost, "/ichor.test.v1.Ping/Ping", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", bearer)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	bearer := "Bearer " + p.mint(t, p.key, nil)

	if rec := call(ok, ""); rec.Header().Get("X-Ichor-Version") != "" {
		t.Errorf("anonymous: X-Ichor-Version = %q, want none", rec.Header().Get("X-Ichor-Version"))
	}
	if rec := call(ok, bearer); rec.Header().Get("X-Ichor-Version") != testVersion {
		t.Errorf("authenticated: X-Ichor-Version = %q, want %q", rec.Header().Get("X-Ichor-Version"), testVersion)
	}
	if rec := call(failing, bearer); rec.Header().Get("X-Ichor-Version") != testVersion {
		t.Errorf("authenticated, handler error: X-Ichor-Version = %q, want %q", rec.Header().Get("X-Ichor-Version"), testVersion)
	}
}

package uiapi

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestSanitizeErrorsHidesServerFailuresAndLogsThem(t *testing.T) {
	var buf bytes.Buffer

	raw := errors.New(`pods is forbidden: User "system:serviceaccount:ichor-system:ichor-ui" cannot list at 10.1.2.3`)
	h := connect.NewUnaryHandler("/x.Y/Z", func(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
		return nil, connect.NewError(connect.CodeInternal, raw)
	}, connect.WithInterceptors(SanitizeErrors(log.New(&buf, "", 0))))

	srv := httptest.NewServer(h)
	defer srv.Close()

	c := connect.NewClient[emptypb.Empty, emptypb.Empty](srv.Client(), srv.URL, connect.WithGRPC())

	_, err := c.CallUnary(context.Background(), connect.NewRequest(&emptypb.Empty{}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v", connect.CodeOf(err))
	}

	if strings.Contains(err.Error(), "serviceaccount") || strings.Contains(err.Error(), "10.1.2.3") {
		t.Fatalf("raw detail reached the client: %v", err)
	}

	if !strings.Contains(buf.String(), "serviceaccount") {
		t.Fatalf("the full error was not logged: %q", buf.String())
	}

	ref := err.Error()[strings.Index(err.Error(), "reference ")+len("reference "):][:8]
	if !strings.Contains(buf.String(), "ref="+ref) {
		t.Fatalf("the reference %q is not in the log %q", ref, buf.String())
	}
}

func TestSanitizeErrorsKeepsCallerErrors(t *testing.T) {
	h := connect.NewUnaryHandler("/x.Y/Z", func(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New(`you may not: forbidden`))
	}, connect.WithInterceptors(SanitizeErrors(log.New(&bytes.Buffer{}, "", 0))))

	srv := httptest.NewServer(http.Handler(h))
	defer srv.Close()

	c := connect.NewClient[emptypb.Empty, emptypb.Empty](srv.Client(), srv.URL, connect.WithGRPC())

	if _, err := c.CallUnary(context.Background(), connect.NewRequest(&emptypb.Empty{})); !strings.Contains(err.Error(), "you may not") {
		t.Fatalf("a caller error was rewritten: %v", err)
	}
}

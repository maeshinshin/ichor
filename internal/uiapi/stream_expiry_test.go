package uiapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fakeLocalExp time.Duration

func (f fakeLocalExp) Verify(context.Context, string) (TokenInfo, error) {
	now := time.Now()

	return TokenInfo{Subject: userAlice, IssuedAt: now, Expires: now.Add(time.Duration(f))}, nil
}

func TestStreamEndsWhenTheTokenExpires(t *testing.T) {
	s := userServer(t, true, newUser(userAlice, false, adminRole))
	a := (*Authenticator)(nil).WithLocal("https://i", fakeLocalExp(300*time.Millisecond), s.Users.Cache)

	h := connect.NewServerStreamHandler("/x.Y/S", func(ctx context.Context, _ *connect.Request[emptypb.Empty], _ *connect.ServerStream[emptypb.Empty]) error {
		<-ctx.Done() // a follow-mode log stream with nothing to say

		return ctx.Err()
	}, connect.WithInterceptors(AuthInterceptor(a)))

	srv := httptest.NewServer(h)
	defer srv.Close()

	c := connect.NewClient[emptypb.Empty, emptypb.Empty](srv.Client(), srv.URL+"/x.Y/S", connect.WithGRPC())
	req := connect.NewRequest(&emptypb.Empty{})
	req.Header().Set("Authorization", "Bearer e30."+b64(`{"iss":"https://i"}`)+".t")

	start := time.Now()

	st, err := c.CallServerStream(context.Background(), req)
	if err == nil {
		for st.Receive() {
		}

		err = st.Err()
	}

	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodeUnauthenticated {
		t.Fatalf("stream end = %v, want Unauthenticated", err)
	}

	if time.Since(start) > 5*time.Second {
		t.Fatalf("the stream outlived its token: %v", time.Since(start))
	}
}

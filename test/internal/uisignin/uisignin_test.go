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

package uisignin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

type fakeUsers struct {
	ichorv1alpha1connect.UnimplementedUserServiceHandler
	mustChange bool
	changed    *bool
}

func (f fakeUsers) WhoAmI(context.Context, *connect.Request[pb.WhoAmIRequest]) (*connect.Response[pb.WhoAmIResponse], error) {
	return connect.NewResponse(&pb.WhoAmIResponse{MustChangePassword: f.mustChange && !*f.changed}), nil
}

func (f fakeUsers) ChangeOwnPassword(_ context.Context, r *connect.Request[pb.ChangeOwnPasswordRequest]) (*connect.Response[pb.ChangeOwnPasswordResponse], error) {
	if r.Msg.GetNewPassword() != NewPassword {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	*f.changed = true
	return connect.NewResponse(&pb.ChangeOwnPasswordResponse{}), nil
}

// fakeIssuer serves the three issuer endpoints and the UserService; the token it issues names the password used.
func fakeIssuer(mustChange bool) *httptest.Server {
	changed := false
	mux := http.NewServeMux()
	mux.HandleFunc("/oidc/authorize", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" || r.URL.Query().Get("audience") != "ichor-ui" ||
			r.URL.Query().Get("client_id") != "ichor-cli" {
			http.Error(w, "bad authorize", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"ticket": "t1"})
	})
	mux.HandleFunc("/oidc/login", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["ticket"] != "t1" || b["username"] != "u" || (changed && b["password"] != NewPassword) {
			http.Error(w, "bad login", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"redirect_uri": "http://127.0.0.1:1/callback?code=pw-" + b["password"]})
	})
	mux.HandleFunc("/oidc/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code_verifier") == "" {
			http.Error(w, "no verifier", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": "tok-" + r.Form.Get("code")})
	})
	p, h := ichorv1alpha1connect.NewUserServiceHandler(fakeUsers{mustChange: mustChange, changed: &changed})
	mux.Handle(p, h)
	return httptest.NewServer(mux)
}

func TestSignIn(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mustChange bool
		want       string
	}{
		{"happy path", false, "tok-pw-secret"},
		{"must_change_password", true, "tok-pw-" + NewPassword},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeIssuer(tc.mustChange)
			defer srv.Close()
			got, err := SignIn(context.Background(), srv.Client(), srv.URL, "u", "secret")
			if err != nil || got != tc.want {
				t.Fatalf("SignIn = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestSignInRejected(t *testing.T) {
	srv := fakeIssuer(false)
	defer srv.Close()
	_, err := SignIn(context.Background(), srv.Client(), srv.URL, "other", "secret")
	if err == nil || !strings.Contains(err.Error(), "login") {
		t.Fatalf("err = %v, want a login error", err)
	}
}

// A must-change password is changed by the first sign-in, so the next run with the same original password signs in
// with the new one (reclaimui runs once per call).
func TestSignInAfterPasswordChange(t *testing.T) {
	srv := fakeIssuer(true)
	defer srv.Close()
	for i := range 2 {
		got, err := SignIn(context.Background(), srv.Client(), srv.URL, "u", "secret")
		if err != nil || got != "tok-pw-"+NewPassword {
			t.Fatalf("sign-in %d = %q, %v", i+1, got, err)
		}
	}
}

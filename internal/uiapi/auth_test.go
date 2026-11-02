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
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/go-jose/go-jose/v4"
	"google.golang.org/protobuf/types/known/emptypb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testClientID = "ichor-ui"
	testGroupDev = "oidc:dev"
	testUserBob  = "oidc:bob"
)

// fakeIdP is an httptest OIDC provider: discovery plus a JWKS, and a way to
// mint signed ID tokens. It never talks to a real IdP.
type fakeIdP struct {
	srv *httptest.Server
	key *rsa.PrivateKey
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	p := &fakeIdP{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.srv.URL, "jwks_uri": p.srv.URL + "/keys",
			"authorization_endpoint": p.srv.URL + "/auth", "token_endpoint": p.srv.URL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"},
		}})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)

	return p
}

// mint signs claims with signer (the IdP's key unless a test passes another).
func (p *fakeIdP) mint(t *testing.T, signer *rsa.PrivateKey, mod func(map[string]any)) string {
	t.Helper()

	claims := map[string]any{
		"iss": p.srv.URL, "aud": testClientID, "sub": "u1",
		"email": "alice@example.com", "email_verified": true,
		"groups": []string{"dev", "system:masters"},
		"iat":    time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	}
	if mod != nil {
		mod(claims)
	}

	s, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: signer, KeyID: "k1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(claims)

	obj, err := s.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}

	tok, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}

	return tok
}

func newTestAuthenticator(t *testing.T, p *fakeIdP) *Authenticator {
	t.Helper()

	a, err := NewAuthenticator(context.Background(), OIDCConfig{
		Issuer: p.srv.URL, ClientID: testClientID, UsernamePrefix: "oidc:", GroupsPrefix: "oidc:",
	})
	if err != nil {
		t.Fatal(err)
	}

	return a
}

func TestAuthenticateAcceptsAValidTokenAndPrefixesIdentity(t *testing.T) {
	p := newFakeIdP(t)
	a := newTestAuthenticator(t, p)

	id, err := a.Authenticate(context.Background(), "Bearer "+p.mint(t, p.key, nil))
	if err != nil {
		t.Fatal(err)
	}

	if id.User != "oidc:alice@example.com" {
		t.Errorf("user = %q", id.User)
	}
	// A group is only ever sent with the prefix, so "system:masters" from the
	// IdP cannot become the built-in group.
	if want := []string{testGroupDev, "oidc:system:masters"}; !slices.Equal(id.Groups, want) {
		t.Errorf("groups = %v, want %v", id.Groups, want)
	}
}

func TestAuthenticateRejectsBadTokensWithoutEchoingThem(t *testing.T) {
	p := newFakeIdP(t)
	a := newTestAuthenticator(t, p)

	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"expired":        p.mint(t, p.key, func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }),
		"wrong audience": p.mint(t, p.key, func(c map[string]any) { c["aud"] = "someone-else" }),
		"wrong issuer":   p.mint(t, p.key, func(c map[string]any) { c["iss"] = "https://evil.example" }),
		"bad signature":  p.mint(t, other, nil),
		"unverified":     p.mint(t, p.key, func(c map[string]any) { c["email_verified"] = false }),
		"no username":    p.mint(t, p.key, func(c map[string]any) { delete(c, "email") }),
		"garbage":        "not-a-jwt-SECRETVALUE",
	}

	for name, tok := range cases {
		_, err := a.Authenticate(context.Background(), "Bearer "+tok)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}

		if strings.Contains(err.Error(), tok) || strings.Contains(err.Error(), "SECRETVALUE") {
			t.Errorf("%s: error echoes the token: %v", name, err)
		}
	}

	for _, h := range []string{"", "Basic abc", "Bearer "} {
		if _, err := a.Authenticate(context.Background(), h); err == nil {
			t.Errorf("header %q accepted", h)
		}
	}
}

func TestAuthenticateRefusesSystemUsers(t *testing.T) {
	p := newFakeIdP(t)

	a, err := NewAuthenticator(context.Background(), OIDCConfig{Issuer: p.srv.URL, ClientID: testClientID})
	if err != nil {
		t.Fatal(err)
	}

	tok := p.mint(t, p.key, func(c map[string]any) { c["email"] = "system:admin" })
	if _, err := a.Authenticate(context.Background(), "Bearer "+tok); err == nil {
		t.Fatal("a token naming system:admin was accepted with no username prefix")
	}
}

func TestInterceptorRequiresTokenAndStoresIdentity(t *testing.T) {
	p := newFakeIdP(t)
	wrapped := authInterceptor{a: newTestAuthenticator(t, p)}.WrapUnary(
		func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
			id, ok := IdentityFrom(ctx)
			if !ok {
				return nil, connect.NewError(connect.CodeInternal, nil)
			}

			return connect.NewResponse(&emptypb.Empty{}), nilIfUser(id)
		})

	req := connect.NewRequest(&emptypb.Empty{})
	if _, err := wrapped(context.Background(), req); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("no token: code %v", connect.CodeOf(err))
	}

	// A malformed token is Unauthenticated (401 for ConnectRPC over HTTP), never Internal (500).
	for _, bad := range []string{"Bearer nope", "Bearer a.b.c", "Bearer \x00", "Basic abc"} {
		req.Header().Set("Authorization", bad)
		if _, err := wrapped(context.Background(), req); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("%q: code %v, want unauthenticated", bad, connect.CodeOf(err))
		}
	}

	req.Header().Set("Authorization", "Bearer "+p.mint(t, p.key, nil))
	if _, err := wrapped(context.Background(), req); err != nil {
		t.Fatalf("valid token: %v", err)
	}
}

func nilIfUser(id Identity) error {
	if id.User != "oidc:alice@example.com" {
		return connect.NewError(connect.CodeInternal, nil)
	}

	return nil
}

func TestInterceptorDisabledPassesThroughWithoutIdentity(t *testing.T) {
	wrapped := AuthInterceptor(nil, true).WrapUnary(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		if _, ok := IdentityFrom(ctx); ok {
			t.Error("identity set with OIDC disabled")
		}

		return connect.NewResponse(&emptypb.Empty{}), nil
	})

	if _, err := wrapped(context.Background(), connect.NewRequest(&emptypb.Empty{})); err != nil {
		t.Fatal(err)
	}
}

func cmClient(name string) client.Client {
	cm := &corev1.ConfigMap{Data: map[string]string{"by": name}}
	cm.Name, cm.Namespace = "x", "d"

	return fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).WithObjects(cm).Build()
}

func TestServerUsesImpersonatedClientAndNamesTheUserAsActor(t *testing.T) {
	var seen []Identity

	s := &Server{
		Client: cmClient("sa"),
		Impersonate: func(id Identity) (client.Client, error) {
			seen = append(seen, id)
			return cmClient("user"), nil
		},
	}

	var cm corev1.ConfigMap

	key := client.ObjectKey{Namespace: "d", Name: "x"}
	if err := s.Get(context.Background(), key, &cm); err != nil || cm.Data["by"] != "sa" {
		t.Fatalf("no identity must use the ServiceAccount: %v %v", err, cm.Data)
	}

	if got := s.actorName(context.Background()); got != "ichor-ui" {
		t.Errorf("actor without identity = %q", got)
	}

	ctx := context.WithValue(context.Background(), identityKey{}, Identity{User: testUserBob, Groups: []string{"g"}})
	if err := s.Get(ctx, key, &cm); err != nil || cm.Data["by"] != "user" {
		t.Fatalf("identity must use the impersonated client: %v %v", err, cm.Data)
	}

	if len(seen) != 1 || seen[0].User != testUserBob {
		t.Errorf("impersonated as %v", seen)
	}

	if got := s.actorName(ctx); got != testUserBob {
		t.Errorf("actor = %q", got)
	}
	// The SA-only path (secrets bundle, Argo CD) must ignore the identity.
	if err := s.Client.Get(ctx, key, &cm); err != nil || cm.Data["by"] != "sa" {
		t.Errorf("s.Client must stay the ServiceAccount: %v", cm.Data)
	}
}

func TestImpersonatorSendsImpersonationHeaders(t *testing.T) {
	var (
		mu  sync.Mutex
		hdr http.Header
	)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hdr = r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"x","namespace":"d"}}`))
	}))
	defer api.Close()

	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.Add(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}, meta.RESTScopeNamespace)

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)

	c, err := NewImpersonator(&rest.Config{Host: api.URL, BearerToken: "sa-token"}, client.Options{Scheme: scheme, Mapper: mapper})(
		Identity{User: "oidc:alice", Groups: []string{testGroupDev, "oidc:ops"}})
	if err != nil {
		t.Fatal(err)
	}

	var cm corev1.ConfigMap
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "d", Name: "x"}, &cm); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if hdr.Get("Impersonate-User") != "oidc:alice" {
		t.Errorf("Impersonate-User = %q", hdr.Get("Impersonate-User"))
	}

	if !slices.Equal(hdr.Values("Impersonate-Group"), []string{testGroupDev, "oidc:ops"}) {
		t.Errorf("Impersonate-Group = %v", hdr.Values("Impersonate-Group"))
	}
}

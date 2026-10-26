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
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/coreos/go-oidc/v3/oidc"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Identity is who an authenticated request is acting as, already in the form
// Kubernetes sees it: prefixes applied, so Impersonate-User and
// AuditRecord.actor carry the same string (docs/adr/0058).
type Identity struct {
	User   string
	Groups []string
}

type identityKey struct{}

// IdentityFrom returns the identity AuthInterceptor stored in ctx.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// OIDCConfig is the browser-facing OIDC setup. There is deliberately no
// client secret: the SPA is a public client using the authorization code
// flow with PKCE, and this backend only verifies the ID token it presents.
type OIDCConfig struct {
	Issuer, ClientID string
	// UsernameClaim defaults to "email" (which must then be email_verified),
	// GroupsClaim to "groups". The prefixes keep an IdP-chosen name from
	// colliding with a Kubernetes built-in such as "system:masters".
	UsernameClaim, GroupsClaim, UsernamePrefix, GroupsPrefix string
}

// Authenticator verifies ID tokens against one issuer.
type Authenticator struct {
	cfg      OIDCConfig
	verifier *oidc.IDTokenVerifier
}

// NewAuthenticator runs OIDC discovery against cfg.Issuer. The verifier
// checks signature (keys fetched from the issuer's JWKS), issuer, audience
// (ClientID) and expiry.
func NewAuthenticator(ctx context.Context, cfg OIDCConfig) (*Authenticator, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" {
		return nil, errors.New("OIDC needs both an issuer and a client ID")
	}

	if cfg.UsernameClaim == "" {
		cfg.UsernameClaim = "email"
	}

	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}

	p, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery at %s: %w", cfg.Issuer, err)
	}

	return &Authenticator{cfg: cfg, verifier: p.Verifier(&oidc.Config{ClientID: cfg.ClientID})}, nil
}

// Authenticate turns an "Authorization: Bearer <id token>" header into an
// Identity. Errors never include the token or the verifier's detail beyond a
// fixed phrase, so nothing sensitive can reach a log or a response.
func (a *Authenticator) Authenticate(ctx context.Context, header string) (Identity, error) {
	raw, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || raw == "" {
		return Identity{}, errors.New("missing bearer token")
	}

	tok, err := a.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, errors.New("invalid ID token")
	}

	var claims map[string]any
	if err := tok.Claims(&claims); err != nil {
		return Identity{}, errors.New("invalid ID token claims")
	}

	name, _ := claims[a.cfg.UsernameClaim].(string)
	if name == "" {
		return Identity{}, fmt.Errorf("ID token has no %q claim", a.cfg.UsernameClaim)
	}

	if a.cfg.UsernameClaim == "email" {
		if verified, _ := claims["email_verified"].(bool); !verified {
			return Identity{}, errors.New("ID token email is not verified")
		}
	}

	id := Identity{User: a.cfg.UsernamePrefix + name}
	// Never let a token name a Kubernetes built-in identity.
	if strings.HasPrefix(id.User, "system:") {
		return Identity{}, errors.New("ID token names a reserved system: user")
	}

	if gs, ok := claims[a.cfg.GroupsClaim].([]any); ok {
		for _, g := range gs {
			if s, ok := g.(string); ok && s != "" && !strings.HasPrefix(a.cfg.GroupsPrefix+s, "system:") {
				id.Groups = append(id.Groups, a.cfg.GroupsPrefix+s)
			}
		}
	}

	return id, nil
}

// AuthInterceptor requires a valid ID token on every RPC and stores the
// resulting Identity in the handler's context. One place, like
// MetricsInterceptor; pass it to every *ServiceHandler. a == nil (OIDC
// disabled) yields a pass-through.
func AuthInterceptor(a *Authenticator) connect.Interceptor {
	return authInterceptor{a}
}

type authInterceptor struct{ a *Authenticator }

func (i authInterceptor) auth(ctx context.Context, authz string) (context.Context, error) {
	if i.a == nil {
		return ctx, nil
	}

	id, err := i.a.Authenticate(ctx, authz)
	if err != nil {
		return ctx, connect.NewError(connect.CodeUnauthenticated, err)
	}

	return context.WithValue(ctx, identityKey{}, id), nil
}

func (i authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ctx, err := i.auth(ctx, req.Header().Get("Authorization"))
		if err != nil {
			return nil, err
		}

		return next(ctx, req)
	}
}

func (i authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ctx, err := i.auth(ctx, conn.RequestHeader().Get("Authorization"))
		if err != nil {
			return err
		}

		return next(ctx, conn)
	}
}

// NewImpersonator builds the Server.Impersonate func: a client that sends
// Impersonate-User / Impersonate-Group for id, so the API server (not Ichor)
// decides what the user may do. opts should carry the base client's
// RESTMapper to avoid a discovery round trip per request.
func NewImpersonator(cfg *rest.Config, opts client.Options) func(Identity) (client.Client, error) {
	return func(id Identity) (client.Client, error) {
		c := rest.CopyConfig(cfg)
		c.Impersonate = rest.ImpersonationConfig{UserName: id.User, Groups: id.Groups}

		return client.New(c, opts)
	}
}

// cl is the client a call made under ctx must use: the user's when ctx
// carries an Identity and impersonation is wired, else the ServiceAccount's.
func (s *Server) cl(ctx context.Context) (client.Client, error) {
	if id, ok := IdentityFrom(ctx); ok && s.Impersonate != nil {
		return s.Impersonate(id)
	}

	return s.Client, nil
}

// Get, List, Create, Update and Delete are the calls handlers make on
// user-requested CRs. They shadow the embedded client's methods so the
// handlers need no change; reads that must stay on the ServiceAccount
// (secrets bundle, Argo CD, Nodes) call s.Client.X explicitly.
func (s *Server) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	c, err := s.cl(ctx)
	if err != nil {
		return err
	}

	return c.Get(ctx, key, obj, opts...)
}

func (s *Server) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	c, err := s.cl(ctx)
	if err != nil {
		return err
	}

	return c.List(ctx, list, opts...)
}

func (s *Server) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c, err := s.cl(ctx)
	if err != nil {
		return err
	}

	return c.Create(ctx, obj, opts...)
}

func (s *Server) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	c, err := s.cl(ctx)
	if err != nil {
		return err
	}

	return c.Update(ctx, obj, opts...)
}

func (s *Server) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	c, err := s.cl(ctx)
	if err != nil {
		return err
	}

	return c.Delete(ctx, obj, opts...)
}

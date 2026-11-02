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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/coreos/go-oidc/v3/oidc"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

// Identity is who an authenticated request is acting as, already in the form
// Kubernetes sees it: prefixes applied, so Impersonate-User and
// AuditRecord.actor carry the same string.
type Identity struct {
	User   string
	Groups []string
	// Source is SourceIchor or SourceOIDC; DisplayName is set
	// for an Ichor user only.
	Source, DisplayName string
	// MustChange is set for an Ichor user whose password must be changed
	// before anything else; AuthInterceptor then allows only the calls in
	// mustChangeAllowed.
	MustChange bool
	// Expires is when the ID token ends; a stream is closed then.
	Expires time.Time
}

// The two places an identity can come from.
const (
	SourceIchor = "ichor"
	SourceOIDC  = "oidc"
)

// Prefixes for Ichor users and their roles. The external IdP is mapped with
// its own prefix (default "oidc:"), and the process refuses one that could
// collide with these.
const (
	IchorUserPrefix  = "ichor:"
	IchorGroupPrefix = "ichor:"
)

// LocalVerifier verifies an ID token minted by Ichor's own issuer, in process,
// against its own keys: signature, issuer, audience "ichor-ui" and expiry.
type LocalVerifier interface {
	Verify(ctx context.Context, raw string) (TokenInfo, error)
}

// TokenInfo is what a LocalVerifier proves about a token.
type TokenInfo struct {
	Subject           string
	IssuedAt, Expires time.Time
}

type identityKey struct{}

// IdentityFrom returns the identity AuthInterceptor stored in ctx.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// ContextWithIdentity is what AuthInterceptor does on success; cross-package tests use it to act as a user.
func ContextWithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
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

// Authenticator verifies ID tokens from up to two sources:
// an external IdP (go-oidc) and Ichor's own issuer (verified in
// process). It routes by the token's unverified "iss" claim, and each path
// then verifies the token in full, so a token never gets a looser check by
// naming the other issuer.
type Authenticator struct {
	cfg      OIDCConfig
	verifier *oidc.IDTokenVerifier

	local       LocalVerifier
	localIssuer string
	users       *UserCache
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

// WithLocal returns a that also accepts tokens from Ichor's own issuer
// (iss == issuer). a may be nil (Ichor users only).
func (a *Authenticator) WithLocal(issuer string, v LocalVerifier, users *UserCache) *Authenticator {
	out := &Authenticator{}
	if a != nil {
		*out = *a
	}

	out.local, out.localIssuer, out.users = v, issuer, users

	return out
}

// unverifiedIssuer reads "iss" from a JWT's payload without checking anything;
// it only chooses which verifier then checks the whole token.
func unverifiedIssuer(raw string) string {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return ""
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var c struct {
		Iss string `json:"iss"`
	}

	_ = json.Unmarshal(payload, &c)

	return c.Iss
}

// Authenticate turns an "Authorization: Bearer <id token>" header into an
// Identity. Errors never include the token or the verifier's detail beyond a
// fixed phrase, so nothing sensitive can reach a log or a response.
func (a *Authenticator) Authenticate(ctx context.Context, header string) (Identity, error) {
	raw, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || raw == "" {
		return Identity{}, errors.New("missing bearer token")
	}

	if a.local != nil && unverifiedIssuer(raw) == a.localIssuer {
		return a.authenticateLocal(ctx, raw)
	}

	if a.verifier == nil {
		return Identity{}, errors.New("invalid ID token")
	}

	return a.authenticateExternal(ctx, raw)
}

// authenticateLocal verifies a token of Ichor's own issuer, then re-reads the
// IchorUser (a cache of at most 10 s): a disabled or deleted user is refused,
// and the roles come from the CR, not from the token, so a role change or a
// disable takes effect here within seconds.
func (a *Authenticator) authenticateLocal(ctx context.Context, raw string) (Identity, error) {
	tok, err := a.local.Verify(ctx, raw)
	if err != nil || tok.Subject == "" || a.users == nil {
		return Identity{}, errors.New("invalid ID token")
	}

	u, err := a.users.Get(ctx, tok.Subject)
	if err != nil || u.Spec.Disabled {
		return Identity{}, errors.New("invalid ID token")
	}

	mc, notBefore, err := a.users.Session(ctx, u.Name)
	if err != nil {
		return Identity{}, errors.New("invalid ID token")
	}

	// RevokeSessions and a password change move notBefore: a token issued before it is dead here
	// too, not only at the issuer's refresh. Whole seconds, like the token's iat.
	if tok.IssuedAt.Unix() < notBefore.Unix() {
		return Identity{}, errors.New("invalid ID token")
	}

	id := Identity{User: IchorUserPrefix + u.Name, Source: SourceIchor, DisplayName: u.Spec.DisplayName, MustChange: mc, Expires: tok.Expires}
	for _, r := range u.Spec.Roles {
		id.Groups = append(id.Groups, IchorGroupPrefix+r)
	}

	return id, nil
}

func (a *Authenticator) authenticateExternal(ctx context.Context, raw string) (Identity, error) {
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

	id := Identity{User: a.cfg.UsernamePrefix + name, Source: SourceOIDC, Expires: tok.Expiry}
	// Never let a token name a Kubernetes built-in identity.
	if strings.HasPrefix(id.User, "system:") || strings.HasPrefix(id.User, IchorUserPrefix) {
		return Identity{}, errors.New("ID token names a reserved user")
	}

	if gs, ok := claims[a.cfg.GroupsClaim].([]any); ok {
		for _, g := range gs {
			if s, ok := g.(string); ok && s != "" && !strings.HasPrefix(a.cfg.GroupsPrefix+s, "system:") &&
				!strings.HasPrefix(a.cfg.GroupsPrefix+s, IchorGroupPrefix) {
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

// mustChangeAllowed are the only RPCs a user who must change the password may
// call: find out why (WhoAmI) and fix it. Signing out is client-side.
var mustChangeAllowed = map[string]bool{
	ichorv1alpha1connect.UserServiceWhoAmIProcedure:            true,
	ichorv1alpha1connect.UserServiceChangeOwnPasswordProcedure: true,
}

func (i authInterceptor) auth(ctx context.Context, authz, procedure string) (context.Context, error) {
	if i.a == nil {
		return ctx, nil
	}

	id, err := i.a.Authenticate(ctx, authz)
	if err != nil {
		return ctx, connect.NewError(connect.CodeUnauthenticated, err)
	}

	if id.MustChange && !mustChangeAllowed[procedure] {
		return ctx, connect.NewError(connect.CodePermissionDenied, errors.New("change your password first"))
	}

	return context.WithValue(ctx, identityKey{}, id), nil
}

func (i authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ctx, err := i.auth(ctx, req.Header().Get("Authorization"), req.Spec().Procedure)
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
		ctx, err := i.auth(ctx, conn.RequestHeader().Get("Authorization"), conn.Spec().Procedure)
		if err != nil {
			return err
		}

		// A stream outlives the check made at its start: end it when the token does, so a
		// revoked or disabled user cannot keep reading logs for hours.
		id, ok := IdentityFrom(ctx)
		if !ok || id.Expires.IsZero() {
			return next(ctx, conn)
		}

		sctx, cancel := context.WithDeadline(ctx, id.Expires)
		defer cancel()

		err = next(sctx, conn)
		if err != nil && ctx.Err() == nil && sctx.Err() != nil {
			return connect.NewError(connect.CodeUnauthenticated, errors.New("your sign-in expired; sign in again"))
		}

		return err
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
// (secrets bundle) call s.Client.X explicitly.
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

// Package oidcissuer is Ichor's minimal stateless OIDC issuer (ADR 0094,
// Decision 2): discovery, JWKS, authorization code + PKCE S256, and a
// non-rotating refresh grant. Codes, login tickets and refresh tokens are JWS
// signed by the issuer key, so no replica stores anything.
//
// It is a library plus http.Handlers. Signing keys, the user directory and the
// password check come in through small interfaces.
package oidcissuer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// Fixed clients and audiences (ADR 0094).
const (
	ClientUI  = "ichor-ui"
	ClientCLI = "ichor-cli"

	AudienceUI   = "ichor-ui"
	AudienceKube = "ichor-kube"

	CodeTTL   = 60 * time.Second
	TicketTTL = 5 * time.Minute

	DefaultTokenTTL      = 5 * time.Minute
	DefaultRefreshLife   = 12 * time.Hour
	defaultLoginPath     = "/login"
	maxFormBytes         = 64 << 10
	maxLoginBytes        = 16 << 10
	maxUserNameLen       = 63
	errInvalidRequest    = "invalid_request"
	errInvalidGrant      = "invalid_grant"
	errAccessDenied      = "access_denied"
	scopeOpenID          = "openid"
	scopeOffline         = "offline_access"
	codeChallengeMethodS = "S256"
	endpointAuthorize    = "/oidc/authorize"
	endpointLogin        = "/oidc/login"
	endpointToken        = "/oidc/token"
	endpointJWKS         = "/oidc/jwks"
	endpointDiscovery    = "/.well-known/openid-configuration"
	minTokenTTL          = time.Minute
	maxTokenTTL          = 15 * time.Minute
	minRefreshLife       = time.Hour
	maxRefreshLife       = 30 * 24 * time.Hour
)

// ErrUserNotFound is returned by UserDirectory.LookupUser for an unknown user.
var ErrUserNotFound = errors.New("oidcissuer: user not found")

// User is what the issuer needs from an IchorUser plus its credentials entry.
type User struct {
	Name      string
	Roles     []string  // become the "groups" claim
	Disabled  bool      // spec.disabled
	NotBefore time.Time // credentials entry notBefore; zero means none
}

// UserDirectory looks up a user by name (a cached read is fine; the ADR allows
// up to 10 s). It returns ErrUserNotFound for an unknown user.
type UserDirectory interface {
	LookupUser(ctx context.Context, name string) (User, error)
}

// PasswordVerifier checks a password. Implementations must spend the same time
// for an unknown user as for a wrong password (verify against a dummy hash),
// and report both as ok=false with a nil error. A non-nil error is an
// infrastructure failure.
type PasswordVerifier interface {
	VerifyPassword(ctx context.Context, name, password string) (ok bool, err error)
}

// Config configures an Issuer.
type Config struct {
	// Issuer is the external https URL (no path, no trailing slash): the "iss"
	// of every token and the discovery document's issuer.
	Issuer string
	// InternalJWKSURI is the jwks_uri advertised by InternalHandler (the
	// ClusterIP listener). Empty means the external one.
	InternalJWKSURI string
	// UIRedirectURI is the exact redirect URI of the ichor-ui client,
	// <externalURL>/auth/callback.
	UIRedirectURI string
	// LoginPath is the SPA route the authorize endpoint sends the browser to
	// (default "/login"); it receives ?ticket=.
	LoginPath string

	TokenTTL        time.Duration // default 5m, 1m..15m
	RefreshLifetime time.Duration // default 12h, 1h..30d

	Keys      KeySource
	Users     UserDirectory
	Passwords PasswordVerifier

	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Issuer is the OIDC provider. Create it with New.
type Issuer struct {
	cfg       Config
	failures  *backoff
	usedCodes *jtiSet
}

// New validates cfg and returns an Issuer.
func New(cfg Config) (*Issuer, error) {
	u, err := url.Parse(cfg.Issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, errors.New("oidcissuer: Issuer must be an https URL without path, query or userinfo")
	}
	if cfg.Keys == nil || cfg.Users == nil || cfg.Passwords == nil {
		return nil, errors.New("oidcissuer: Keys, Users and Passwords are required")
	}
	if cfg.UIRedirectURI == "" {
		return nil, errors.New("oidcissuer: UIRedirectURI is required")
	}
	if cfg.TokenTTL == 0 {
		cfg.TokenTTL = DefaultTokenTTL
	}
	if cfg.RefreshLifetime == 0 {
		cfg.RefreshLifetime = DefaultRefreshLife
	}
	if cfg.TokenTTL < minTokenTTL || cfg.TokenTTL > maxTokenTTL {
		return nil, fmt.Errorf("oidcissuer: TokenTTL must be between %s and %s", minTokenTTL, maxTokenTTL)
	}
	if cfg.RefreshLifetime < minRefreshLife || cfg.RefreshLifetime > maxRefreshLife {
		return nil, fmt.Errorf("oidcissuer: RefreshLifetime must be between %s and %s", minRefreshLife, maxRefreshLife)
	}
	if cfg.LoginPath == "" {
		cfg.LoginPath = defaultLoginPath
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Issuer{cfg: cfg, failures: newBackoff(), usedCodes: newJTISet()}, nil
}

// Handler serves discovery, JWKS, authorize, login and token (port 8080 side).
func (i *Issuer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+endpointDiscovery, i.discovery(i.cfg.Issuer+endpointJWKS))
	mux.HandleFunc("GET "+endpointJWKS, i.jwks)
	mux.HandleFunc("GET "+endpointAuthorize, i.authorize)
	mux.HandleFunc("POST "+endpointLogin, i.login)
	mux.HandleFunc("POST "+endpointToken, i.token)
	return mux
}

// InternalHandler serves only discovery (with the internal jwks_uri) and the
// JWKS, for the kube-apiserver through discoveryURL (port 8444 side).
func (i *Issuer) InternalHandler() http.Handler {
	jwksURI := i.cfg.InternalJWKSURI
	if jwksURI == "" {
		jwksURI = i.cfg.Issuer + endpointJWKS
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+endpointDiscovery, i.discovery(jwksURI))
	mux.HandleFunc("GET "+endpointJWKS, i.jwks)
	return mux
}

func (i *Issuer) discovery(jwksURI string) http.HandlerFunc {
	doc := map[string]any{
		"issuer":                                i.cfg.Issuer,
		"authorization_endpoint":                i.cfg.Issuer + endpointAuthorize,
		"token_endpoint":                        i.cfg.Issuer + endpointToken,
		"jwks_uri":                              jwksURI,
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{codeChallengeMethodS},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{scopeOpenID, scopeOffline},
		"claims_supported":                      []string{"iss", "sub", "aud", "exp", "iat", "groups", "nonce"},
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", noCache)
		writeJSON(w, http.StatusOK, doc)
	}
}

func (i *Issuer) jwks(w http.ResponseWriter, _ *http.Request) {
	set := jose.JSONWebKeySet{}
	for _, k := range i.cfg.Keys.Public() {
		pub := k.Public()
		if !pub.Valid() {
			continue
		}
		pub.Use, pub.Algorithm = jwkUseSig, string(jose.RS256)
		set.Keys = append(set.Keys, pub)
	}
	w.Header().Set("Cache-Control", noCache)
	writeJSON(w, http.StatusOK, set)
}

// noCache is deliberate for discovery and the JWKS: the kube-apiserver's
// go-oidc (v2.5.0) keeps fetched keys until the response's own cache expiry
// and refetches on an unknown kid only after that, so any positive max-age
// would delay a rotation by that long. Without one it refetches immediately.
const jwkUseSig = "sig"

const noCache = "no-cache"

// Package oidcissuer is Ichor's minimal stateless OIDC issuer: discovery, JWKS, authorization code + PKCE S256, and a
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
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

// Fixed clients, audiences and lifetimes live in ichorauth.
const (
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
)

// ErrUserNotFound is returned by UserDirectory.LookupUser for an unknown user.
var ErrUserNotFound = errors.New("oidcissuer: user not found")

// User is what the issuer needs from an IchorUser plus its credentials entry.
type User struct {
	Name      string
	Roles     []string  // become the "groups" claim
	Disabled  bool      // spec.disabled
	NotBefore time.Time // credentials entry notBefore; zero means none
	// MustChange is the credentials entry's mustChange: the user may sign in
	// to the UI (audience ichor-ui) to change the password, but gets no
	// Kubernetes (ichor-kube) token until then.
	MustChange bool
}

// UserDirectory looks up a user by name (a cached read is fine, up to
// 10 s). It returns ErrUserNotFound for an unknown user.
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

// ErrRefreshReused is returned by RefreshStore.Rotate when the presented token
// is not the family's current one (a replay: the family has been revoked) or
// the family is unknown (revoked, expired or pruned). Both mean the grant fails.
var ErrRefreshReused = errors.New("oidcissuer: refresh token reused or revoked")

// RefreshStore keeps one entry per refresh-token family so a refresh token is
// good once: every use rotates it, and a second use of a retired token revokes
// the whole family. It must be shared by all replicas.
type RefreshStore interface {
	// Start registers a new family whose current token has id jti, until exp.
	Start(ctx context.Context, user, family, jti string, exp time.Time) error
	// Rotate makes newJTI the current token of family if oldJTI is, else
	// revokes the family. Either failure returns ErrRefreshReused.
	Rotate(ctx context.Context, user, family, oldJTI, newJTI string) error
}

// Config configures an Issuer.
type Config struct {
	// Issuer is the external https URL (no path, no trailing slash): the "iss"
	// of every token and the discovery document's issuer.
	Issuer string
	// InternalJWKSURI is the jwks_uri advertised by InternalHandler (the
	// ClusterIP listener). Empty means the external one.
	InternalJWKSURI string
	// InternalJWKSFromHost, with InternalJWKSURI empty, advertises the jwks_uri on the host the
	// caller connected to, so the document follows the Service when its ClusterIP changes.
	InternalJWKSFromHost bool
	// UIRedirectURI is the exact redirect URI of the ichor-ui client,
	// <externalURL>/auth/callback.
	UIRedirectURI string

	TokenTTL        time.Duration // default 5m, 1m..1h
	RefreshLifetime time.Duration // default 12h, 1h..30d

	// Lifetimes, when set, is asked for the lifetimes at every issuance (spec.users).
	// A zero or out-of-range answer keeps TokenTTL / RefreshLifetime.
	Lifetimes func(ctx context.Context) (tokenTTL, refreshLifetime time.Duration)

	Keys      KeySource
	Users     UserDirectory
	Passwords PasswordVerifier
	// Refresh, when set, rotates refresh tokens with reuse detection. nil keeps
	// them stateless and unrotated (tests only).
	Refresh RefreshStore
	// TrustedProxies are the peers whose X-Forwarded-For is believed; the rightmost address that
	// is not itself trusted is then the client. Empty means the TCP peer is the client.
	TrustedProxies []netip.Prefix

	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Issuer is the OIDC provider. Create it with New.
type Issuer struct {
	cfg         Config
	failures    *Backoff // per (source, user)
	srcFailures *Backoff // per source
	ceiling     *userCeiling
	usedCodes   *jtiSet
	usedTickets *jtiSet
	// Per-source request rates: signing a ticket and answering a token request cost CPU.
	authzRate, loginRate, tokenRate *bucket
}

// ErrBusy is returned by PasswordVerifier.VerifyPassword when too many password checks are
// already queued; the login answers 503 with Retry-After instead of piling up memory-heavy work.
var ErrBusy = errors.New("oidcissuer: too many password checks in progress")

// clientIP is the source a request counts against: the TCP peer, or with TrustedProxies
// the client named by X-Forwarded-For.
func (i *Issuer) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || len(i.cfg.TrustedProxies) == 0 || !i.trusted(peer) {
		return host
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for n := len(hops) - 1; n >= 0; n-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[n]))
		if err != nil {
			return host
		}
		if !i.trusted(a) {
			return a.String()
		}
	}
	return host
}

func (i *Issuer) trusted(a netip.Addr) bool {
	for _, p := range i.cfg.TrustedProxies {
		if p.Contains(a.Unmap()) {
			return true
		}
	}
	return false
}

// lifetimes returns the ID token and refresh token lifetimes for this issuance.
func (i *Issuer) lifetimes(ctx context.Context) (tokenTTL, refreshLifetime time.Duration) {
	tokenTTL, refreshLifetime = i.cfg.TokenTTL, i.cfg.RefreshLifetime
	if i.cfg.Lifetimes == nil {
		return tokenTTL, refreshLifetime
	}

	t, r := i.cfg.Lifetimes(ctx)
	if t >= ichorauth.MinTokenTTL && t <= ichorauth.MaxTokenTTL {
		tokenTTL = t
	}

	if r >= ichorauth.MinRefreshLifetime && r <= ichorauth.MaxRefreshLifetime {
		refreshLifetime = r
	}

	return tokenTTL, refreshLifetime
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
		cfg.TokenTTL = ichorauth.DefaultTokenTTL
	}
	if cfg.RefreshLifetime == 0 {
		cfg.RefreshLifetime = ichorauth.DefaultRefreshLifetime
	}
	if cfg.TokenTTL < ichorauth.MinTokenTTL || cfg.TokenTTL > ichorauth.MaxTokenTTL {
		return nil, fmt.Errorf("oidcissuer: TokenTTL must be between %s and %s", ichorauth.MinTokenTTL, ichorauth.MaxTokenTTL)
	}
	if cfg.RefreshLifetime < ichorauth.MinRefreshLifetime || cfg.RefreshLifetime > ichorauth.MaxRefreshLifetime {
		return nil, fmt.Errorf("oidcissuer: RefreshLifetime must be between %s and %s", ichorauth.MinRefreshLifetime, ichorauth.MaxRefreshLifetime)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Issuer{
		cfg: cfg, failures: NewBackoff(), srcFailures: newSourceBackoff(), ceiling: &userCeiling{},
		usedCodes: newJTISet(), usedTickets: newJTISet(),
		authzRate: newBucket(20, 100), loginRate: newBucket(10, 50), tokenRate: newBucket(20, 100),
	}, nil
}

// Handler serves discovery, JWKS, authorize, login and token (port 8080 side).
func (i *Issuer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+endpointDiscovery, i.discovery(i.cfg.Issuer+endpointJWKS))
	mux.HandleFunc("GET "+endpointJWKS, i.jwks)
	mux.HandleFunc("GET "+endpointAuthorize, i.limit(i.authzRate, i.authorize))
	mux.HandleFunc("POST "+endpointLogin, i.limit(i.loginRate, i.login))
	mux.HandleFunc("POST "+endpointToken, i.limit(i.tokenRate, i.token))
	return http.MaxBytesHandler(mux, maxFormBytes)
}

// InternalHandler serves only discovery (with the internal jwks_uri) and the
// JWKS, for the kube-apiserver through discoveryURL (port 8444 side).
func (i *Issuer) InternalHandler() http.Handler {
	mux := http.NewServeMux()
	switch {
	case i.cfg.InternalJWKSURI != "":
		mux.HandleFunc("GET "+endpointDiscovery, i.discovery(i.cfg.InternalJWKSURI))
	case i.cfg.InternalJWKSFromHost:
		mux.HandleFunc("GET "+endpointDiscovery, func(w http.ResponseWriter, r *http.Request) {
			u := url.URL{Scheme: "https", Host: r.Host, Path: endpointJWKS}
			i.discovery(u.String())(w, r)
		})
	default:
		mux.HandleFunc("GET "+endpointDiscovery, i.discovery(i.cfg.Issuer+endpointJWKS))
	}
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
	set.Keys = i.cfg.Keys.Public() // public keys with use and alg already set
	w.Header().Set("Cache-Control", noCache)
	writeJSON(w, http.StatusOK, set)
}

// noCache is deliberate for discovery and the JWKS: the kube-apiserver's
// go-oidc (v2.5.0) keeps fetched keys until the response's own cache expiry
// and refetches on an unknown kid only after that, so any positive max-age
// would delay a rotation by that long. Without one it refetches immediately.
const jwkUseSig = "sig"

const noCache = "no-cache"

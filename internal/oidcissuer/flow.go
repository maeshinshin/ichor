package oidcissuer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	noStore(w)
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// redirectAllowed implements the fixed client rules. ichor-ui: exact match.
// ichor-cli: RFC 8252 section 7.3 loopback, the literal 127.0.0.1 (section
// 8.3), any port, path exactly /callback.
func (i *Issuer) redirectAllowed(client, raw string) bool {
	switch client {
	case ichorauth.ClientUI:
		return raw == i.cfg.UIRedirectURI
	case ichorauth.ClientCLI:
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
			u.Opaque != "" || u.Path != "/callback" || strings.Contains(raw, "#") || strings.Contains(raw, "?") {
			return false
		}
		host, port, err := net.SplitHostPort(u.Host)
		if err != nil || host != "127.0.0.1" {
			return false
		}
		n, err := strconv.Atoi(port)
		return err == nil && n >= 1 && n <= 65535 && port == strconv.Itoa(n)
	}
	return false
}

// audienceFor resolves the ID token audience a client may get. requested may
// be empty (the client default). The browser client never gets the Kubernetes
// audience, so a token in sessionStorage is not a Kubernetes credential.
func audienceFor(client, requested string) (string, bool) {
	switch client {
	case ichorauth.ClientUI:
		if requested == "" || requested == ichorauth.AudienceUI {
			return ichorauth.AudienceUI, true
		}
	case ichorauth.ClientCLI:
		if requested == "" {
			return ichorauth.AudienceKube, true
		}
		if requested == ichorauth.AudienceKube || requested == ichorauth.AudienceUI {
			return requested, true
		}
	}
	return "", false
}

func isB64URL(s string, minLen, maxLen int, extra string) bool {
	if len(s) < minLen || len(s) > maxLen {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' && !strings.ContainsRune(extra, c) {
			return false
		}
	}
	return true
}

func hasContentType(r *http.Request, want string) bool {
	mt, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	return strings.TrimSpace(mt) == want
}

func hasScope(scope, want string) bool { return slices.Contains(strings.Fields(scope), want) }

// authorize validates the request and hands the browser to the login route
// with a signed ticket (or, for Accept: application/json, answers the ticket
// as JSON). Until client and redirect_uri are proven, errors are shown, not
// redirected (RFC 6749 section 4.1.2.1); a JSON caller gets a 400 with the
// error code instead of a redirect.
func (i *Issuer) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for _, v := range q {
		if len(v) > 1 {
			http.Error(w, "repeated parameter", http.StatusBadRequest)
			return
		}
	}
	client, redirect := q.Get("client_id"), q.Get("redirect_uri")
	if !i.redirectAllowed(client, redirect) {
		http.Error(w, "unknown client or redirect_uri", http.StatusBadRequest)
		return
	}
	// The in-page flow (ADR 0094 amendment): the SPA sends "Accept: application/json" and gets the
	// ticket as JSON instead of a redirect to the login page.
	asJSON := strings.Contains(r.Header.Get("Accept"), "application/json")
	fail := func(code string) {
		if asJSON {
			oauthError(w, http.StatusBadRequest, code, "")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, i.callbackURL(redirect, "error", code, q.Get("state")), http.StatusFound)
	}
	aud, audOK := audienceFor(client, q.Get("audience"))
	switch {
	case q.Get("response_type") != "code":
		fail("unsupported_response_type")
	case !hasScope(q.Get("scope"), scopeOpenID):
		fail("invalid_scope")
	case q.Get("state") == "" || q.Get("nonce") == "":
		fail(errInvalidRequest)
	case q.Get("code_challenge_method") != codeChallengeMethodS || !isB64URL(q.Get("code_challenge"), 43, 43, ""):
		fail(errInvalidRequest) // plain is refused
	case !audOK:
		fail(errInvalidRequest)
	default:
		now := i.cfg.Now()
		ticket, err := i.sign(ichorauth.TypTicket, jwt.Claims{
			Issuer: i.cfg.Issuer, Audience: jwt.Audience{internalAudience}, ID: rand.Text(),
			IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(ichorauth.DefaultTicketTTL)),
		}, authRequest{
			ClientID: client, RedirectURI: redirect, Scope: q.Get("scope"), State: q.Get("state"),
			Nonce: q.Get("nonce"), CodeChallenge: q.Get("code_challenge"), TokenAudience: aud,
		})
		if err != nil {
			fail("server_error")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if asJSON {
			writeJSON(w, http.StatusOK, map[string]string{"ticket": ticket})
			return
		}
		http.Redirect(w, r, i.cfg.Issuer+"/login?ticket="+url.QueryEscape(ticket), http.StatusFound)
	}
}

// callbackURL is redirect with the result parameter (code or error), state when
// set, and the RFC 9207 iss.
func (i *Issuer) callbackURL(redirect, key, val, state string) string {
	u, _ := url.Parse(redirect)
	q := u.Query()
	q.Set(key, val)
	if state != "" {
		q.Set("state", state)
	}
	q.Set("iss", i.cfg.Issuer)
	u.RawQuery = q.Encode()
	return u.String()
}

type loginRequest struct {
	Ticket   string `json:"ticket"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// login takes the ticket plus credentials as JSON (the content type forces a
// CORS preflight, and no cookie is involved) and answers with the redirect URI
// carrying the authorization code.
func (i *Issuer) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !hasContentType(r, "application/json") {
		oauthError(w, http.StatusUnsupportedMediaType, errInvalidRequest, "JSON body required")
		return
	}
	var req loginRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBytes))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil {
		oauthError(w, http.StatusBadRequest, errInvalidRequest, "malformed body")
		return
	}
	var ar authRequest
	ticket, err := i.verify(req.Ticket, ichorauth.TypTicket, &ar)
	if err != nil {
		oauthError(w, http.StatusBadRequest, errInvalidRequest, "invalid or expired ticket")
		return
	}
	state := ar.State
	ar.State = ""
	now := i.cfg.Now()
	src, name := i.clientIP(r), req.Username[:min(len(req.Username), maxUserNameLen+1)]
	pairKey, srcKey := "p:"+src+"|"+name, "ip:"+src
	if d := max(i.failures.Wait(pairKey, now), i.srcFailures.Wait(srcKey, now)); d > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(d/time.Second)+1))
		oauthError(w, http.StatusTooManyRequests, "rate_limited", "too many failed logins")
		return
	}
	// Past the per-user ceiling every login of this user is slowed, never refused: an attacker
	// can cap the guess rate but cannot lock the account out.
	if d := i.ceiling.delay(name, now); d > 0 {
		select {
		case <-time.After(d):
		case <-r.Context().Done():
			return
		}
	}
	denied := func() {
		i.failures.Fail(pairKey, now)
		i.srcFailures.Fail(srcKey, now)
		i.ceiling.fail(name, now)
		oauthError(w, http.StatusUnauthorized, errAccessDenied, "invalid credentials")
	}
	if len(req.Username) == 0 || len(req.Username) > maxUserNameLen {
		denied()
		return
	}
	ok, err := i.cfg.Passwords.VerifyPassword(r.Context(), req.Username, req.Password)
	if errors.Is(err, ErrBusy) {
		w.Header().Set("Retry-After", "2")
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "too many sign-ins in progress")
		return
	}
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	if !ok {
		denied()
		return
	}
	user, active, err := i.activeUser(r, req.Username, time.Time{})
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	if !active {
		denied()
		return
	}
	i.failures.Reset(pairKey)
	// A ticket buys one successful login (failed attempts may retry with it): a replayed ticket
	// cannot be turned into a second code.
	if !i.usedTickets.use(ticket.ID, ticket.Expiry.Time(), now) {
		oauthError(w, http.StatusBadRequest, errInvalidRequest, "invalid or expired ticket")
		return
	}
	code, err := i.sign(ichorauth.TypCode, jwt.Claims{
		Issuer: i.cfg.Issuer, Subject: user.Name, Audience: jwt.Audience{internalAudience}, ID: rand.Text(),
		IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(ichorauth.DefaultCodeTTL)),
	}, ar)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect_uri": i.callbackURL(ar.RedirectURI, "code", code, state)})
}

// activeUser looks the user up and applies the disable and notBefore rules.
// issuedAt is the session start of a refresh token (zero for a fresh login).
func (i *Issuer) activeUser(r *http.Request, name string, issuedAt time.Time) (User, bool, error) {
	u, err := i.cfg.Users.LookupUser(r.Context(), name)
	if errors.Is(err, ErrUserNotFound) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	if u.Disabled || (!issuedAt.IsZero() && issuedAt.Unix() < u.NotBefore.Unix()) {
		return User{}, false, nil
	}
	return u, true, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
}

// token serves the authorization_code and refresh_token grants. Clients are
// public (no secret), so client_id is the only client check besides the
// redirect URI and PKCE.
func (i *Issuer) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if !hasContentType(r, "application/x-www-form-urlencoded") || r.ParseForm() != nil {
		oauthError(w, http.StatusBadRequest, errInvalidRequest, "form body required")
		return
	}
	f := r.PostForm
	for _, v := range f {
		if len(v) > 1 {
			oauthError(w, http.StatusBadRequest, errInvalidRequest, "repeated parameter")
			return
		}
	}
	client := f.Get("client_id")
	if client != ichorauth.ClientUI && client != ichorauth.ClientCLI {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return
	}
	switch f.Get("grant_type") {
	case "authorization_code":
		i.codeGrant(w, r, client)
	case "refresh_token":
		i.refreshGrant(w, r, client)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

func (i *Issuer) codeGrant(w http.ResponseWriter, r *http.Request, client string) {
	f := r.PostForm
	var ar authRequest
	std, err := i.verify(f.Get("code"), ichorauth.TypCode, &ar)
	if err != nil || ar.ClientID != client || ar.RedirectURI != f.Get("redirect_uri") {
		oauthError(w, http.StatusBadRequest, errInvalidGrant, "invalid code")
		return
	}
	// Single use per replica; consumed before the PKCE check so a stolen code
	// cannot be probed with guessed verifiers.
	if !i.usedCodes.use(std.ID, std.Expiry.Time(), i.cfg.Now()) {
		oauthError(w, http.StatusBadRequest, errInvalidGrant, "code already used")
		return
	}
	verifier := f.Get("code_verifier")
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if !isB64URL(verifier, 43, 128, ".~") || subtle.ConstantTimeCompare([]byte(want), []byte(ar.CodeChallenge)) != 1 {
		oauthError(w, http.StatusBadRequest, errInvalidGrant, "PKCE verification failed")
		return
	}
	user, active, err := i.activeUser(r, std.Subject, std.IssuedAt.Time())
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	if !active {
		oauthError(w, http.StatusBadRequest, errInvalidGrant, "user not allowed")
		return
	}
	i.respond(r.Context(), w, user, client, ar.TokenAudience, ar.Scope, ar.Nonce,
		refreshPlan{issue: hasScope(ar.Scope, scopeOffline), start: i.cfg.Now()})
}

func (i *Issuer) refreshGrant(w http.ResponseWriter, r *http.Request, client string) {
	f := r.PostForm
	var rc refreshClaims
	std, err := i.verify(f.Get("refresh_token"), ichorauth.TypRefresh, &rc)
	if err != nil || rc.ClientID != client {
		oauthError(w, http.StatusBadRequest, errInvalidGrant, "invalid refresh token")
		return
	}
	requested := f.Get("audience")
	if requested == "" {
		requested = rc.TokenAudience // the audience chosen at login
	}
	aud, ok := audienceFor(client, requested)
	if !ok {
		oauthError(w, http.StatusBadRequest, errInvalidRequest, "audience not allowed")
		return
	}
	user, active, err := i.activeUser(r, std.Subject, std.IssuedAt.Time())
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	if !active {
		oauthError(w, http.StatusBadRequest, errInvalidGrant, "user not allowed")
		return
	}
	plan := refreshPlan{}
	if i.cfg.Refresh != nil {
		plan = refreshPlan{issue: true, start: std.IssuedAt.Time(), expiry: std.Expiry.Time(), family: rc.Family, oldJTI: std.ID}
	}
	i.respond(r.Context(), w, user, client, aud, rc.Scope, "", plan)
}

// refreshPlan says what refresh token a response carries. issue false means
// none. A login starts a session at start; a rotation (family set) keeps the
// session's start and expiry and replaces oldJTI; a refresh of a token from
// before rotation existed (issue, no family) starts a family.
type refreshPlan struct {
	issue         bool
	start, expiry time.Time
	family        string
	oldJTI        string
}

// respond signs the ID token and, per plan, a refresh token. With a
// RefreshStore every refresh grant returns a new refresh token and retires the
// presented one; without one the client keeps its own.
func (i *Issuer) respond(ctx context.Context, w http.ResponseWriter, u User, client, aud, scope, nonce string, plan refreshPlan) {
	if u.MustChange && aud == ichorauth.AudienceKube {
		oauthError(w, http.StatusForbidden, errAccessDenied, "change your password first (sign in to the Ichor UI); no Kubernetes token is issued until then")
		return
	}
	now := i.cfg.Now()
	tokenTTL, refreshLifetime := i.lifetimes(ctx)
	groups := append([]string{}, u.Roles...) // never null in the token
	idTok, err := i.sign("JWT", jwt.Claims{
		Issuer: i.cfg.Issuer, Subject: u.Name, Audience: jwt.Audience{aud}, ID: rand.Text(),
		IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(tokenTTL)),
	}, idClaims{Groups: groups, Nonce: nonce})
	resp := tokenResponse{
		AccessToken: idTok, // no userinfo endpoint: the ID token doubles as the bearer
		TokenType:   "Bearer", ExpiresIn: int(tokenTTL / time.Second), IDToken: idTok, Scope: scope,
	}
	if err == nil && plan.issue {
		var rt string
		rt, err = i.refreshToken(ctx, u.Name, client, aud, scope, plan, refreshLifetime)
		if errors.Is(err, ErrRefreshReused) {
			oauthError(w, http.StatusBadRequest, errInvalidGrant, "invalid refresh token")
			return
		}
		resp.RefreshToken = rt
	}
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, resp)
}

// refreshToken signs the refresh token for plan and records it in the store.
func (i *Issuer) refreshToken(ctx context.Context, user, client, aud, scope string, plan refreshPlan, lifetime time.Duration) (string, error) {
	exp, jti := plan.expiry, rand.Text()
	if exp.IsZero() {
		exp = plan.start.Add(lifetime)
	}
	family := plan.family
	if st := i.cfg.Refresh; st != nil {
		var err error
		if plan.family == "" {
			family = rand.Text()
			err = st.Start(ctx, user, family, jti, exp)
		} else {
			err = st.Rotate(ctx, user, family, plan.oldJTI, jti)
		}
		if err != nil {
			return "", err
		}
	}
	return i.sign(ichorauth.TypRefresh, jwt.Claims{
		Issuer: i.cfg.Issuer, Subject: user, Audience: jwt.Audience{internalAudience}, ID: jti,
		IssuedAt: jwt.NewNumericDate(plan.start), Expiry: jwt.NewNumericDate(exp),
	}, refreshClaims{ClientID: client, Scope: scope, TokenAudience: aud, Family: family})
}

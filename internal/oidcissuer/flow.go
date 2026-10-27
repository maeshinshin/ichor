package oidcissuer

import (
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
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// redirectAllowed implements the fixed client rules. ichor-ui: exact match.
// ichor-cli: RFC 8252 section 7.3 loopback, the literal 127.0.0.1 (section
// 8.3), any port, path exactly /callback.
func (i *Issuer) redirectAllowed(client, raw string) bool {
	switch client {
	case ClientUI:
		return raw == i.cfg.UIRedirectURI
	case ClientCLI:
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
	case ClientUI:
		if requested == "" || requested == AudienceUI {
			return AudienceUI, true
		}
	case ClientCLI:
		if requested == "" {
			return AudienceKube, true
		}
		if requested == AudienceKube || requested == AudienceUI {
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

func hasScope(scope, want string) bool { return slices.Contains(strings.Fields(scope), want) }

// authorize validates the request and hands the browser to the login route
// with a signed ticket. Until client and redirect_uri are proven, errors are
// shown, not redirected (RFC 6749 section 4.1.2.1).
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
	fail := func(code string) { i.redirectResult(w, r, redirect, q.Get("state"), "", code) }
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
		id, err := randomID()
		if err != nil {
			fail("server_error")
			return
		}
		ticket, err := i.sign(typTicket, jwt.Claims{
			Issuer: i.cfg.Issuer, Audience: jwt.Audience{internalAudience}, ID: id,
			IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(TicketTTL)),
		}, authRequest{
			ClientID: client, RedirectURI: redirect, Scope: q.Get("scope"), State: q.Get("state"),
			Nonce: q.Get("nonce"), CodeChallenge: q.Get("code_challenge"), TokenAudience: aud,
		})
		if err != nil {
			fail("server_error")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, i.cfg.Issuer+i.cfg.LoginPath+"?ticket="+url.QueryEscape(ticket), http.StatusFound)
	}
}

func (i *Issuer) redirectResult(w http.ResponseWriter, r *http.Request, redirect, state, code, errCode string) {
	u, _ := url.Parse(redirect)
	q := u.Query()
	if errCode != "" {
		q.Set("error", errCode)
	} else {
		q.Set("code", code)
	}
	if state != "" {
		q.Set("state", state)
	}
	q.Set("iss", i.cfg.Issuer) // RFC 9207
	u.RawQuery = q.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u.String(), http.StatusFound)
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
	if mt, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); strings.TrimSpace(mt) != "application/json" {
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
	if _, err := i.verify(req.Ticket, typTicket, &ar); err != nil {
		oauthError(w, http.StatusBadRequest, errInvalidRequest, "invalid or expired ticket")
		return
	}
	state := ar.State
	ar.State = ""
	now := i.cfg.Now()
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	userKey, ipKey := "u:"+truncate(req.Username, maxUserNameLen+1), "ip:"+ip
	if d := max(i.failures.wait(userKey, now), i.failures.wait(ipKey, now)); d > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(d/time.Second)+1))
		oauthError(w, http.StatusTooManyRequests, "rate_limited", "too many failed logins")
		return
	}
	denied := func() {
		i.failures.fail(userKey, now)
		i.failures.fail(ipKey, now)
		oauthError(w, http.StatusUnauthorized, errAccessDenied, "invalid credentials")
	}
	if len(req.Username) == 0 || len(req.Username) > maxUserNameLen {
		denied()
		return
	}
	ok, err := i.cfg.Passwords.VerifyPassword(r.Context(), req.Username, req.Password)
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
	i.failures.reset(userKey)
	id, err := randomID()
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	code, err := i.sign(typCode, jwt.Claims{
		Issuer: i.cfg.Issuer, Subject: user.Name, Audience: jwt.Audience{internalAudience}, ID: id,
		IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(CodeTTL)),
	}, ar)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	u, _ := url.Parse(ar.RedirectURI)
	q := u.Query()
	q.Set("code", code)
	q.Set("iss", i.cfg.Issuer)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	writeJSON(w, http.StatusOK, map[string]string{"redirect_uri": u.String()})
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
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
	if mt, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); strings.TrimSpace(mt) != "application/x-www-form-urlencoded" || r.ParseForm() != nil {
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
	if client != ClientUI && client != ClientCLI {
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
	std, err := i.verify(f.Get("code"), typCode, &ar)
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
	i.respond(w, user, client, ar.TokenAudience, ar.Scope, ar.Nonce, hasScope(ar.Scope, scopeOffline), i.cfg.Now())
}

func (i *Issuer) refreshGrant(w http.ResponseWriter, r *http.Request, client string) {
	f := r.PostForm
	var rc refreshClaims
	std, err := i.verify(f.Get("refresh_token"), typRefresh, &rc)
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
	i.respond(w, user, client, aud, rc.Scope, "", false, time.Time{})
}

// respond signs the ID token and, for a new login (sessionStart non-zero and
// withRefresh), a refresh token. A refresh grant returns no new refresh token:
// they are not rotated, the client keeps its own.
func (i *Issuer) respond(w http.ResponseWriter, u User, client, aud, scope, nonce string, withRefresh bool, sessionStart time.Time) {
	now := i.cfg.Now()
	id, err := randomID()
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	groups := slices.Clone(u.Roles)
	if groups == nil {
		groups = []string{}
	}
	idTok, err := i.sign("JWT", jwt.Claims{
		Issuer: i.cfg.Issuer, Subject: u.Name, Audience: jwt.Audience{aud}, ID: id,
		IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(i.cfg.TokenTTL)),
	}, idClaims{Groups: groups, Nonce: nonce})
	resp := tokenResponse{
		AccessToken: idTok, // no userinfo endpoint: the ID token doubles as the bearer
		TokenType:   "Bearer", ExpiresIn: int(i.cfg.TokenTTL / time.Second), IDToken: idTok, Scope: scope,
	}
	if err == nil && withRefresh {
		rid, rerr := randomID()
		if rerr != nil {
			err = rerr
		} else {
			resp.RefreshToken, err = i.sign(typRefresh, jwt.Claims{
				Issuer: i.cfg.Issuer, Subject: u.Name, Audience: jwt.Audience{internalAudience}, ID: rid,
				IssuedAt: jwt.NewNumericDate(sessionStart), Expiry: jwt.NewNumericDate(sessionStart.Add(i.cfg.RefreshLifetime)),
			}, refreshClaims{ClientID: client, Scope: scope, TokenAudience: aud})
		}
	}
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, resp)
}

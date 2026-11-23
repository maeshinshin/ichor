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

// Package uisignin signs in to ichor-ui the way the CLI does, for the test harness commands: the issuer's
// authorization-code flow with PKCE (the JSON ticket variant the browser app uses, no browser).
package uisignin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

// Bearer adds the ID token to every request.
type Bearer struct {
	Base  http.RoundTripper
	Token string
}

func (b Bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.Token)
	return b.Base.RoundTrip(r)
}

// NewPassword is what a must-change password becomes (it satisfies ichorauth.ValidatePassword).
const NewPassword = "uisignin-changed-Password-0123456789"

// SignIn returns an ID token for ichor-ui. A user who must change the password can call only ChangeOwnPassword,
// so it changes it and signs in again.
func SignIn(ctx context.Context, hc *http.Client, base, user, password string) (string, error) {
	tok, err := idToken(ctx, hc, base, user, password)
	if err != nil {
		return "", err
	}
	users := ichorv1alpha1connect.NewUserServiceClient(&http.Client{Timeout: time.Minute,
		Transport: Bearer{Base: hc.Transport, Token: tok}}, base)
	who, err := users.WhoAmI(ctx, connect.NewRequest(&pb.WhoAmIRequest{}))
	if err != nil {
		return "", fmt.Errorf("whoami: %w", err)
	}
	if !who.Msg.GetMustChangePassword() {
		return tok, nil
	}
	_, err = users.ChangeOwnPassword(ctx,
		connect.NewRequest(&pb.ChangeOwnPasswordRequest{OldPassword: password, NewPassword: NewPassword}))
	if err != nil {
		return "", fmt.Errorf("changing the password: %w", err)
	}
	return idToken(ctx, hc, base, user, NewPassword)
}

// idToken runs the ichor-cli authorization-code flow against the issuer served by ichor-ui, for the ichor-ui
// audience: authorize (ticket as JSON), login, token.
func idToken(ctx context.Context, hc *http.Client, base, user, password string) (string, error) {
	v := make([]byte, 32)
	_, _ = rand.Read(v)
	verifier := base64.RawURLEncoding.EncodeToString(v)
	sum := sha256.Sum256([]byte(verifier))
	const redirect = "http://127.0.0.1:1/callback"
	q := url.Values{"response_type": {"code"}, "client_id": {"ichor-cli"}, "redirect_uri": {redirect},
		"scope": {"openid"}, "state": {"s"}, "nonce": {"n"}, "audience": {"ichor-ui"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/oidc/authorize?"+q.Encode(), nil)
	req.Header.Set("Accept", "application/json")
	var ticket struct{ Ticket string }
	if err := doJSON(hc, req, &ticket); err != nil {
		return "", fmt.Errorf("authorize: %w", err)
	}
	body, _ := json.Marshal(map[string]string{"ticket": ticket.Ticket, "username": user, "password": password})
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, base+"/oidc/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	var login struct {
		RedirectURI string `json:"redirect_uri"`
	}
	if err := doJSON(hc, req, &login); err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	u, err := url.Parse(login.RedirectURI)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {"ichor-cli"}, "redirect_uri": {redirect},
		"code": {u.Query().Get("code")}, "code_verifier": {verifier}}
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, base+"/oidc/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := doJSON(hc, req, &tok); err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	return tok.IDToken, nil
}

func doJSON(hc *http.Client, req *http.Request, out any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

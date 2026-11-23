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

// Command cniui calls ichor-ui's ClusterService.GetCluster and CniService for the hardware case D6 (the UI
// API on a CNI-unmanaged cluster). It reaches ichor-ui through the port-forward `ichor ui` opens (the certificate is
// self-signed, so verification is skipped) and prints each response, or the error with its Connect code, as one line.
//
//	cniui -url https://127.0.0.1:PORT -cluster NAME [-user NAME -password PW]
//
// ichor-ui requires sign-in, so with -user and -password cniui signs in the way the CLI does, through the issuer's
// authorization-code flow with PKCE (the JSON ticket variant the browser app uses, no browser), changes the
// password when it must be changed, and sends the ID token as a bearer token.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

func main() {
	url := flag.String("url", "https://127.0.0.1:8443", "ichor-ui base URL")
	cluster := flag.String("cluster", "", "TalosCluster name (namespace ichor-system)")
	user := flag.String("user", "", "Ichor user to sign in as")
	password := flag.String("password", "", "its password (a must-change one is changed to a fixed new one first)")
	flag.Parse()

	tlsCfg := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test harness only
	hc := &http.Client{Timeout: time.Minute, Transport: &http.Transport{TLSClientConfig: tlsCfg}}
	if *user != "" {
		tok, err := signIn(context.Background(), hc, *url, *user, *password)
		if err != nil {
			fmt.Printf("sign-in: ERROR %s\n", err)
			os.Exit(1)
		}
		hc.Transport = bearer{base: hc.Transport, token: tok}
	}
	ref := &pb.ObjectRef{Namespace: "ichor-system", Name: *cluster}
	ctx := context.Background()
	cl := ichorv1alpha1connect.NewClusterServiceClient(hc, *url)
	cni := ichorv1alpha1connect.NewCniServiceClient(hc, *url)

	show := func(name string, m proto.Message, err error) {
		if err != nil {
			fmt.Printf("%s: ERROR %s\n", name, err)
			return
		}
		fmt.Printf("%s: OK %s\n", name, protojson.MarshalOptions{}.Format(m))
	}
	r1, err := cl.GetCluster(ctx, connect.NewRequest(&pb.GetClusterRequest{Cluster: ref}))
	show("GetCluster", msg(r1), err)
	r2, err := cni.GetCiliumValues(ctx, connect.NewRequest(&pb.GetCiliumValuesRequest{Cluster: ref}))
	show("GetCiliumValues", msg(r2), err)
	r3, err := cni.SetCiliumValues(ctx, connect.NewRequest(&pb.SetCiliumValuesRequest{
		Cluster: ref, UserYaml: "debug:\n  enabled: true\n",
	}))
	show("SetCiliumValues", msg(r3), err)
	r4, err := cni.StartCniUpgrade(ctx, connect.NewRequest(&pb.StartCniUpgradeRequest{Cluster: ref}))
	show("StartCniUpgrade", msg(r4), err)
	_ = os.Stdout.Sync()
}

// msg returns the response message, or nil when the call failed (r is nil then).
func msg[T any, P interface {
	*T
	proto.Message
}](r *connect.Response[T]) proto.Message {
	if r == nil {
		return nil
	}
	return P(r.Msg)
}

// bearer adds the ID token to every request.
type bearer struct {
	base  http.RoundTripper
	token string
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// newPassword is what a must-change password becomes (it satisfies ichorauth.ValidatePassword).
const newPassword = "cniui-changed-Password-0123456789"

// signIn returns an ID token for ichor-ui. A user who must change the password can call only ChangeOwnPassword,
// so it changes it and signs in again.
func signIn(ctx context.Context, hc *http.Client, base, user, password string) (string, error) {
	tok, err := idToken(ctx, hc, base, user, password)
	if err != nil {
		return "", err
	}
	users := ichorv1alpha1connect.NewUserServiceClient(&http.Client{Timeout: time.Minute,
		Transport: bearer{base: hc.Transport, token: tok}}, base)
	who, err := users.WhoAmI(ctx, connect.NewRequest(&pb.WhoAmIRequest{}))
	if err != nil {
		return "", fmt.Errorf("whoami: %w", err)
	}
	if !who.Msg.GetMustChangePassword() {
		return tok, nil
	}
	_, err = users.ChangeOwnPassword(ctx,
		connect.NewRequest(&pb.ChangeOwnPasswordRequest{OldPassword: password, NewPassword: newPassword}))
	if err != nil {
		return "", fmt.Errorf("changing the password: %w", err)
	}
	return idToken(ctx, hc, base, user, newPassword)
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

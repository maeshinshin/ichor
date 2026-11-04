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

package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const authRevokeTimeout = 10 * time.Second

// runAuthLogout revokes the refresh token of every cached login of the issuer (only audience's when it
// is not empty) at the issuer, then deletes those logins. It returns how many it deleted. The revocation
// is best effort: the cache is deleted whatever the issuer answers.
func runAuthLogout(ctx context.Context, f *authFlow, audience string) (int, error) {
	f.revokeCached(ctx, audience)

	return deleteAuthCacheIssuer(f.Dir, f.Issuer, audience)
}

// revokeCached posts each matching login's refresh token to the issuer's RFC 7009 revocation endpoint, so
// a copy of the token stops working. Problems are warnings on stderr; a token is never printed.
func (f *authFlow) revokeCached(ctx context.Context, audience string) {
	entries, err := listAuthCache(f.Dir)
	if err != nil {
		return // deleteAuthCacheIssuer reports it
	}

	var todo []*authCacheEntry

	for _, e := range entries {
		if sameIssuer(e.Issuer, f.Issuer) && (audience == "" || e.Audience == audience) && e.RefreshToken != "" {
			todo = append(todo, e)
		}
	}

	if len(todo) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, authRevokeTimeout)
	defer cancel()

	endpoint, err := f.revocationEndpoint(ctx)
	if err != nil {
		f.logf("Warning: refresh tokens not revoked at the issuer: %v", err)

		return
	}

	if endpoint == "" {
		f.logf("Warning: refresh tokens not revoked: %s offers no revocation endpoint, so they stay valid until they expire", f.Issuer)

		return
	}

	for _, e := range todo {
		if err := f.postRevoke(ctx, endpoint, e); err != nil {
			f.logf("Warning: the refresh token for %q not revoked at the issuer: %v", e.Audience, err)
		}
	}
}

// revocationEndpoint is the issuer's revocation_endpoint, empty when it advertises none.
func (f *authFlow) revocationEndpoint(ctx context.Context) (string, error) {
	p, err := oidc.NewProvider(f.withClient(ctx), f.Issuer)
	if err != nil {
		return "", fmt.Errorf("discovering issuer %s: %w", f.Issuer, err)
	}

	var doc struct {
		RevocationEndpoint string `json:"revocation_endpoint"`
	}

	if err := p.Claims(&doc); err != nil {
		return "", err
	}

	return doc.RevocationEndpoint, nil
}

func (f *authFlow) postRevoke(ctx context.Context, endpoint string, e *authCacheEntry) error {
	form := url.Values{"token": {e.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {e.ClientID}}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	hc := f.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}

	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("posting to %s: %w", endpoint, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the issuer answered %s", resp.Status)
	}

	return nil
}

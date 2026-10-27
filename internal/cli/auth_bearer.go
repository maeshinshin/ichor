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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// authConfig is ichor-ui's GET /auth/config: the enabled
// flag plus the providers a client may log in to.
type authConfig struct {
	Enabled   bool `json:"enabled"`
	Providers []struct {
		ID     string `json:"id"`
		Issuer string `json:"issuer"`
	} `json:"providers"`
}

// issuers lists the issuers of cfg, Ichor's own first.
func (c authConfig) issuers() []string {
	var out []string

	for _, p := range c.Providers {
		if p.ID == "ichor" {
			out = append([]string{p.Issuer}, out...)
		} else {
			out = append(out, p.Issuer)
		}
	}

	return out
}

// fetchAuthConfig reads /auth/config. Anything but a 200 JSON answer -- an
// older backend, an unparseable body -- counts as off: the backend then rejects
// an unauthenticated call itself.
func fetchAuthConfig(ctx context.Context, hc *http.Client, base string) authConfig {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/auth/config", nil)
	if err != nil {
		return authConfig{}
	}

	resp, err := hc.Do(req)
	if err != nil {
		return authConfig{}
	}

	defer func() { _ = resp.Body.Close() }()

	var cfg authConfig
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&cfg) != nil {
		return authConfig{}
	}

	return cfg
}

// oidcLoginError is errOIDCBackend plus the command that fixes it.
type oidcLoginError struct{ issuer, cause string }

func (e *oidcLoginError) Unwrap() error { return errOIDCBackend }

func (e *oidcLoginError) Error() string {
	return errOIDCBackend.Error() + "; " + e.hint() + e.cause
}

func (e *oidcLoginError) hint() string {
	if e.issuer == "" {
		return "log in with `ichor auth login --issuer <ichor-ui url> --audience " + uiAudience + "`"
	}

	return "to use ichor-ui, run `ichor auth login --issuer " + e.issuer + " --audience " + uiAudience + "`"
}

// loginHint is the "how to use ichor-ui instead" part of err.
func loginHint(err error) string {
	if le, ok := errors.AsType[*oidcLoginError](err); ok {
		return le.hint() + le.cause
	}

	return err.Error()
}

// uiBearerToken finds an ID token for audience ichor-ui in the login cache
// for one of cfg's issuers and refreshes it when it is near expiry. The token
// is only ever returned, never printed.
func uiBearerToken(ctx context.Context, cfg authConfig, caFile string, stderr io.Writer) (string, error) {
	issuers := cfg.issuers()
	hint := ""

	if len(issuers) > 0 {
		hint = issuers[0]
	}

	dir, err := authCacheDir()
	if err != nil {
		return "", &oidcLoginError{issuer: hint, cause: fmt.Sprintf(" (%v)", err)}
	}

	entries, err := listAuthCache(dir)
	if err != nil {
		return "", &oidcLoginError{issuer: hint, cause: fmt.Sprintf(" (%v)", err)}
	}

	hc, err := authHTTPClient(caFile)
	if err != nil {
		return "", err
	}

	for _, iss := range issuers {
		for _, e := range entries {
			if e.Audience != uiAudience || !sameIssuer(e.Issuer, iss) {
				continue
			}

			f := &authFlow{Issuer: e.Issuer, ClientID: e.ClientID, Audience: uiAudience, Dir: dir, Stderr: stderr, HTTP: hc}

			got, err := f.ensure(ctx, false)
			if err != nil {
				return "", &oidcLoginError{issuer: iss, cause: fmt.Sprintf(" (%v)", err)}
			}

			return got.IDToken, nil
		}
	}

	return "", &oidcLoginError{issuer: hint}
}

// withBearer returns a client that sends token on every request.
func withBearer(hc *http.Client, token string) *http.Client {
	base := http.DefaultTransport
	if hc.Transport != nil {
		base = hc.Transport
	}

	return &http.Client{Transport: bearerTransport{base: base, token: token}, Timeout: hc.Timeout}
}

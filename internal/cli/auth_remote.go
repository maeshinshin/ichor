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
	"cmp"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

// caFileEnv names the environment variable behind --ca-file.
const caFileEnv = "ICHOR_CA_FILE"

// addCAFileFlag registers --ca-file (env ICHOR_CA_FILE) on a flag set. The
// CA is public: it only lets the CLI trust an issuer whose certificate a
// private CA signed.
func addCAFileFlag(fs *pflag.FlagSet) {
	fs.StringVar(&authOpts.caFile, "ca-file", os.Getenv(caFileEnv),
		"PEM file of the CA that signed the issuer's TLS certificate, added to the system roots (env "+caFileEnv+")")
}

// authHTTPClient returns a client that trusts the system roots plus the PEM
// bundle in caFile; nil (the default client) when caFile is empty.
func authHTTPClient(caFile string) (*http.Client, error) {
	if caFile == "" {
		return nil, nil
	}

	pemData, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("reading --ca-file: %w", err)
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}

	if !pool.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("--ca-file %s holds no PEM certificate", caFile)
	}

	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:errcheck,forcetypeassert // the default transport is an *http.Transport
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}

	return &http.Client{Transport: tr}, nil
}

// bearerTransport adds the ichor-ui token to every request. The token never
// leaves this process except to the issuer URL the user named.
type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)

	return b.base.RoundTrip(r)
}

// secureIssuer refuses to send a bearer token over plain HTTP, except to the
// loopback (a port-forward or a test server).
func secureIssuer(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("--issuer %q is not a URL", raw)
	}

	if u.Scheme == schemeHTTPS {
		return nil
	}

	if ip := net.ParseIP(u.Hostname()); u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
		return nil
	}

	return errors.New("--issuer must be an https URL (a bearer token is sent to it)")
}

// uiSession is a logged-in connection to ichor-ui at the issuer URL.
type uiSession struct {
	Users  ichorv1alpha1connect.UserServiceClient
	Entry  *authCacheEntry
	Issuer string
}

// dialUI is the seam tests replace.
var dialUI = dialUIOverIssuer

// dialUIOverIssuer reaches UserService the way a person without a kubeconfig
// can: ichor-ui is served at the issuer's own URL (the
// external URL is both), and the call carries an ID token for audience
// ichor-ui from the login cache, logging in through the browser when there
// is none. It does not go through a port-forward: the backend would refuse
// an unauthenticated tunnel once users are on, and `ichor auth kubeconfig`
// must work for someone who has no cluster access yet.
func dialUIOverIssuer(cmd *cobra.Command, interactive bool) (*uiSession, error) {
	ctx := cmd.Context()

	if err := secureIssuer(authOpts.issuer); err != nil {
		return nil, err
	}

	f, err := newAuthFlowFor(cmd, uiAudience, interactive)
	if err != nil {
		return nil, err
	}

	e, err := f.ensure(ctx, interactive)
	if err != nil {
		return nil, err
	}

	h := withBearer(cmp.Or(f.HTTP, &http.Client{}), e.IDToken)

	return &uiSession{Users: ichorv1alpha1connect.NewUserServiceClient(h, authOpts.issuer), Entry: e, Issuer: authOpts.issuer}, nil
}

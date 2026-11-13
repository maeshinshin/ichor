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
	"os"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientauthv1 "k8s.io/client-go/pkg/apis/clientauthentication/v1"
)

const (
	execInfoEnv       = "KUBERNETES_EXEC_INFO"
	execCredentialAPI = "client.authentication.k8s.io/v1"
	defaultAuthClient = "ichor-cli"

	// defaultAuthAudience is what a kubeconfig token is for; user and status
	// commands ask for ichor-ui themselves.
	defaultAuthAudience = "ichor-kube"
	uiAudience          = "ichor-ui"
)

var authOpts struct {
	issuer    string
	clientID  string
	audience  string
	noBrowser bool
	caFile    string
}

var authCmd = &cobra.Command{
	Use:         "auth",
	GroupID:     groupAccess,
	RunE:        groupRunE,
	Annotations: map[string]string{groupingKey: "true"},
	Short:       "Log in to Ichor's OIDC issuer and act as a kubectl credential plugin",
	Example: `  ichor auth login --issuer https://ichor.example.com
  ichor auth whoami --issuer https://ichor.example.com`,
	Long: `Log in to Ichor's own OIDC issuer with the browser, and print
short-lived ID tokens for kubectl. The login cache lives in ~/.ichor/auth
(directory 0700, files 0600) and holds a refresh token; the kubeconfig itself
holds no secret.`,
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in through the browser (authorization code + PKCE, loopback redirect); the default audience ichor-kube is the login kubectl needs when it has no terminal",
	Example: `  ichor auth login --issuer https://ichor.example.com
  ichor auth login --issuer https://ichor.example.com --no-browser`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f, err := newAuthFlow(cmd, !authOpts.noBrowser)
		if err != nil {
			return err
		}

		if err := f.loginAndSave(cmd.Context()); err != nil {
			return err
		}

		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Logged in to %s as client %s.\n", f.Issuer, f.ClientID)

		return nil
	},
}

var authTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Print an ExecCredential for kubectl (client-go exec plugin)",
	Long: `Prints a client.authentication.k8s.io/v1 ExecCredential on stdout, and only
that. It uses the cached ID token while it has 30 seconds left, then the cached
refresh token, and starts a browser login only when kubectl says the session is
interactive (KUBERNETES_EXEC_INFO).`,
	Example: `  ichor auth token --issuer https://ichor.example.com`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f, err := newAuthFlow(cmd, true)
		if err != nil {
			return err
		}

		return runAuthToken(cmd.Context(), f, os.Getenv(execInfoEnv), cmd.OutOrStdout())
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Revoke and delete every cached login for the issuer (all audiences), or only --audience's",
	Example: `  ichor auth logout --issuer https://ichor.example.com
  ichor auth logout --issuer https://ichor.example.com --audience ichor-ui`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f, err := newAuthFlow(cmd, false)
		if err != nil {
			return err
		}

		audience := ""
		if cmd.Flags().Changed("audience") {
			audience = authOpts.audience
		}

		if _, err := runAuthLogout(cmd.Context(), f, audience); err != nil {
			return err
		}

		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Logged out of %s.\n", f.Issuer)

		return nil
	},
}

// addAuthFlags registers the issuer flags of `ichor auth` (and of `ichor create kubeconfig`, which signs in the same way).
func addAuthFlags(fs *pflag.FlagSet) {
	fs.StringVar(&authOpts.issuer, "issuer", os.Getenv("ICHOR_ISSUER"), "Ichor OIDC issuer URL, the external https URL of ichor-ui (required; env ICHOR_ISSUER)")
	fs.StringVar(&authOpts.clientID, "client-id", defaultAuthClient, "OIDC client id")
	fs.StringVar(&authOpts.audience, "audience", defaultAuthAudience, "token audience: ichor-kube (the kube-apiserver, default; `ichor auth login` once lets kubectl run without a terminal) or ichor-ui (ichor-ui itself)")
	addCAFileFlag(fs)
}

func init() {
	addAuthFlags(authCmd.PersistentFlags())
	authLoginCmd.Flags().BoolVar(&authOpts.noBrowser, "no-browser", false, "print the login URL instead of opening a browser")

	authCmd.AddCommand(authLoginCmd, authTokenCmd, authLogoutCmd, authWhoamiCmd)
	rootCmd.AddCommand(authCmd)
}

func newAuthFlow(cmd *cobra.Command, openBrowserToo bool) (*authFlow, error) {
	return newAuthFlowFor(cmd, authOpts.audience, openBrowserToo)
}

// newAuthFlowFor is newAuthFlow for a token of another audience (the user and
// status commands present ichor-ui's, not the kube-apiserver's).
func newAuthFlowFor(cmd *cobra.Command, audience string, openBrowserToo bool) (*authFlow, error) {
	if authOpts.issuer == "" {
		return nil, errors.New("--issuer is required")
	}

	dir, err := authCacheDir()
	if err != nil {
		return nil, err
	}

	hc, err := authHTTPClient(authOpts.caFile)
	if err != nil {
		return nil, err
	}

	f := &authFlow{
		Issuer: authOpts.issuer, ClientID: authOpts.clientID, Audience: audience,
		Dir: dir, Stderr: cmd.ErrOrStderr(), HTTP: hc,
	}
	if openBrowserToo {
		f.OpenBrowser = browser.OpenURL
	}

	return f, nil
}

// execInteractive reads kubectl's KUBERNETES_EXEC_INFO. An unset variable
// means the plugin was not started by client-go, which is not a session that
// may open a browser; use `ichor auth login` for that.
func execInteractive(info string) (bool, error) {
	if info == "" {
		return false, nil
	}

	var in clientauthv1.ExecCredential
	if err := json.Unmarshal([]byte(info), &in); err != nil {
		return false, fmt.Errorf("parsing %s: %w", execInfoEnv, err)
	}

	if in.APIVersion != "" && in.APIVersion != execCredentialAPI {
		return false, fmt.Errorf("kubectl asked for %s; only %s is supported (set the kubeconfig exec apiVersion to it)", in.APIVersion, execCredentialAPI)
	}

	return in.Spec.Interactive, nil
}

// runAuthToken writes the ExecCredential, the only thing allowed on out.
func runAuthToken(ctx context.Context, f *authFlow, execInfo string, out io.Writer) error {
	interactive, err := execInteractive(execInfo)
	if err != nil {
		return err
	}

	e, err := f.ensure(ctx, interactive)
	if err != nil {
		return err
	}

	exp := metav1.NewTime(e.Expiry)

	cred := clientauthv1.ExecCredential{
		Status: &clientauthv1.ExecCredentialStatus{Token: e.IDToken, ExpirationTimestamp: &exp},
	}
	cred.APIVersion, cred.Kind = execCredentialAPI, "ExecCredential"

	return json.NewEncoder(out).Encode(cred)
}

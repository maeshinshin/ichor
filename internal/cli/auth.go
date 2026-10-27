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

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientauthv1 "k8s.io/client-go/pkg/apis/clientauthentication/v1"
)

const (
	execInfoEnv       = "KUBERNETES_EXEC_INFO"
	execCredentialAPI = "client.authentication.k8s.io/v1"
	defaultAuthClient = "ichor-cli"
)

var authOpts struct {
	issuer    string
	clientID  string
	audience  string
	noBrowser bool
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Log in to Ichor's OIDC issuer and act as a kubectl credential plugin",
	Long: `Log in to Ichor's own OIDC issuer (ADR 0094) with the browser, and print
short-lived ID tokens for kubectl. The login cache lives in ~/.ichor/auth
(directory 0700, files 0600) and holds a refresh token; the kubeconfig itself
holds no secret.`,
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in through the browser (authorization code + PKCE, loopback redirect)",
	Args:  cobra.NoArgs,
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
	Args: cobra.NoArgs,
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
	Short: "Delete the cached login for the issuer",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f, err := newAuthFlow(cmd, false)
		if err != nil {
			return err
		}

		if err := deleteAuthCache(f.Dir, f.key()); err != nil {
			return err
		}

		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Logged out of %s.\n", f.Issuer)

		return nil
	},
}

func init() {
	pf := authCmd.PersistentFlags()
	pf.StringVar(&authOpts.issuer, "issuer", "", "Ichor OIDC issuer URL (required)")
	pf.StringVar(&authOpts.clientID, "client-id", defaultAuthClient, "OIDC client id")
	pf.StringVar(&authOpts.audience, "audience", "", "token audience (for example ichor-kube or ichor-ui); default: the client id")
	authLoginCmd.Flags().BoolVar(&authOpts.noBrowser, "no-browser", false, "print the login URL instead of opening a browser")

	authCmd.AddCommand(authLoginCmd, authTokenCmd, authLogoutCmd)
	rootCmd.AddCommand(authCmd)
}

func newAuthFlow(cmd *cobra.Command, openBrowserToo bool) (*authFlow, error) {
	if authOpts.issuer == "" {
		return nil, errors.New("--issuer is required")
	}

	dir, err := authCacheDir()
	if err != nil {
		return nil, err
	}

	f := &authFlow{
		Issuer: authOpts.issuer, ClientID: authOpts.clientID, Audience: authOpts.audience,
		Dir: dir, Stderr: cmd.ErrOrStderr(),
	}
	if openBrowserToo {
		f.OpenBrowser = openBrowser
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

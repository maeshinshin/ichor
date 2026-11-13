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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var authKubeconfigOut string

var authWhoamiCmd = &cobra.Command{
	Use:     "whoami",
	Short:   "Show who you are signed in as and when the cached tokens expire (prints no token)",
	Example: `  ichor auth whoami --issuer https://ichor.example.com`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		sess, err := dialUI(cmd, false)
		if err != nil {
			return fmt.Errorf("not signed in to %s (run `ichor auth login --issuer %s --audience %s`): %w",
				authOpts.issuer, authOpts.issuer, uiAudience, err)
		}

		return runAuthStatus(cmd.Context(), sess, cmd.OutOrStdout())
	},
}

var createKubeconfigCmd = &cobra.Command{
	Use:   "kubeconfig",
	Short: "Download your own kubeconfig (it holds no secret: it runs `ichor auth token`)",
	Long: `Asks ichor-ui for a kubeconfig for the signed-in user and writes it (mode 0600).
The file has the API server address, the cluster CA certificate and an exec
block that runs ` + "`ichor auth token`" + `; there is nothing secret in it. With --ca-file the
exec block carries the same --ca-file so kubectl trusts the issuer's private CA.`,
	Example: `  ichor create kubeconfig --issuer https://ichor.example.com
  ichor create kubeconfig --issuer https://ichor.example.com --out - | kubectl --kubeconfig /dev/stdin get nodes`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		sess, err := dialUI(cmd, true)
		if err != nil {
			return err
		}

		return runAuthKubeconfig(cmd.Context(), sess, authKubeconfigOut, authOpts.caFile, cmd.OutOrStdout(), cmd.ErrOrStderr())
	},
}

func init() {
	addAuthFlags(createKubeconfigCmd.Flags())
	createCmd.AddCommand(createKubeconfigCmd)
	createKubeconfigCmd.Flags().StringVar(&authKubeconfigOut, "out", "", `file to write ("-" for stdout); default: the file name ichor-ui suggests, in the current directory`)
}

func runAuthStatus(ctx context.Context, sess *uiSession, out io.Writer) error {
	resp, err := sess.Users.WhoAmI(ctx, connect.NewRequest(&pb.WhoAmIRequest{}))
	if err != nil {
		return err
	}

	w := resp.Msg

	_, _ = fmt.Fprintf(out, "issuer:   %s\nuser:     %s\n", sess.Issuer, w.GetUsername())
	if w.GetDisplayName() != "" {
		_, _ = fmt.Fprintf(out, "name:     %s\n", w.GetDisplayName())
	}

	_, _ = fmt.Fprintf(out, "groups:   %s\nsource:   %s\n", strings.Join(w.GetGroups(), ", "), w.GetSource())

	if w.GetMustChangePassword() {
		_, _ = fmt.Fprintln(out, "password: must be changed at the next sign-in in the browser")
	}

	_, _ = fmt.Fprintf(out, "ichor-ui token expires: %s\n", sess.Entry.Expiry.Local().Format(time.RFC3339))

	if dir, err := authCacheDir(); err == nil {
		kube, _ := loadAuthCache(dir, authCacheKey(sess.Issuer, authOpts.clientID, defaultAuthAudience))

		switch kube {
		case nil:
			_, _ = fmt.Fprintln(out, "kubectl token: none cached (run `ichor auth login --audience ichor-kube` once, or use kubectl from a terminal, where the first call signs in)")
		default:
			_, _ = fmt.Fprintf(out, "kubectl token expires: %s (refreshable: %t)\n", kube.Expiry.Local().Format(time.RFC3339), kube.RefreshToken != "")
		}
	}

	return nil
}

func runAuthKubeconfig(ctx context.Context, sess *uiSession, outPath, caFile string, stdout, stderr io.Writer) error {
	if caFile != "" {
		abs, err := filepath.Abs(caFile)
		if err != nil {
			return err
		}

		caFile = abs
	}

	resp, err := sess.Users.GetMyKubeconfig(ctx, connect.NewRequest(&pb.GetMyKubeconfigRequest{CaFile: caFile}))
	if err != nil {
		return err
	}

	raw := []byte(resp.Msg.GetKubeconfig())

	if outPath == "-" {
		_, err = stdout.Write(raw)

		return err
	}

	if outPath == "" {
		outPath = filepath.Base(resp.Msg.GetFilename())
	}

	if _, err := os.Stat(outPath); err == nil {
		return fmt.Errorf("%s already exists; pass --out with another path, or - for stdout", outPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	if err := writeFileAtomic(outPath, raw); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stderr, "Wrote %s (mode 0600). Use it with: KUBECONFIG=%s kubectl get nodes\n", outPath, outPath)

	return nil
}

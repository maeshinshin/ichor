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
	"os"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/credentials"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

var clusterCredentialsOpts struct {
	Bundle         string
	KubeconfigOut  string
	TalosconfigOut string
	TalosEndpoints []string
	Operation      string
	Acknowledge    bool
	Force          bool
	TTL            time.Duration
	ReadOnly       bool
}

// staleBundleWarning is printed when a --bundle file's CAs are not the cluster's current ones: the
// files written from it trust CAs that no node accepts any more.
const staleBundleWarning = "the bundle's CAs differ from the secrets bundle Ichor stores for this cluster; " +
	"the files will not work unless that stored bundle is the out-of-date one"

// clusterCredentialsCmd implements `ichor cluster credentials` (ADR 0091): write a kubeconfig and a
// talosconfig for the operator, from the cluster's secrets bundle or, offline, from a bundle file.
var clusterCredentialsCmd = &cobra.Command{
	Use:   "credentials NAME",
	Short: "Write a kubeconfig and a talosconfig for the cluster's admin, from its secrets bundle",
	Long: `credentials writes two files and never prints their contents (mode 0600):

  a kubeconfig whose CA bundle holds the Kubernetes CA(s) and whose client certificate is signed by
  the newest Kubernetes CA, and
  a talosconfig whose CA bundle holds the Talos API CA(s) and whose os:admin client certificate is
  signed by the newest Talos API CA.

By default the client certificates live a year, which is also the maximum; --ttl asks for fewer
hours and --read-only makes the talosconfig os:reader instead of os:admin, for access that cannot
change a node.

The server and context names are copied from your current kubeconfig, the Talos endpoints from the
cluster's control-plane machines (or --talos-endpoints, or your current talosconfig). The files are
written next to your current kubeconfig and talosconfig with the suffix ` + credentialsSuffix + `
(never over them), unless --kubeconfig-out and --talosconfig-out say otherwise.

By default the CAs are read from the cluster's secrets bundle Secret. While a CA rotation is under way
the files trust the old and the new CAs and are signed by the new one, which every node accepts from
the moment the rotation waits for credentials: they keep working after the old CA is dropped.

With --bundle FILE (the output of 'ichor cluster bundle-export') nothing is read from the cluster, so
an operator with a current bundle can always get back in. A bundle saved before a rotation names CAs
that no node accepts any more.

With --acknowledge the command also tells a waiting CA rotation that you have the credentials, after
checking that the new kubeconfig authenticates to the cluster: the rotation does not continue without
it. 'ichor cluster rotate-ca' does all of this itself.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		o := clusterCredentialsOpts

		if o.TTL < 0 || o.TTL > credentials.MaxLifetime {
			return fmt.Errorf("--ttl must be between 0 and %s", credentials.MaxLifetime)
		}

		req := credentialRequest{
			Cluster: args[0], Namespace: apiOpts.Namespace, IchorNamespace: apiOpts.IchorNamespace,
			BundleFile: o.Bundle, KubeconfigIn: kubeconfigPath, TalosconfigIn: talosconfigPath,
			KubeconfigOut: o.KubeconfigOut, TalosconfigOut: o.TalosconfigOut, TalosEndpoints: o.TalosEndpoints,
			TTL: o.TTL, ReadOnly: o.ReadOnly,
		}

		var k8s ctrlclient.Client

		if o.Bundle == "" || o.Acknowledge {
			var err error
			if k8s, err = newKubeClient(kubeconfigPath); err != nil {
				return err
			}
		}

		stale := false

		if o.Bundle != "" {
			// Best effort: without a cluster to compare with, the bundle is taken as it is.
			var cluster ctrlclient.Client
			if kc, err := newKubeClient(kubeconfigPath); err == nil {
				cluster = kc
			}

			var err error

			if stale, err = checkBundleCurrent(cmd.Context(), cluster, cmd.ErrOrStderr(), o.Bundle, req, o.Force); err != nil {
				return err
			}
		}

		files, err := writeCredentials(cmd.Context(), k8s, req)
		if err != nil {
			return err
		}

		files.ForcedStaleBundle = stale

		reportCredentials(cmd.ErrOrStderr(), files)

		if !o.Acknowledge {
			return nil
		}

		return acknowledgeWithChecks(cmd.Context(), k8s, defaultHandoffChecks(), req, files, o.Operation, cmd.ErrOrStderr())
	},
}

// handoffChecks are the checks made on the written files before the controller is told it may go on.
// Both are seams: the real ones need a cluster.
type handoffChecks struct {
	// Kube must succeed: it proves the new kubeconfig authenticates to the Kubernetes API, so the
	// operator is not stranded. Failing it blocks the acknowledgement.
	Kube func(ctx context.Context, kubeconfigPath string) error

	// Talos is best effort (the operator's machine may not reach the nodes directly); a failure is
	// reported but does not block. nil skips it.
	Talos func(ctx context.Context, talosconfigPath string) error

	// Reconnect, when set, gives the client the acknowledgement retries through after a transient
	// error, built from the new kubeconfig. nil keeps the client it was given.
	Reconnect func(kubeconfigPath string) (ctrlclient.Client, error)
}

func defaultHandoffChecks() handoffChecks {
	return handoffChecks{Kube: verifyKubeconfig, Talos: verifyTalosconfig, Reconnect: newKubeClient}
}

// checkBundleCurrent warns when the bundle file holds CAs the cluster does not have any more, and
// refuses to write credentials from it unless force is set. It needs the cluster to compare with:
// with k8s nil, or when the cluster cannot be read, there is nothing to compare and it passes.
func checkBundleCurrent(ctx context.Context, k8s ctrlclient.Client, errw io.Writer, bundleFile string,
	req credentialRequest, force bool) (stale bool, err error) {
	if k8s == nil {
		return false, nil
	}

	bundle, err := loadBundleFile(bundleFile)
	if err != nil {
		return false, err
	}

	dctx, cancel := context.WithTimeout(ctx, apiConnectTimeout)
	defer cancel()

	current, known := bundleMatchesCluster(dctx, k8s, req, bundle)
	if !known || current {
		return false, nil
	}

	_, _ = fmt.Fprintln(errw, "warning: "+staleBundleWarning)

	if force {
		return true, nil
	}

	return false, errors.New("refusing to write credentials from " + bundleFile + ": its CAs differ from the stored secrets bundle; " +
		"pass --force if the file is the current bundle (for example after a restore)")
}

// bundleMatchesCluster reports whether bundle carries the CAs the cluster holds now: the live secrets
// bundle's, or during a rotation also the new ones. known is false when the cluster could not be read.
func bundleMatchesCluster(ctx context.Context, k8s ctrlclient.Client, req credentialRequest,
	bundle *secrets.Bundle) (current, known bool) {
	var cluster ichorv1alpha1.TalosCluster
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Cluster}, &cluster); err != nil {
		return false, false
	}

	live, err := secretsbundle.Load(ctx, k8s, req.IchorNamespace, cluster.Spec.SecretsRef.Name)
	if err != nil {
		return false, false
	}

	rot, err := secretsbundle.LoadRotation(ctx, k8s, req.IchorNamespace, cluster.Spec.SecretsRef.Name)
	if err != nil {
		return false, false
	}

	candidates := []*secrets.Bundle{live}

	if rot != nil && rot.Validate() == nil && live.Certs != nil {
		certs := *live.Certs
		if rot.Talos != nil {
			certs.OS = rot.Talos
		}

		if rot.Kubernetes != nil {
			certs.K8s = rot.Kubernetes
		}

		candidates = append(candidates, &secrets.Bundle{Certs: &certs})
	}

	want, err := secretsbundle.BundleFingerprint(bundle)
	if err != nil {
		return false, false
	}

	for _, c := range candidates {
		got, err := secretsbundle.BundleFingerprint(c)
		if err != nil {
			return false, false
		}

		if got == want {
			return true, true
		}
	}

	return false, true
}

// verifyKubeconfig asks the API server who the kubeconfig's credentials are.
func verifyKubeconfig(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	return credentials.VerifyKubeconfig(ctx, data)
}

// verifyTalosconfig calls Version on the first endpoint with the new talosconfig.
func verifyTalosconfig(ctx context.Context, path string) error {
	dctx, cancel := context.WithTimeout(ctx, apiConnectTimeout)
	defer cancel()

	c, err := client.New(dctx, client.WithConfigFromFile(path))
	if err != nil {
		return err
	}

	defer func() { _ = c.Close() }()

	_, err = c.Version(dctx)

	return err
}

// acknowledgeWithChecks verifies the new files and then sets the hand-off annotation.
func acknowledgeWithChecks(
	ctx context.Context, k8s ctrlclient.Client, checks handoffChecks, req credentialRequest, files *credentialFiles, operation string,
	errw io.Writer,
) error {
	if !files.Dual {
		return fmt.Errorf("the cluster's secrets bundle holds no rotation in progress, so there is nothing to acknowledge; " +
			"run this while a CA rotation waits for credentials")
	}

	if err := retryHandoff(ctx, errw, "checking the new kubeconfig", func() error { return checks.Kube(ctx, files.Kubeconfig) }); err != nil {
		return fmt.Errorf("the new kubeconfig %s does not authenticate to the cluster (%w); NOT acknowledging, so the rotation keeps "+
			"waiting and your current credentials keep working", files.Kubeconfig, err)
	}

	_, _ = fmt.Fprintf(errw, "checked: the new kubeconfig %s authenticates to the cluster\n", files.Kubeconfig)

	if checks.Talos != nil {
		if err := checks.Talos(ctx, files.Talosconfig); err != nil {
			_, _ = fmt.Fprintf(errw, "ichor: warning: could not check the new talosconfig %s against a node from this machine (%v); "+
				"continuing, since the kubeconfig works\n", files.Talosconfig, err)
		}
	}

	var redial func() (ctrlclient.Client, error)
	if checks.Reconnect != nil {
		redial = func() (ctrlclient.Client, error) { return checks.Reconnect(files.Kubeconfig) }
	}

	name, err := acknowledgeHandoff(ctx, k8s, req.Namespace, req.Cluster, operation, errw, redial)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(errw, "Acknowledged on operation %s: the rotation goes on.\n", name)

	return nil
}

// reportCredentials says where the files are and what to do with them. It never prints their content.
func reportCredentials(w io.Writer, files *credentialFiles) {
	trusts := "the cluster's CA"
	if files.ForcedStaleBundle {
		trusts = "the bundle's CAs, which differ from the stored ones"
	}

	if files.Dual {
		trusts = "the old and the new CAs"
	}

	_, _ = fmt.Fprintf(w, "Wrote (mode 0600, content not printed; they trust %s):\n  kubeconfig:  %s\n  talosconfig: %s\n"+
		"Use them with: KUBECONFIG=%s TALOSCONFIG=%s\n",
		trusts, files.Kubeconfig, files.Talosconfig, files.Kubeconfig, files.Talosconfig)
}

// handoffDeps are runRotateCAHandoff's seams.
type handoffDeps struct {
	K8s    ctrlclient.Client
	Checks handoffChecks
	Poll   time.Duration

	// Progress, when set, is shared with the --follow that comes after, so no stage line prints twice.
	Progress *stageProgress
}

func init() {
	clusterCmd.AddCommand(clusterCredentialsCmd)

	f := clusterCredentialsCmd.Flags()
	f.StringVar(&clusterCredentialsOpts.Bundle, "bundle", "",
		"read the CAs from this secrets bundle file (bundle-export's output) instead of the cluster; works offline")
	f.StringVar(&clusterCredentialsOpts.KubeconfigOut, "kubeconfig-out", "",
		"where to write the kubeconfig (default: your current kubeconfig's path plus "+credentialsSuffix+")")
	f.StringVar(&clusterCredentialsOpts.TalosconfigOut, "talosconfig-out", "",
		"where to write the talosconfig (default: your current talosconfig's path plus "+credentialsSuffix+")")
	f.StringSliceVar(&clusterCredentialsOpts.TalosEndpoints, "talos-endpoints", nil,
		"the Talos API endpoints for the talosconfig (default: the cluster's control-plane machines, else your current talosconfig's)")
	f.DurationVar(&clusterCredentialsOpts.TTL, "ttl", 0,
		"how long the client certificates in the files are valid; the default and the maximum is "+credentials.MaxLifetime.String()+
			" (a rotation hand-off must outlive the rotation)")
	f.BoolVar(&clusterCredentialsOpts.ReadOnly, "read-only", false,
		"make the talosconfig an os:reader client (read-only Talos API access) instead of os:admin")
	f.BoolVar(&clusterCredentialsOpts.Acknowledge, "acknowledge", false,
		"after checking the new kubeconfig works, tell the CA rotation that is waiting for credentials to continue")
	f.StringVar(&clusterCredentialsOpts.Operation, "operation", "", "with --acknowledge: the CA rotation operation to acknowledge")
	f.BoolVar(&clusterCredentialsOpts.Force, "force", false,
		"with --bundle: write the files even though the bundle's CAs differ from the stored secrets bundle; pass it when you "+
			"know the file is the current bundle (for example after a restore)")
}

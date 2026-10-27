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
	"io"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var clusterCredentialsOpts struct {
	Bundle         string
	KubeconfigOut  string
	TalosconfigOut string
	TalosEndpoints []string
	Operation      string
	Acknowledge    bool
}

// clusterCredentialsCmd implements `ichor cluster credentials`: write a kubeconfig and a
// talosconfig for the operator, from the cluster's secrets bundle or, offline, from a bundle file.
var clusterCredentialsCmd = &cobra.Command{
	Use:   "credentials NAME",
	Short: "Write a kubeconfig and a talosconfig for the cluster's admin, from its secrets bundle",
	Long: `credentials writes two files and never prints their contents (mode 0600):

  a kubeconfig whose CA bundle holds the Kubernetes CA(s) and whose client certificate is signed by
  the newest Kubernetes CA, and
  a talosconfig whose CA bundle holds the Talos API CA(s) and whose os:admin client certificate is
  signed by the newest Talos API CA.

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

		req := credentialRequest{
			Cluster: args[0], Namespace: apiOpts.Namespace, IchorNamespace: apiOpts.IchorNamespace,
			BundleFile: o.Bundle, KubeconfigIn: kubeconfigPath, TalosconfigIn: talosconfigPath,
			KubeconfigOut: o.KubeconfigOut, TalosconfigOut: o.TalosconfigOut, TalosEndpoints: o.TalosEndpoints,
		}

		var k8s ctrlclient.Client

		if o.Bundle == "" || o.Acknowledge {
			var err error
			if k8s, err = newKubeClient(kubeconfigPath); err != nil {
				return err
			}
		}

		files, err := writeCredentials(cmd.Context(), k8s, req)
		if err != nil {
			return err
		}

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
}

func defaultHandoffChecks() handoffChecks {
	return handoffChecks{Kube: verifyKubeconfig, Talos: verifyTalosconfig}
}

// verifyKubeconfig asks the API server who the kubeconfig's credentials are.
func verifyKubeconfig(ctx context.Context, path string) error {
	cfg, err := loadKubeconfig(path)
	if err != nil {
		return err
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}

	_, err = selfSubjectName(ctx, cs.AuthenticationV1().SelfSubjectReviews())

	return err
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

	if err := checks.Kube(ctx, files.Kubeconfig); err != nil {
		return fmt.Errorf("the new kubeconfig %s does not authenticate to the cluster (%w); NOT acknowledging, so the rotation keeps "+
			"waiting and your current credentials keep working", files.Kubeconfig, err)
	}

	if checks.Talos != nil {
		if err := checks.Talos(ctx, files.Talosconfig); err != nil {
			_, _ = fmt.Fprintf(errw, "ichor: warning: could not check the new talosconfig %s against a node from this machine (%v); "+
				"continuing, since the kubeconfig works\n", files.Talosconfig, err)
		}
	}

	name, err := acknowledgeHandoff(ctx, k8s, req.Namespace, req.Cluster, operation)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(errw, "Acknowledged on operation %s: the rotation goes on.\n", name)

	return nil
}

// reportCredentials says where the files are and what to do with them. It never prints their content.
func reportCredentials(w io.Writer, files *credentialFiles) {
	trusts := "the cluster's CA"
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
	f.BoolVar(&clusterCredentialsOpts.Acknowledge, "acknowledge", false,
		"after checking the new kubeconfig works, tell the CA rotation that is waiting for credentials to continue")
	f.StringVar(&clusterCredentialsOpts.Operation, "operation", "", "with --acknowledge: the CA rotation operation to acknowledge")
}

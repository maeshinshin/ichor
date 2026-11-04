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
	"strings"
	"time"

	"github.com/spf13/cobra"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var (
	clusterRotateKeysOnly               string
	clusterRotateKeysTokenSettle        string
	clusterRotateKeysIgnoreLegacyTokens bool
	clusterRotateKeysFollow             bool
)

// clusterRotateKeysCmd implements `ichor cluster rotate-keys` (ADR 0121): it starts a key-rotation
// TalosOperation through OperationService like every other start (ADR 0068), so the plan, the refusals and
// the permission check are the ones the UI has. Unlike rotate-ca it hands the operator nothing: the Talos API
// and the Kubernetes API CAs are not touched, so every kubeconfig and talosconfig keeps working.
var clusterRotateKeysCmd = &cobra.Command{
	Use:   "rotate-keys NAME",
	Short: "Replace the cluster's service-account signing key and secretbox (encryption at rest) key",
	Long: `rotate-keys starts a key rotation operation: the Kubernetes service-account signing key and the
kube-apiserver secretbox key that Secrets are encrypted with in etcd are replaced in a dual-key period.
The new keys are first only accepted by every control plane (the old ones still sign and encrypt), then
made primary; every Secret in the cluster is rewritten under the new secretbox key; the old service-account
key is kept while the tokens it signed are re-issued; and only then are the old keys dropped. Control planes
are reconfigured one at a time and each kube-apiserver is waited for, so the Kubernetes API stays up on a
cluster with more than one control plane. Workers are not touched.

  --only service-account|secretbox   rotate one key (default: both)
  --token-settle 90m                 how long the old service-account key is kept after the new one signs;
                                     raise it when workloads ask for projected tokens that live longer than an hour
  --ignore-legacy-tokens             drop the old service-account key although token Secrets
                                     (type kubernetes.io/service-account-token) it signed still exist; they stop working

Your kubeconfig and talosconfig keep working: no CA changes. The cluster must use versionContract
v1.14 or later. Before it starts, every control plane must answer, run the configuration Ichor
renders for it, and every Kubernetes Node must be Ready; otherwise the operation pauses without
changing anything. While it runs, the machine controller does not reconcile the control planes and other
disruptive operations are refused. It resumes by itself after the controller restarts and does not advance
past a step it could not verify.

When it completes, the secrets bundle in the cluster has been replaced and it now also keeps the retired
keys (backups sealed before the rotation stay readable with them), so the copy saved on your machine is
stale: run 'ichor cluster bundle-export NAME' and keep both files safe. Defragment etcd on each control
plane afterwards ('ichor node ...' etcd-defrag): old revisions encrypted with the old key stay until then.
A snapshot taken before the rotation finished cannot be restored.

It shows which control planes are reconfigured and in which order, and asks for confirmation unless --yes.
--follow keeps reporting progress until the rotation completes.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		targets, err := keyRotationTargets(clusterRotateKeysOnly)
		if err != nil {
			return err
		}

		spec, err := keyRotationStart(args[0], targets, clusterRotateKeysTokenSettle, clusterRotateKeysIgnoreLegacyTokens)
		if err != nil {
			return err
		}

		spec.Follow = clusterRotateKeysFollow

		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runStartOperation(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), spec)
		})
	},
}

// keyRotationTargets reads --only: a comma-separated list of the keys to rotate, "service-account" or
// "serviceAccount" and "secretbox". Empty means both (nil, which the server expands).
func keyRotationTargets(only string) ([]string, error) {
	if strings.TrimSpace(only) == "" {
		return nil, nil
	}

	var out []string

	for name := range strings.SplitSeq(only, ",") {
		switch strings.TrimSpace(name) {
		case "service-account", ichorv1alpha1.KeyRotationTargetServiceAccount:
			out = append(out, ichorv1alpha1.KeyRotationTargetServiceAccount)
		case ichorv1alpha1.KeyRotationTargetSecretbox:
			out = append(out, ichorv1alpha1.KeyRotationTargetSecretbox)
		default:
			return nil, fmt.Errorf("--only: %q is not a key this rotation replaces (service-account, secretbox)", strings.TrimSpace(name))
		}
	}

	return out, nil
}

// keyRotationStart is what the flags ask for, as a start.
func keyRotationStart(cluster string, targets []string, tokenSettle string, ignoreLegacyTokens bool) (startSpec, error) {
	if tokenSettle != "" {
		if d, err := time.ParseDuration(tokenSettle); err != nil || d < 0 {
			return startSpec{}, fmt.Errorf("--token-settle %q is not a duration of zero or more (like 90m)", tokenSettle)
		}
	}

	return startSpec{
		Kind: pb.OperationKind_OPERATION_KIND_KEY_ROTATION, Cluster: cluster,
		KeyRotation: &pb.KeyRotationRequest{Targets: targets, TokenSettle: tokenSettle, IgnoreLegacyTokens: ignoreLegacyTokens},
	}, nil
}

func init() {
	clusterCmd.AddCommand(clusterRotateKeysCmd)
	clusterRotateKeysCmd.Flags().StringVar(&clusterRotateKeysOnly, "only", "", "rotate only this key: service-account or secretbox (default: both)")
	clusterRotateKeysCmd.Flags().StringVar(&clusterRotateKeysTokenSettle, "token-settle", "",
		"how long the old service-account key is kept after the new one signs (default 90m)")
	clusterRotateKeysCmd.Flags().BoolVar(&clusterRotateKeysIgnoreLegacyTokens, "ignore-legacy-tokens", false,
		"drop the old service-account key although token Secrets it signed still exist")
	clusterRotateKeysCmd.Flags().BoolVar(&clusterRotateKeysFollow, "follow", false, "keep reporting progress until the rotation completes or fails")
	markWrites(clusterRotateKeysCmd)
}

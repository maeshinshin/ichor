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
	"io"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var describeConfigCmd = &cobra.Command{
	Use:   "config NODE",
	Short: "Show a node's rendered configuration, each value tagged with the layer that set it",
	Long: `describe config renders the node's complete configuration the way the
controller does and prints every value with its origin layer. Secrets are
masked ("***") by the same code ichor-ui uses -- this works without ichor-ui,
reading the CRs and the secrets bundle with your own Kubernetes access.`,
	Example: `  ichor describe config cp-1
  ichor describe config worker-3 -o yaml`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runConfigEffective(ctx, c, cmd.OutOrStdout(), args[0])
		})
	},
}

func runConfigEffective(ctx context.Context, c *apiClients, out io.Writer, machine string) error {
	resp, err := c.Node.GetEffectiveConfig(ctx, connect.NewRequest(&pb.GetEffectiveConfigRequest{
		Machine: objectRef(apiOpts.Namespace, machine),
	}))
	if err != nil {
		return err
	}

	return printAPI(out, resp.Msg, renderEffectiveConfig)
}

var configApplyFlags startFlags

var rolloutConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Roll out the pending configuration changes that need a reboot or an API server restart",
	Long: `Ichor never reboots a node for a configuration change on its own: a node whose
rendered configuration differs from what it runs in a way that needs a reboot waits with
Ready=False, reason RebootRequired, and the message names the fields. rollout config starts the
config operation that rolls those changes out, one node at a time: control planes
first, one at a time with an etcd membership check before each, then workers (at most
--max-unavailable at once), and the node this controller runs on last. Per node:
storage evacuation hook, the node is cordoned and drained (evictions respect
PodDisruptionBudgets; DaemonSet and mirror pods stay), the configuration is staged, the node
reboots, and the step finishes when the node runs the configuration, is Ready, is
uncordoned and (control planes) is back in etcd. On a cluster with one control plane the
Kubernetes API is unavailable while it reboots. A change that needs no reboot but restarts the
Kubernetes API server (authentication, API server arguments, admission, audit, authorizers: Ready=False,
reason APIServerRestartRequired) is applied the same way, one control plane at a time with no drain
and no reboot, waiting for that node's API server to answer /readyz before the next. A node that fails pauses the operation; nothing is
rolled back. Nodes held with upgrade-hold are skipped.

It shows which nodes reboot, which fields need it and in which order, and asks for
confirmation unless --yes.`,
	Example: `  ichor rollout config --cluster prod
  ichor rollout config --cluster prod --max-unavailable 2 --follow`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runStartOperation(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), startSpec{
				Kind: pb.OperationKind_OPERATION_KIND_CONFIG, Cluster: configApplyFlags.Cluster,
				MaxUnavailable: configApplyFlags.MaxUnavailable, Follow: configApplyFlags.Follow,
			})
		})
	},
}

func init() {
	addAPIFlags(describeConfigCmd)
	addAPIFlags(rolloutConfigCmd)
	describeCmd.AddCommand(describeConfigCmd)
	rolloutCmd.AddCommand(rolloutConfigCmd)
	addStartFlags(rolloutConfigCmd, &configApplyFlags)
	markWrites(rolloutConfigCmd)
}

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

// configCmd groups the machine-configuration read subcommands.
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect machine configuration",
}

var configEffectiveCmd = &cobra.Command{
	Use:   "effective MACHINE",
	Short: "Show a machine's rendered configuration, each value tagged with the layer that set it",
	Long: `effective renders the machine's complete configuration the way the
controller does and prints every value with its origin layer. Secrets are
masked ("***") by the same code ichor-ui uses -- this works without ichor-ui,
reading the CRs and the secrets bundle with your own Kubernetes access.`,
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

var configApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply the pending configuration changes that need a reboot or an API server restart",
	Long: `Ichor never reboots a node for a configuration change on its own: a machine whose
rendered configuration differs from the node's in a way that needs a reboot waits with
Ready=False, reason RebootRequired, and the message names the fields. apply starts the
config operation that rolls those changes out, one machine at a time: control planes
first, one at a time with an etcd membership check before each, then workers (at most
--max-unavailable at once), and the machine this controller runs on last. Per machine:
storage evacuation hook, the node is cordoned and drained (evictions respect
PodDisruptionBudgets; DaemonSet and mirror pods stay), the configuration is staged, the node
reboots, and the step finishes when the node runs the configuration, is Ready, is
uncordoned and (control planes) is back in etcd. On a cluster with one control plane the
Kubernetes API is unavailable while it reboots. A change that needs no reboot but restarts the
Kubernetes API server (authentication, API server arguments, admission, audit, authorizers: Ready=False,
reason APIServerRestartRequired) is applied the same way, one control plane at a time with no drain
and no reboot, waiting for that node's API server to answer /readyz before the next. A machine that fails pauses the operation; nothing is
rolled back. Machines held with upgrade-hold are skipped.

It shows which machines reboot, which fields need it and in which order, and asks for
confirmation unless --yes.`,
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
	rootCmd.AddCommand(configCmd)
	addAPIFlags(configCmd)
	configCmd.AddCommand(configEffectiveCmd, configApplyCmd)
	addStartFlags(configApplyCmd, &configApplyFlags)
	markWrites(configApplyCmd)
}

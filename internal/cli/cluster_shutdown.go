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

	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var clusterShutdownFollow bool

var shutdownClusterCmd = &cobra.Command{
	Use:   "cluster NAME",
	Short: "Stop every node of a cluster in the order that keeps the API up longest",
	Long: `shutdown cluster starts a shutdown operation: workers are drained and stopped first, then
every control plane. Nodes stay off until you power them on; the operation
completes once they answer again. While it has not completed, every other
disruptive operation on the cluster is refused.

It shows which nodes stop and in which order, and asks for confirmation unless --yes.`,
	Example: `  ichor shutdown cluster lab
  ichor shutdown cluster lab --follow --yes`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runStartOperation(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
				startSpec{Kind: pb.OperationKind_OPERATION_KIND_SHUTDOWN, Cluster: args[0], Follow: clusterShutdownFollow})
		})
	},
}

func init() {
	addAPIFlags(shutdownClusterCmd)
	shutdownCmd.AddCommand(shutdownClusterCmd)
	shutdownClusterCmd.Flags().BoolVar(&clusterShutdownFollow, "follow", false,
		"after creating the operation, follow its progress (it ends at ShutDown, when every node is down)")
	markWrites(shutdownClusterCmd)
}

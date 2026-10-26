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

// clusterCmd groups the read-only TalosCluster subcommands.
var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "List or inspect clusters",
}

var clusterListCmd = &cobra.Command{
	Use:   "list",
	Short: "List clusters",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runClusterList(ctx, c, cmd.OutOrStdout())
		})
	},
}

var clusterGetCmd = &cobra.Command{
	Use:   "get NAME",
	Short: "Show one cluster: versions, conditions, rollout and hand-over state",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runClusterGet(ctx, c, cmd.OutOrStdout(), args[0])
		})
	},
}

func runClusterList(ctx context.Context, c *apiClients, out io.Writer) error {
	resp, err := c.Cluster.ListClusters(ctx, connect.NewRequest(&pb.ListClustersRequest{}))
	if err != nil {
		return err
	}

	return writeMessage(out, outputFormat, resp.Msg)
}

func runClusterGet(ctx context.Context, c *apiClients, out io.Writer, name string) error {
	resp, err := c.Cluster.GetCluster(ctx, connect.NewRequest(&pb.GetClusterRequest{Cluster: objectRef(apiOpts.Namespace, name)}))
	if err != nil {
		return err
	}

	return writeMessage(out, outputFormat, resp.Msg)
}

func init() {
	rootCmd.AddCommand(clusterCmd)
	addAPIFlags(clusterCmd)
	clusterCmd.AddCommand(clusterListCmd, clusterGetCmd)
}

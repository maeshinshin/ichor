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

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var machineListCluster string

var getNodesCmd = &cobra.Command{
	Use:   "nodes",
	Short: "List nodes, pending ones included",
	Example: `  ichor get nodes
  ichor get nodes --cluster prod -o yaml`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return showAPIHuman(cmd, false, func(ctx context.Context, c *apiClients) (*connect.Response[pb.ListMachinesResponse], error) {
			return c.Machine.ListMachines(ctx, connect.NewRequest(&pb.ListMachinesRequest{
				Cluster: clusterFilter(apiOpts.Namespace, machineListCluster),
			}))
		}, renderMachineList)
	},
}

func init() {
	addAPIFlags(getNodesCmd)
	getCmd.AddCommand(getNodesCmd)
	getNodesCmd.Flags().StringVar(&machineListCluster, flagCluster, "", "only nodes of this TalosCluster")
}

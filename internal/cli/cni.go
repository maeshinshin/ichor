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

var cniFlags startFlags

var cniCmd = &cobra.Command{
	Use:   "cni",
	Short: "Operate on the cluster's CNI settings Ichor owns",
}

var cniKubeProxyCmd = &cobra.Command{
	Use:   "kube-proxy replaced|enabled",
	Short: "Switch kube-proxy between enabled and replaced by the CNI",
	Long: `kube-proxy sets spec.cni.kubeProxy and starts the operation that carries the switch out
node by node: the new side comes up and is verified before the old side is turned off.
The admission policy that guards this field wants a confirmation annotation; the
server sets it (to the new mode) for this one write and removes it again, so it never stays behind as
a standing consent. Ichor does not edit the CNI's own configuration: the operation
tells you the values to put there and waits for you to.

It shows which machines are touched and in which order, and asks for confirmation unless --yes.`,
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{"replaced", "enabled"},
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			f := cniFlags

			return runStartOperation(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
				startSpec{Kind: pb.OperationKind_OPERATION_KIND_CNI, Cluster: f.Cluster, Target: args[0], MaxUnavailable: f.MaxUnavailable, Follow: f.Follow})
		})
	},
}

func init() {
	rootCmd.AddCommand(cniCmd)
	addAPIFlags(cniCmd)
	cniCmd.AddCommand(cniKubeProxyCmd)
	addStartFlags(cniKubeProxyCmd, &cniFlags)
	markWrites(cniKubeProxyCmd)
}

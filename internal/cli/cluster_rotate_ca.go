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

var clusterRotateCAFollow bool

// clusterRotateCACmd implements `ichor cluster rotate-ca`: it starts a ca-rotation TalosOperation
// (ADR 0063) through OperationService like every other start (ADR 0068), so the plan, the
// refusals and the permission check are the ones the UI has. The controller runs the rotation.
var clusterRotateCACmd = &cobra.Command{
	Use:   "rotate-ca NAME",
	Short: "Replace the cluster's Talos API and Kubernetes API certificate authorities",
	Long: `rotate-ca starts a CA rotation operation: the Talos API CA and the Kubernetes API CA are
replaced in three passes over every machine (accept the new CA, make it the issuer, drop the old
one), each verified before the next. The kubelet restarts on each machine in the passes that change
the Kubernetes CA. The etcd and aggregator CAs and the service-account key are not rotated.

Before it starts, every machine must answer, run the configuration Ichor renders for it, and every
Kubernetes Node must be Ready; otherwise the operation pauses without changing anything. While it
runs, the machine controller does not reconcile the cluster's machines and other disruptive
operations are refused. It resumes by itself after the controller restarts, and does not advance past
a step it could not verify. Machines held with upgrade-hold are not skipped.

When it completes, the secrets bundle in the cluster has been replaced, and the copy saved on your
machine is stale: run 'ichor cluster bundle-export NAME' and keep the file safe.

It shows which machines are reconfigured and in which order, and asks for confirmation unless --yes.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runStartOperation(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
				startSpec{Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, Cluster: args[0], Follow: clusterRotateCAFollow})
		})
	},
}

func init() {
	clusterCmd.AddCommand(clusterRotateCACmd)
	clusterRotateCACmd.Flags().BoolVar(&clusterRotateCAFollow, "follow", false,
		"after creating the operation, follow its progress until it completes or a machine fails")
	markWrites(clusterRotateCACmd)
}

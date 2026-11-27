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
	"strconv"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var setContainerdCluster string

var setContainerdDeviceOwnershipCmd = &cobra.Command{
	Use:   "containerd-device-ownership true|false",
	Short: "Let containerd give a pod's block devices to its user and group, so non-root pods can use volumeMode: Block volumes",
	Long: `Set spec.containerd.deviceOwnershipFromSecurityContext of the cluster (ADR 0172). With true, containerd's
device_ownership_from_security_context is on: a container's devices are owned by the pod's runAsUser and fsGroup
instead of root. KubeVirt virtual machines and CDI importers run as a non-root user and need it to open a
volumeMode: Block volume.

The controller renders the change into every node, which restarts containerd there; no node reboots. Pods that
are already running keep their device ownership until they are recreated. Needs versionContract v1.14 or later.`,
	Example: `  ichor set containerd-device-ownership true --cluster prod`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		enabled, err := strconv.ParseBool(args[0])
		if err != nil {
			return fmt.Errorf("%q is not true or false", args[0])
		}

		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runSetDeviceOwnership(ctx, c, cmd, enabled)
		})
	},
}

func runSetDeviceOwnership(ctx context.Context, c *apiClients, cmd *cobra.Command, enabled bool) error {
	ref, err := resolveCluster(ctx, c, setContainerdCluster)
	if err != nil {
		return err
	}

	resp, err := c.Cluster.SetContainerdDeviceOwnership(ctx, connect.NewRequest(
		&pb.SetContainerdDeviceOwnershipRequest{Cluster: ref, Enabled: enabled}))
	if err != nil {
		return err
	}

	if outputFormat != outputHuman {
		return writeMessage(cmd.OutOrStdout(), outputFormat, resp.Msg)
	}

	h := newHuman(cmd.OutOrStdout())
	h.done("containerd device ownership of %s is now %t", ref.GetName(), enabled)

	return h.Err()
}

func init() {
	addAPIFlags(setContainerdDeviceOwnershipCmd)
	setCmd.AddCommand(setContainerdDeviceOwnershipCmd)
	setContainerdDeviceOwnershipCmd.Flags().StringVar(&setContainerdCluster, "cluster", "",
		"TalosCluster in the controller's namespace (--ichor-namespace, default ichor-system) to change; optional when there is exactly one")
	markWrites(setContainerdDeviceOwnershipCmd)
}

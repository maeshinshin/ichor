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

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var getClustersCmd = &cobra.Command{
	Use:   "clusters",
	Short: "List clusters",
	Example: `  ichor get clusters
  ichor get clusters -o yaml`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return showAPIHuman(cmd, false, func(ctx context.Context, c *apiClients) (*connect.Response[pb.ListClustersResponse], error) {
			return c.Cluster.ListClusters(ctx, connect.NewRequest(&pb.ListClustersRequest{}))
		}, renderClusterList)
	},
}

var describeClusterCmd = &cobra.Command{
	Use:   useClusterName,
	Short: "Show one cluster: versions, conditions, rollout and hand-over state",
	Example: `  ichor describe cluster prod
  ichor describe cluster prod -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return showAPIHuman(cmd, false, func(ctx context.Context, c *apiClients) (*connect.Response[pb.GetClusterResponse], error) {
			return c.Cluster.GetCluster(ctx, connect.NewRequest(&pb.GetClusterRequest{Cluster: objectRef(apiOpts.Namespace, args[0])}))
		}, func(h *human, r *pb.GetClusterResponse) error { return renderClusterGet(h, r.GetCluster()) })
	},
}

var getClusterCmd = &cobra.Command{
	Use:     useClusterName,
	Short:   "Show a cluster's versions, endpoint and CNI in a few lines",
	Example: `  ichor get cluster prod`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runClusterStatus(ctx, c, cmd.OutOrStdout(), args[0])
		})
	},
}

func runClusterStatus(ctx context.Context, c *apiClients, out io.Writer, name string) error {
	resp, err := c.Cluster.GetCluster(ctx, connect.NewRequest(&pb.GetClusterRequest{Cluster: objectRef(apiOpts.Namespace, name)}))
	if err != nil {
		return err
	}

	cl := resp.Msg.GetCluster()
	if outputFormat == outputHuman {
		return renderClusterStatus(newHuman(out), cl)
	}

	// -o yaml and -o json keep the key: value lines status always printed (scripts read them).
	_, err = fmt.Fprintf(out, "cluster: %s/%s\ntalos: %s\nkubernetes: %s\nendpoint: %s\ncni: %s\n",
		cl.GetRef().GetNamespace(), cl.GetRef().GetName(), cl.GetTalosVersion(), cl.GetKubernetesVersion(),
		cl.GetEndpoint(), cniStatusLine(cl))

	return err
}

// cniStatusLine is the cni line of `ichor get cluster -o yaml`.
func cniStatusLine(cl *pb.Cluster) string {
	if cl.GetCniPreset() != ichorv1alpha1.TalosClusterCNIPresetUnmanaged {
		return string(detectedCilium)
	}

	detected := cl.GetCniDetected()
	if detected == "" {
		detected = "not detected yet"
	}

	return fmt.Sprintf("unmanaged (%s, not managed by Ichor)", detected)
}

func init() {
	for _, c := range []*cobra.Command{getClustersCmd, getClusterCmd, describeClusterCmd} {
		addAPIFlags(c)
	}

	getCmd.AddCommand(getClustersCmd, getClusterCmd)
	describeCmd.AddCommand(describeClusterCmd)
}

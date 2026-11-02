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
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var clusterDiscoveryCluster string

// clusterDiscoveryCmd groups the commands that edit what discovery scans (ADR 0011): single
// addresses and IPv4 ranges. They write spec.discovery as the caller.
var clusterDiscoveryCmd = &cobra.Command{
	Use:   "discovery",
	Short: "List, add or remove the addresses and ranges Ichor scans for new machines",
}

var clusterDiscoveryListCmd = &cobra.Command{
	Use:   useList,
	Short: "List the addresses and ranges the cluster scans",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runDiscoveryList(ctx, c, cmd.OutOrStdout())
		})
	},
}

var clusterDiscoveryAddCmd = &cobra.Command{
	Use:   "add ADDRESS|CIDR",
	Short: "Scan one more IPv4 address or range (/24 or smaller) for machines in maintenance mode",
	Long: `Add an IPv4 address (192.168.0.50) or a range (192.168.0.0/24) to the cluster's
spec.discovery. A range is scanned address by address (at most 256 per range, at most
4 ranges). The scan starts right away; machines found appear as pending (see
"ichor machine list") and wait for approval.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runDiscoveryEdit(ctx, c, cmd, args[0], false)
		})
	},
}

var clusterDiscoveryRemoveCmd = &cobra.Command{
	Use:   "remove ADDRESS|CIDR",
	Short: "Stop scanning an address or range (machines already found stay)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runDiscoveryEdit(ctx, c, cmd, args[0], true)
		})
	},
}

// runDiscoveryEdit adds or removes one target. A slash means a range. Removing a range asks first
// (unless --yes) because it can stand for up to 256 machines.
func runDiscoveryEdit(ctx context.Context, c *apiClients, cmd *cobra.Command, target string, remove bool) error {
	target = strings.TrimSpace(target)
	isCidr := strings.Contains(target, "/")

	ref, err := resolveCluster(ctx, c, clusterDiscoveryCluster)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	if remove && isCidr && !assumeYes {
		if err := confirmOrAbort(cmd.InOrStdin(), cmd.ErrOrStderr(), "discovery remove",
			fmt.Sprintf("Stop scanning %s in cluster %s/%s? [y/N] ", target, ref.GetNamespace(), ref.GetName()), "aborted"); err != nil {
			return err
		}
	}

	switch {
	case remove && isCidr:
		resp, err := c.Cluster.RemoveDiscoveryCidr(ctx, connect.NewRequest(&pb.RemoveDiscoveryCidrRequest{Cluster: ref, Cidr: target}))
		if err != nil {
			return err
		}

		return writeMessage(out, outputFormat, resp.Msg)
	case remove:
		resp, err := c.Cluster.RemoveDiscoveryAddress(ctx, connect.NewRequest(&pb.RemoveDiscoveryAddressRequest{Cluster: ref, Address: target}))
		if err != nil {
			return err
		}

		return writeMessage(out, outputFormat, resp.Msg)
	case isCidr:
		resp, err := c.Cluster.AddDiscoveryCidr(ctx, connect.NewRequest(&pb.AddDiscoveryCidrRequest{Cluster: ref, Cidr: target}))
		if err != nil {
			return err
		}

		return writeMessage(out, outputFormat, resp.Msg)
	default:
		resp, err := c.Cluster.AddDiscoveryAddress(ctx, connect.NewRequest(&pb.AddDiscoveryAddressRequest{Cluster: ref, Address: target}))
		if err != nil {
			return err
		}

		return writeMessage(out, outputFormat, resp.Msg)
	}
}

func runDiscoveryList(ctx context.Context, c *apiClients, out io.Writer) error {
	ref, err := resolveCluster(ctx, c, clusterDiscoveryCluster)
	if err != nil {
		return err
	}

	resp, err := c.Cluster.GetCluster(ctx, connect.NewRequest(&pb.GetClusterRequest{Cluster: ref}))
	if err != nil {
		return err
	}

	cl := resp.Msg.GetCluster()
	if len(cl.GetDiscoveryAddresses())+len(cl.GetDiscoveryCidrs()) == 0 {
		_, err = fmt.Fprintln(out, "discovery is off: no address or range is listed")

		return err
	}

	for _, a := range cl.GetDiscoveryAddresses() {
		if _, err := fmt.Fprintf(out, "address  %s\n", a); err != nil {
			return err
		}
	}

	for _, r := range cl.GetDiscoveryCidrs() {
		if _, err := fmt.Fprintf(out, "range    %s\n", r); err != nil {
			return err
		}
	}

	return nil
}

func init() {
	clusterCmd.AddCommand(clusterDiscoveryCmd)
	clusterDiscoveryCmd.AddCommand(clusterDiscoveryListCmd, clusterDiscoveryAddCmd, clusterDiscoveryRemoveCmd)
	clusterDiscoveryCmd.PersistentFlags().StringVar(&clusterDiscoveryCluster, "cluster", "",
		"TalosCluster (in --namespace) to edit; optional when the namespace has exactly one")
	markWrites(clusterDiscoveryAddCmd, clusterDiscoveryRemoveCmd)
}

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
	"errors"
	"fmt"
	"io"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var describeNodeCmd = &cobra.Command{
	Use:   "node NAME",
	Short: "Show one node: its record, and its hardware, usage and Kubernetes status when ichor-ui is reachable",
	Long: `describe node shows the node's record (the TalosMachine: role, approval, install disk, network, disks,
conditions) and, below it, what only a live read of the node gives: its hardware, CPU, memory and filesystem
use, disks and the Kubernetes Node. The live part is read through ichor-ui, which holds the read-only Talos
identity (the CLI never talks to a node itself).

When ichor-ui is not reachable, or the node cannot be read (it is still pending, or powered off), the record
alone is shown, with a line saying why the live part is missing. -o yaml and -o json print the record under
"machine" and, when it was read, the live part under "node".`,
	Example: `  ichor describe node cp-1
  ichor describe node worker-3 -o yaml`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runDescribeNode(ctx, c, cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0])
		})
	},
}

// runDescribeNode shows the record of the node always and the live detail when it can be read.
func runDescribeNode(ctx context.Context, c *apiClients, out, errw io.Writer, name string) error {
	ref := objectRef(apiOpts.Namespace, name)

	rec, err := c.Machine.GetMachine(ctx, connect.NewRequest(&pb.GetMachineRequest{Machine: ref}))
	if err != nil {
		return err
	}

	live, liveErr := readNodeLive(ctx, c, ref)

	if outputFormat != outputHuman {
		if liveErr != nil {
			_, _ = fmt.Fprintf(errw, "ichor: no live detail for node %s: %v\n", name, liveErr)

			return writeMessage(out, outputFormat, rec.Msg)
		}

		return writeMessages(out, outputFormat, rec.Msg, live)
	}

	h := newHuman(out)
	if err := renderMachineGet(h, rec.Msg.GetMachine()); err != nil {
		return err
	}

	if liveErr != nil {
		h.Note("\nLive detail (hardware, usage, Kubernetes node) is not shown: " + liveErr.Error())

		return h.Err()
	}

	return renderNodeLive(h, live.GetNode())
}

// readNodeLive reads the node through ichor-ui. The in-process fallback has no Talos identity, so it
// is not asked.
func readNodeLive(ctx context.Context, c *apiClients, ref *pb.ObjectRef) (*pb.GetNodeResponse, error) {
	if c.Local {
		return nil, errors.New("it needs ichor-ui, which was not used (it is not reachable, or --local was given)")
	}

	resp, err := c.Node.GetNode(ctx, connect.NewRequest(&pb.GetNodeRequest{Machine: ref}))
	if err != nil {
		return nil, err
	}

	return resp.Msg, nil
}

func init() {
	addAPIFlags(describeNodeCmd)
	describeCmd.AddCommand(describeNodeCmd)
}

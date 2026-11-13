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

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var rejectNodeCmd = &cobra.Command{
	Use:     "node NAME",
	Short:   "Reject a pending node (recorded, never deleted)",
	Example: `  ichor reject node 3f1c2a`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runMachineReject(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0])
		})
	},
}

func runMachineReject(ctx context.Context, c *apiClients, in io.Reader, out, errw io.Writer, name string) error {
	if !assumeYes {
		if err := confirmOrAbort(in, errw, "reject node", fmt.Sprintf("Reject machine %s/%s? [y/N] ", apiOpts.Namespace, name), "aborted"); err != nil {
			return err
		}
	}

	resp, err := c.Machine.RejectMachine(ctx, connect.NewRequest(&pb.RejectMachineRequest{
		Machine: objectRef(apiOpts.Namespace, name),
	}))
	if err != nil {
		return err
	}

	if outputFormat == outputHuman {
		return renderMachineRejected(newHuman(out), name)
	}

	return writeMessage(out, outputFormat, resp.Msg)
}

func init() {
	addAPIFlags(rejectNodeCmd)
	rejectCmd.AddCommand(rejectNodeCmd)
}

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

var machineGetCmd = &cobra.Command{
	Use:   "get NAME",
	Short: "Show one machine",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runMachineGet(ctx, c, cmd.OutOrStdout(), args[0])
		})
	},
}

func runMachineGet(ctx context.Context, c *apiClients, out io.Writer, name string) error {
	resp, err := c.Machine.GetMachine(ctx, connect.NewRequest(&pb.GetMachineRequest{Machine: objectRef(apiOpts.Namespace, name)}))
	if err != nil {
		return err
	}

	return writeMessage(out, outputFormat, resp.Msg)
}

func init() { machineCmd.AddCommand(machineGetCmd) }

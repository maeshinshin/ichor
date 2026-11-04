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

var machineGetCmd = &cobra.Command{
	Use:   useGetName,
	Short: "Show one machine",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return showAPIHuman(cmd, false, func(ctx context.Context, c *apiClients) (*connect.Response[pb.GetMachineResponse], error) {
			return c.Machine.GetMachine(ctx, connect.NewRequest(&pb.GetMachineRequest{Machine: objectRef(apiOpts.Namespace, args[0])}))
		}, func(h *human, r *pb.GetMachineResponse) error { return renderMachineGet(h, r.GetMachine()) })
	},
}

func init() { machineCmd.AddCommand(machineGetCmd) }

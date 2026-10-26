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

// configCmd groups the machine-configuration read subcommands.
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect machine configuration",
}

var configEffectiveCmd = &cobra.Command{
	Use:   "effective MACHINE",
	Short: "Show a machine's rendered configuration, each value tagged with the layer that set it",
	Long: `effective renders the machine's complete configuration the way the
controller does and prints every value with its origin layer. Secrets are
masked ("***") by the same code ichor-ui uses -- this works without ichor-ui,
reading the CRs and the secrets bundle with your own Kubernetes access.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runConfigEffective(ctx, c, cmd.OutOrStdout(), args[0])
		})
	},
}

func runConfigEffective(ctx context.Context, c *apiClients, out io.Writer, machine string) error {
	resp, err := c.Node.GetEffectiveConfig(ctx, connect.NewRequest(&pb.GetEffectiveConfigRequest{
		Machine: objectRef(apiOpts.Namespace, machine),
	}))
	if err != nil {
		return err
	}

	return writeMessage(out, outputFormat, resp.Msg)
}

func init() {
	rootCmd.AddCommand(configCmd)
	addAPIFlags(configCmd)
	configCmd.AddCommand(configEffectiveCmd)
}

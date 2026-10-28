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
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/maeshinshin/ichor/internal/stackapply"
)

// stackCmd groups what only the ichor-stack Job runs. Hidden: the
// controller starts the Job; a person never runs this.
var stackCmd = &cobra.Command{
	Use:    "stack",
	Short:  "Commands the ichor-stack Job runs",
	Hidden: true,
}

var stackApplyValues string

var stackApplyCmd = &cobra.Command{
	Use:       "apply " + stackapply.ComponentKokumi + "|" + stackapply.ComponentArgoCD,
	Short:     "Apply Kokumi or Argo CD once, create their Secrets, wait for them and apply their network policies",
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{stackapply.ComponentKokumi, stackapply.ComponentArgoCD},
	RunE: func(cmd *cobra.Command, args []string) error {
		o := stackapply.Options{
			Component:         args[0],
			KokumiAdminHash:   os.Getenv(stackapply.EnvKokumiAdminHash),
			ArgoRedisPassword: os.Getenv(stackapply.EnvArgoRedisPasswd),
			Log:               cmd.ErrOrStderr(),
		}

		if stackApplyValues != "" {
			if err := json.Unmarshal([]byte(stackApplyValues), &o.ArgoCDValues); err != nil {
				return fmt.Errorf("--values: %w", err)
			}
		}

		c, err := newKubeClient(kubeconfigPath)
		if err != nil {
			return err
		}

		return stackapply.Apply(cmd.Context(), c, o)
	},
}

func init() {
	rootCmd.AddCommand(stackCmd)
	stackCmd.AddCommand(stackApplyCmd)
	stackApplyCmd.Flags().StringVar(&stackApplyValues, "values", "", "the user layer for the Argo CD chart, as a JSON object")
}

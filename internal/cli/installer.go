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
	"github.com/spf13/cobra"

	"github.com/maeshinshin/ichor/internal/installer"
	"github.com/maeshinshin/ichor/internal/installmanifest"
)

// installerCmd groups what only the installer Job runs. Hidden: a person upgrades
// Ichor by creating a TalosOperation of kind ichor, never by running this.
var installerCmd = &cobra.Command{
	Use:    "installer",
	Short:  "Commands the installer Job runs",
	Hidden: true,
}

var installerRunOpts struct {
	Namespace string
	RunID     string
}

var installerRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Apply this binary's embedded install manifest, rolling back from a snapshot on failure",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := newKubeClient(kubeconfigPath)
		if err != nil {
			return err
		}

		return installer.Run(cmd.Context(), c, installer.Options{
			Namespace: installerRunOpts.Namespace,
			RunID:     installerRunOpts.RunID,
			Manifest:  installmanifest.Manifest,

			NetworkPolicies: installmanifest.NetworkPolicies,
			Log:       cmd.ErrOrStderr(),
		})
	},
}

func init() {
	rootCmd.AddCommand(installerCmd)
	installerCmd.AddCommand(installerRunCmd)

	f := installerRunCmd.Flags()
	f.StringVar(&installerRunOpts.Namespace, "namespace", ichorControllerNamespace, "namespace the snapshot is saved in")
	f.StringVar(&installerRunOpts.RunID, "run-id", "", "names the snapshot (the Job passes its own name)")
	_ = installerRunCmd.MarkFlagRequired("run-id")
}

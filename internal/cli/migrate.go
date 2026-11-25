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
	"fmt"

	"github.com/spf13/cobra"

	"github.com/maeshinshin/ichor/internal/installer"
)

var migrateCmd = &cobra.Command{
	Use:         "migrate",
	GroupID:     groupMaintain,
	RunE:        groupRunE,
	Args:        groupArgs,
	Annotations: groupingAnnotations(),
	Short:       "Move stored objects to the current schema",
	Example:     `  ichor migrate patches`,
}

var migratePatchesNamespace string

var migratePatchesCmd = &cobra.Command{
	Use:   "patches",
	Short: "Move TalosConfigPatch spec.documents to spec.config",
	Long: `Release 0.3 retired TalosConfigPatch spec.documents in favour of spec.config. A stored patch that still sets
spec.documents keeps rendering as it did, so no node changes, and this command rewrites it: spec.config gets the same
settings and spec.documents is cleared, in one update. ichor upgrade ichor does the same as its last step, and helm
upgrade does it in a post-upgrade hook Job; run this to do it again by hand.

A patch with a setting spec.config has no place for is left as it is, listed with the setting, and gets the condition
Migrated=False. Write those settings in spec.config by hand and clear spec.documents. Running the command again
changes nothing that is already migrated.`,
	Example: `  ichor migrate patches
  ichor migrate patches --namespace ichor-system`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := newKubeClient(kubeconfigPath)
		if err != nil {
			return err
		}

		res, err := installer.MigratePatches(cmd.Context(), c, migratePatchesNamespace, cmd.ErrOrStderr())
		if err != nil {
			return err
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "migrated %d TalosConfigPatches; %d left as they are\n", len(res.Migrated), len(res.Failed))

		for _, f := range res.Failed {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  "+f)
		}

		if len(res.Failed) > 0 {
			return fmt.Errorf("%d TalosConfigPatches could not be migrated", len(res.Failed))
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
	migrateCmd.AddCommand(migratePatchesCmd)
	migratePatchesCmd.Flags().StringVarP(&migratePatchesNamespace, "namespace", "n", "", "only the patches of this namespace (default: all)")
	markWrites(migratePatchesCmd)
}

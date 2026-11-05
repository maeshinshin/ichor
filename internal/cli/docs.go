package cli

import "github.com/spf13/cobra"

// RootCommand returns the ichor root command with every subcommand registered. The reference
// generator in internal/tools/gendocs walks it to write docs/reference/cli.
func RootCommand() *cobra.Command {
	return rootCmd
}

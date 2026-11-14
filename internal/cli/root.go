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
	"io"
	"os"

	"github.com/spf13/cobra"
)

// Global flags every subcommand shares (docs/design/architecture.md, "CLI
// and UI": "The CLI offers -o yaml|json ... and interactive confirmation
// with --yes"). kubeconfig and talosconfig follow kubectl's and talosctl's
// own conventions -- an empty default lets client-go's and machinery's own
// loaders resolve $KUBECONFIG/$TALOSCONFIG and the well-known file paths,
// instead of this command guessing at them a second time.
const (
	outputYAML = "yaml"
	outputJSON = "json"

	// flagCluster is the "--cluster" flag name every subcommand that names
	// a TalosCluster uses (adopt and init so far).
	flagCluster = "cluster"
)

var (
	kubeconfigPath  string
	talosconfigPath string
	outputFormat    string
	assumeYes       bool
)

// rootCmd is the base command when ichor is called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "ichor",
	Short: "Manage a Talos Linux cluster through Ichor's CRDs",
	Long: `ichor is the CLI half of Ichor's CLI/UI parity (docs/design/architecture.md,
"CLI and UI"): it changes the world by writing the same custom resources
the UI and controller use, never by talking to a Talos node directly to
mutate it.

After a command that reaches a cluster, ichor prints one line on stderr when the
cluster knows of a newer Ichor release, or when this binary is older than that
release. The check is made by the cluster's controller, not by ichor. The line is
shown on a terminal only, at most once a day per cluster, and never with -o or for
help and completion. Set ICHOR_NO_UPDATE_NOTICE=1 to turn it off.`,
	Example: `  ichor get clusters
  ichor describe node cp-1
  ichor upgrade talos --list`,
	SilenceUsage:  true,
	SilenceErrors: true, // executeRoot prints the error once
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	// PersistentPostRun does not run after a failed command, and the notice should show then too.
	cmd, err := executeRoot(os.Stderr)

	printUpdateNotice(cmd)

	if err != nil {
		os.Exit(1)
	}
}

// executeRoot runs the root command and prints its error once, on errw, unless a failed step showed
// it already. Cobra's own "Error: ..." line is off (SilenceErrors), or every error would appear twice.
func executeRoot(errw io.Writer) (*cobra.Command, error) {
	rootCmd.SetErr(errw)
	rootCmd.InitDefaultCompletionCmd() // cobra adds it inside ExecuteC, so it must exist before the walk
	rejectUnknownSubcommands(rootCmd)

	cmd, err := rootCmd.ExecuteC()
	if err != nil && !errorShown(err) {
		_, _ = fmt.Fprintln(errw, err)
	}

	return cmd, err
}

// rejectUnknownSubcommands gives every command with no Run of its own the grouping behaviour, so an unknown
// subcommand under it fails. Cobra rejects an unknown subcommand only under the root; under any other command with no
// Run it prints that command's help and exits 0. A command that has a Run is left alone.
func rejectUnknownSubcommands(c *cobra.Command) {
	for _, sub := range c.Commands() {
		if !sub.Runnable() {
			sub.Args = groupArgs
			sub.RunE = groupRunE

			if sub.Annotations == nil {
				sub.Annotations = groupingAnnotations() // the usage line reads "[command]", not "[flags]"
			}
		}

		rejectUnknownSubcommands(sub)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&kubeconfigPath, "kubeconfig", "",
		"path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config")
	rootCmd.PersistentFlags().StringVar(&talosconfigPath, "talosconfig", "",
		"path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config")
	rootCmd.PersistentFlags().StringVar(&bundlePassphraseFile, "bundle-passphrase-file", "",
		"file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; "+
			"otherwise $"+bundlePassphraseEnv+", then a prompt on a terminal. Never a value on the command line")
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", outputHuman,
		fmt.Sprintf("output format: %q or %q for scripts; by default a command prints for people (commands without a human view print YAML)",
			outputYAML, outputJSON))
	rootCmd.PersistentFlags().BoolVar(&assumeYes, "yes", false,
		"skip interactive confirmation before a dangerous operation")
}

// validateOutputFormat rejects anything but the two formats the design
// document promises ("-o yaml|json") and the unset default, before a command
// does any real work.
func validateOutputFormat(format string) error {
	if format != outputHuman && format != outputYAML && format != outputJSON {
		return fmt.Errorf("invalid --output %q: must be %q or %q", format, outputYAML, outputJSON)
	}

	return nil
}

// machineFormat is the format for a command that has no human view yet: the unset -o is YAML, the
// output every command had before human output became the default. Every writer of a
// machine format goes through it, so an unconverted command never prints nothing.
func machineFormat(format string) string {
	if format == outputHuman {
		return outputYAML
	}

	return format
}

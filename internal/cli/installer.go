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
	"errors"
	"io"
	"os"
	"unicode/utf8"

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

// installerTerminationLog is the file the kubelet mounts as the container's termination message
// path (the default). A variable, not a flag: the command's surface stays the same and tests
// point it at a file of their own.
var installerTerminationLog = "/dev/termination-log"

// terminationMessageLimit stays under the node's 4096-byte cut, so the node never truncates the
// message in the middle of a sentence.
const terminationMessageLimit = 4095

var installerRunOpts struct {
	Namespace string
	RunID     string

	CNIDetected string
	NoPolicies  bool
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

		err = installer.Run(cmd.Context(), c, installer.Options{
			Namespace: installerRunOpts.Namespace,
			RunID:     installerRunOpts.RunID,
			Manifest:  installmanifest.Manifest,

			NetworkPolicies:    installmanifest.NetworkPolicies,
			CNIDetected:        installerRunOpts.CNIDetected,
			SkipCiliumPolicies: installerRunOpts.NoPolicies,
			Log:                cmd.ErrOrStderr(),
		})
		if err != nil {
			// The pod log keeps the full record; the termination message is the summary the
			// controller shows on the paused operation. A failed write must not hide err.
			if werr := writeTerminationMessage(installerTerminationLog, err); werr != nil {
				_, _ = io.WriteString(cmd.ErrOrStderr(), "installer: could not write the termination message: "+werr.Error()+"\n")
			}
		}

		return err
	},
}

// writeTerminationMessage puts err's text at the start of path. It opens an existing file only
// (no O_CREATE): the kubelet mounts the file, and nothing else should ever be created here.
func writeTerminationMessage(path string, err error) error {
	f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if openErr != nil {
		return openErr
	}

	_, writeErr := io.WriteString(f, truncateAtRune(err.Error(), terminationMessageLimit))

	return errors.Join(writeErr, f.Close())
}

// truncateAtRune returns the longest prefix of s, at most limit bytes, that ends on a rune boundary.
func truncateAtRune(s string, limit int) string {
	if len(s) <= limit {
		return s
	}

	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}

	return s[:cut]
}

func init() {
	rootCmd.AddCommand(installerCmd)
	installerCmd.AddCommand(installerRunCmd)

	f := installerRunCmd.Flags()
	f.StringVar(&installerRunOpts.Namespace, "namespace", ichorControllerNamespace, "namespace the snapshot is saved in")
	f.StringVar(&installerRunOpts.RunID, "run-id", "", "names the snapshot (the Job passes its own name)")
	f.StringVar(&installerRunOpts.CNIDetected, "cni-detected", "", "TalosCluster status.cni.detected; Ichor's CiliumNetworkPolicies are applied only for cilium")
	f.BoolVar(&installerRunOpts.NoPolicies, "no-cilium-policies", false, "skip Ichor's CiliumNetworkPolicies even on Cilium (operator opt-out)")
	_ = installerRunCmd.MarkFlagRequired("run-id")
}

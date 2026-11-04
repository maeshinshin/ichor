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

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
)

// writesAnnotation marks a command that changes the world. Only these are refused when the
// CLI and the server are more than one minor apart (design, "Upgrades"): a read
// of a newer or older server is at worst incomplete, a write can be wrong.
const writesAnnotation = "ichor.maesh.dev/writes"

// maxMinorSkew is how far apart the CLI and the server may be for a write.
const maxMinorSkew = 1

func markWrites(cmds ...*cobra.Command) {
	for _, c := range cmds {
		if c.Annotations == nil {
			c.Annotations = map[string]string{}
		}

		c.Annotations[writesAnnotation] = "true"
	}
}

// checkServerSkew refuses when the server's reported version (IchorInstallation
// status.installedVersion, written by the controller itself) is more than maxMinorSkew minors
// from cliVersion. It only ever refuses on a positive finding: no IchorInstallation, no
// reported version, an unreadable cluster or a "dev" build all mean "cannot judge", and the
// command goes on to report its own connection problems.
func checkServerSkew(ctx context.Context, c client.Reader, cliVersion string) error {
	var inst ichorv1alpha1.IchorInstallation
	if err := c.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
		return nil //nolint:nilerr // cannot judge; see the doc comment
	}

	distance, ok := version.MinorDistance(cliVersion, inst.Status.InstalledVersion)
	if !ok || distance <= maxMinorSkew {
		return nil
	}

	return fmt.Errorf("this ichor CLI is %s and the server runs %s, %d minor versions apart; writes are refused "+
		"beyond %d (reads still work). Use the CLI of the server's version, or upgrade Ichor first",
		cliVersion, inst.Status.InstalledVersion, distance, maxMinorSkew)
}

// skewPreRun is the root's PersistentPreRunE.
func skewPreRun(cmd *cobra.Command, _ []string) error {
	if cmd.Annotations[writesAnnotation] != "true" {
		return nil
	}

	c, err := newKubeClient(kubeconfigPath)
	if err != nil {
		return nil //nolint:nilerr // the command itself reports a missing kubeconfig, with its own wording
	}

	return checkServerSkew(cmd.Context(), c, version.Version)
}

func init() {
	markWrites(machineApproveCmd, machineRejectCmd, patchCreateCmd, patchUpdateCmd, patchDeleteCmd)

	rootCmd.PersistentPreRunE = skewPreRun
	rootCmd.Version = clientVersion()
}

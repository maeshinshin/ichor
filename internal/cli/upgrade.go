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
	"strings"

	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
)

// startFlags are the flags every command that starts an operation shares.
type startFlags struct {
	Cluster        string
	MaxUnavailable int32
	Follow         bool
}

var upgradeFlags startFlags

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade Talos, Kubernetes or Ichor itself",
	Long: `Each upgrade changes the version in the spec that names it and then starts the
operation that rolls it out machine by machine: control planes one at a time,
then workers. The spec is changed first, because the operation reads its target
from it; if the operation cannot be created (no permission, another operation
running) nothing was changed. Run the command again to retry: a spec that
already names the version is not written twice.`,
}

func newUpgradeCmd(use, short string, kind pb.OperationKind) *cobra.Command {
	return &cobra.Command{
		Use:   use + " VERSION",
		Short: short,
		Long: short + `.

It shows which machines restart and in which order, and asks for confirmation unless --yes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkUpgradeTarget(args[0]); err != nil {
				return err
			}

			if kind == pb.OperationKind_OPERATION_KIND_ICHOR {
				if err := refuseHelmManagedInstall(cmd.Context(), cmd.ErrOrStderr()); err != nil {
					return err
				}
			}

			return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
				return runStartOperation(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), startSpec{Kind: kind, Cluster: upgradeFlags.Cluster, Target: args[0], MaxUnavailable: upgradeFlags.MaxUnavailable, Follow: upgradeFlags.Follow})
			})
		},
	}
}

// checkUpgradeTarget refuses a target that is not a semantic version before anything is
// written: the spec would keep the bad value, and the controller would misorder it. The "v" is
// optional on the command line, as the server takes it, and is added here the same way.
func checkUpgradeTarget(target string) error {
	return version.CheckSemver("v" + strings.TrimPrefix(target, "v"))
}

var (
	upgradeTalosCmd      = newUpgradeCmd("talos", "Upgrade Talos on every machine of a cluster (each machine reboots)", pb.OperationKind_OPERATION_KIND_TALOS)
	upgradeKubernetesCmd = newUpgradeCmd("kubernetes", "Upgrade Kubernetes on a cluster (no reboot)", pb.OperationKind_OPERATION_KIND_KUBERNETES)
	upgradeIchorCmd      = newUpgradeCmd("ichor", "Upgrade Ichor itself (IchorInstallation.spec.version)", pb.OperationKind_OPERATION_KIND_ICHOR)
)

func addStartFlags(cmd *cobra.Command, f *startFlags) {
	fl := cmd.Flags()
	fl.StringVar(&f.Cluster, "cluster", "", "the cluster (default: the only one in the namespace)")
	fl.Int32Var(&f.MaxUnavailable, "max-unavailable", 0, "workers handled at once (default 1)")
	fl.BoolVar(&f.Follow, "follow", false, "after creating the operation, follow its progress until it finishes")
}

func init() {
	rootCmd.AddCommand(upgradeCmd)
	addAPIFlags(upgradeCmd)
	upgradeCmd.AddCommand(upgradeTalosCmd, upgradeKubernetesCmd, upgradeIchorCmd)

	for _, c := range []*cobra.Command{upgradeTalosCmd, upgradeKubernetesCmd, upgradeIchorCmd} {
		addStartFlags(c, &upgradeFlags)
	}

	markWrites(upgradeTalosCmd, upgradeKubernetesCmd, upgradeIchorCmd)
}

// refuseHelmManagedInstall stops a command that would run the installer Job when Helm owns the
// install (ADR 0104 4.6). Without a usable kubeconfig it cannot judge, and the command goes on
// to report its own connection problems. It also prints the NOTICE about roles that still grant
// the earlier override verb (ADR 0109).
func refuseHelmManagedInstall(ctx context.Context, errOut io.Writer) error {
	c, err := newKubeClient(kubeconfigPath)
	if err != nil {
		return nil //nolint:nilerr // see the doc comment
	}

	if err := refuseHelmManaged(ctx, c); err != nil {
		return err
	}

	noticeEarlierOverrideVerb(ctx, c, errOut)
	noticePendingRenames(ctx, c, errOut)

	return nil
}

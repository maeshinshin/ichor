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
	"io"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
	"github.com/maeshinshin/ichor/pkg/release"
)

// startFlags are the flags every command that starts an operation shares.
type startFlags struct {
	Cluster        string
	MaxUnavailable int32
	Follow         bool
}

var upgradeFlags startFlags

// upgradeList is --list on `upgrade talos` and `upgrade kubernetes`: print the versions the cluster
// can move to and exit.
var upgradeList bool

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade the cluster's Talos or Kubernetes, or Ichor itself",
	Long: `Upgrade one component of the cluster, or Ichor itself. Talos, Kubernetes and Cilium move
independently: the release set of the Ichor that runs the cluster is the version of a
first install, and any valid target is allowed after that.

` + "`ichor upgrade talos`" + ` and ` + "`ichor upgrade kubernetes`" + ` offer the versions the cluster can
move to (` + "`--list`" + `). Without a VERSION they use the recommended one, the release set's version
when it is a valid upgrade, else the newest patch of the running minor. With a VERSION they move to it.
` + "`ichor upgrade ichor`" + ` moves Ichor itself.

Each upgrade changes the version in the spec that names it and then starts the
operation that rolls it out machine by machine: control planes one at a time,
then workers. The spec is changed first, because the operation reads its target
from it; if the operation cannot be created (no permission, another operation
running) nothing was changed. Run the command again to retry: a spec that
already names the version is not written twice.`,
}

// newUpgradeCmd is `ichor upgrade <kind> [VERSION]`. Talos and Kubernetes take the version from the
// server's list when none is named; Ichor itself needs one.
func newUpgradeCmd(use, short string, kind pb.OperationKind) *cobra.Command {
	optional := kind == pb.OperationKind_OPERATION_KIND_TALOS || kind == pb.OperationKind_OPERATION_KIND_KUBERNETES

	usage, args := use+" VERSION", cobra.ExactArgs(1)
	if optional {
		usage, args = use+" [VERSION]", cobra.MaximumNArgs(1)
	}

	return &cobra.Command{
		Use:   usage,
		Short: short,
		Long: short + `.

Without VERSION it uses the recommended version (see ` + "`--list`" + `). It shows which machines restart
and in which order, and asks for confirmation unless --yes.`,
		Args: args,
		RunE: func(cmd *cobra.Command, args []string) error {
			var target string
			if len(args) == 1 {
				target = args[0]
			}

			if target != "" {
				if err := checkUpgradeTarget(target); err != nil {
					return err
				}
			}

			if kind == pb.OperationKind_OPERATION_KIND_ICHOR {
				if err := refuseHelmManagedInstall(cmd.Context(), cmd.ErrOrStderr()); err != nil {
					return err
				}
			}

			listOnly := optional && upgradeList

			return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
				if listOnly {
					m, err := upgradeTargets(ctx, c, upgradeFlags.Cluster, kind)
					if err != nil {
						return err
					}

					printUpgradeTargets(cmd.OutOrStdout(), kind, m)

					return nil
				}

				if optional && target == "" {
					t, err := chooseUpgradeTarget(ctx, c, cmd.ErrOrStderr(), kind, upgradeFlags.Cluster)
					if err != nil {
						return err
					}

					target = t
				}

				return runStartOperation(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), startSpec{Kind: kind, Cluster: upgradeFlags.Cluster, Target: target, MaxUnavailable: upgradeFlags.MaxUnavailable, Follow: upgradeFlags.Follow, Override: overrideOf(apiOpts)})
			})
		},
	}
}

// upgradeTargets asks the server which versions the cluster's Talos or Kubernetes can move to.
func upgradeTargets(ctx context.Context, c *apiClients, cluster string, kind pb.OperationKind) (*pb.ListUpgradeTargetsResponse, error) {
	ref, err := resolveCluster(ctx, c, cluster)
	if err != nil {
		return nil, err
	}

	resp, err := c.Operation.ListUpgradeTargets(ctx, connect.NewRequest(&pb.ListUpgradeTargetsRequest{Cluster: ref, Kind: kind}))
	if err != nil {
		return nil, err
	}

	return resp.Msg, nil
}

// chooseUpgradeTarget is the version of an upgrade the user did not name: the server's default, and
// the choices with an error when there is none.
func chooseUpgradeTarget(ctx context.Context, c *apiClients, errw io.Writer, kind pb.OperationKind, cluster string) (string, error) {
	m, err := upgradeTargets(ctx, c, cluster, kind)
	if err != nil {
		return "", err
	}

	if d := m.GetDefault(); d != "" {
		how := "the newest listed"
		if d == pinnedOf(kind) {
			how = "the release set's version"
		}

		_, _ = fmt.Fprintf(errw, "using %s (%s)\n", d, how)

		return d, nil
	}

	printUpgradeTargets(errw, kind, m)

	return "", fmt.Errorf("no %s version to upgrade to by default: name one, as `ichor upgrade %s VERSION` (see --list)",
		kindName(kind), kindName(kind))
}

// printUpgradeTargets writes the groups of versions, newest first, and marks the default. A list that
// cannot be given says why.
func printUpgradeTargets(w io.Writer, kind pb.OperationKind, m *pb.ListUpgradeTargetsResponse) {
	if !m.GetAvailable() {
		_, _ = fmt.Fprintf(w, "no list of %s versions: %s\n", kindName(kind), m.GetUnavailableReason())
	}

	n := 0

	group := func(label string, versions []string) {
		for _, v := range versions {
			mark := ""
			if v == m.GetDefault() {
				mark = "  (default)"
			}

			_, _ = fmt.Fprintf(w, "  %-12s %s%s\n", label, v, mark)
			n++
		}
	}

	group("recommended", m.GetRecommended())
	group("patch", m.GetPatch())
	group("next minor", m.GetNextMinor())

	if n == 0 {
		_, _ = fmt.Fprintln(w, "  no version to move to")
	}
}

// pinnedOf is the release set's version of a kind that has one ("" for the others).
func pinnedOf(kind pb.OperationKind) string {
	switch kind {
	case pb.OperationKind_OPERATION_KIND_TALOS:
		return release.Pinned.Talos
	case pb.OperationKind_OPERATION_KIND_KUBERNETES:
		return release.Pinned.Kubernetes
	default:
		return ""
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

	for _, c := range []*cobra.Command{upgradeTalosCmd, upgradeKubernetesCmd} {
		c.Flags().BoolVar(&upgradeList, "list", false, "print the versions the cluster can move to, the recommended one first, and exit")
	}

	markWrites(upgradeTalosCmd, upgradeKubernetesCmd, upgradeIchorCmd)
}

// refuseHelmManagedInstall stops a command that would run the installer Job when Helm owns the
// install. Without a usable kubeconfig it cannot judge, and the command goes on
// to report its own connection problems. It also prints the NOTICE about roles that still grant
// the earlier override verb.
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

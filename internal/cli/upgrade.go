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
	"errors"
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

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade the cluster to the release set of this Ichor, or Talos, Kubernetes or Ichor itself",
	Long: `The cluster's Talos, Kubernetes and Cilium versions are the release set of the
Ichor that runs them (docs/adr/0113). ` + "`ichor upgrade cluster`" + ` brings a cluster there, one step at a
time. ` + "`ichor upgrade talos`" + ` and ` + "`ichor upgrade kubernetes`" + ` run one of those steps on its own; their
version is the release set's, and any other is refused. ` + "`ichor upgrade ichor`" + ` moves Ichor itself.

Each upgrade changes the version in the spec that names it and then starts the
operation that rolls it out machine by machine: control planes one at a time,
then workers. The spec is changed first, because the operation reads its target
from it; if the operation cannot be created (no permission, another operation
running) nothing was changed. Run the command again to retry: a spec that
already names the version is not written twice.`,
}

// newUpgradeCmd is `ichor upgrade <kind> [VERSION]`. A kind whose version the release set pins
// (pinned non-empty) takes the pinned version by default and refuses any other (ADR 0113).
func newUpgradeCmd(use, short string, kind pb.OperationKind, pinned string) *cobra.Command {
	usage, args := use+" VERSION", cobra.ExactArgs(1)
	if pinned != "" {
		usage, args = use+" [VERSION]", cobra.MaximumNArgs(1)
	}

	return &cobra.Command{
		Use:   usage,
		Short: short,
		Long: short + `.

It shows which machines restart and in which order, and asks for confirmation unless --yes.`,
		Args: args,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := pinned
			if len(args) == 1 {
				target = args[0]
			}

			if pinned != "" {
				if err := checkPinnedTarget(kind, target); err != nil {
					return err
				}
			} else if err := checkUpgradeTarget(target); err != nil {
				return err
			}

			if kind == pb.OperationKind_OPERATION_KIND_ICHOR {
				if err := refuseHelmManagedInstall(cmd.Context(), cmd.ErrOrStderr()); err != nil {
					return err
				}
			}

			return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
				return runStartOperation(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), startSpec{Kind: kind, Cluster: upgradeFlags.Cluster, Target: target, MaxUnavailable: upgradeFlags.MaxUnavailable, Follow: upgradeFlags.Follow})
			})
		},
	}
}

// checkPinnedTarget refuses a Talos or Kubernetes target that is not the release set's version
// (ADR 0113); the server refuses it too, before anything is written.
func checkPinnedTarget(kind pb.OperationKind, target string) error {
	switch kind {
	case pb.OperationKind_OPERATION_KIND_TALOS:
		return release.CheckTarget("Talos", "v"+strings.TrimPrefix(target, "v"), release.Pinned.Talos)
	case pb.OperationKind_OPERATION_KIND_KUBERNETES:
		return release.CheckTarget("Kubernetes", "v"+strings.TrimPrefix(target, "v"), release.Pinned.Kubernetes)
	default:
		return nil
	}
}

// checkUpgradeTarget refuses a target that is not a semantic version before anything is
// written: the spec would keep the bad value, and the controller would misorder it. The "v" is
// optional on the command line, as the server takes it, and is added here the same way.
func checkUpgradeTarget(target string) error {
	return version.CheckSemver("v" + strings.TrimPrefix(target, "v"))
}

var (
	upgradeTalosCmd      = newUpgradeCmd("talos", "Upgrade Talos on every machine of a cluster (each machine reboots)", pb.OperationKind_OPERATION_KIND_TALOS, release.Pinned.Talos)
	upgradeKubernetesCmd = newUpgradeCmd("kubernetes", "Upgrade Kubernetes on a cluster (no reboot)", pb.OperationKind_OPERATION_KIND_KUBERNETES, release.Pinned.Kubernetes)
	upgradeIchorCmd      = newUpgradeCmd("ichor", "Upgrade Ichor itself (IchorInstallation.spec.version)", pb.OperationKind_OPERATION_KIND_ICHOR, "")
)

// upgradeClusterFollow is `ichor upgrade cluster --follow`: keep going through the steps.
var upgradeClusterFollow bool

// upgradeClusterCmd brings a cluster to the release set of this Ichor, one step per operation
// (ADR 0113). It is one user action, not automatic: each step is planned, shown and confirmed.
var upgradeClusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Bring the cluster to the versions of this Ichor: Talos, then Kubernetes, then Cilium",
	Long: `Bring the cluster to the release set of this Ichor (Talos ` + release.Pinned.Talos + `, version contract ` +
		release.Pinned.VersionContract + `, Kubernetes ` + release.Pinned.Kubernetes + `, Cilium chart ` +
		release.Pinned.Cilium + `).

The steps run in this order, each one finished before the next starts:

  1. talos       Talos on every machine (each reboots, one at a time). The version contract
                 moves with it. Talos goes first: Kubernetes must be a release the running Talos
                 supports, and Talos does not upgrade Kubernetes by itself.
  2. config      only when a machine has a configuration change the new contract needs; the
                 Kubernetes step refuses while a machine has a change it does not apply.
  3. kubernetes  the control planes, kube-proxy, then the kubelets (no reboot).
  4. cilium      the Cilium release, last, so the new chart meets the Kubernetes it was tested with.

Each run creates the next step's operation only. Without --follow it stops there; with --follow it
keeps going until the cluster runs the release set or a step fails. Downgrades are refused, and
nothing is started while another operation runs on the cluster.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runClusterUpgrade(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), upgradeFlags.Cluster, upgradeClusterFollow)
		})
	},
}

// runClusterUpgrade plans, confirms and starts the next step of the cluster's rollout, and with
// follow waits for it and goes on (ADR 0113). A step that starts a second time in one run means the
// cluster did not move, and the run stops rather than loop.
func runClusterUpgrade(ctx context.Context, c *apiClients, in io.Reader, out, errw io.Writer, cluster string, follow bool) error {
	ref, err := resolveCluster(ctx, c, cluster)
	if err != nil {
		return err
	}

	started := map[string]bool{}

	for {
		plan, err := c.Operation.PlanClusterUpgrade(ctx, connect.NewRequest(&pb.PlanClusterUpgradeRequest{Cluster: ref}))
		if err != nil {
			return err
		}

		if len(plan.Msg.GetSteps()) == 0 {
			_, _ = fmt.Fprintf(errw, "ichor: cluster %s already runs the release set of Ichor %s: nothing to upgrade\n",
				ref.GetName(), version.Version)

			return nil
		}

		if b := plan.Msg.GetBlocked(); b != "" {
			return errors.New(b)
		}

		if err := confirmPlan(in, errw, "upgrade cluster", clusterPlanText(ref.GetName(), plan.Msg)); err != nil {
			return err
		}

		resp, err := c.Operation.StartClusterUpgrade(ctx, connect.NewRequest(&pb.StartClusterUpgradeRequest{Cluster: ref}))
		if err != nil {
			return err
		}

		step := resp.Msg.GetStep()
		if started[step] {
			return fmt.Errorf("upgrade cluster: step %s started again but the cluster did not move on: see its operation", step)
		}

		started[step] = true

		if !follow {
			return writeMessage(out, outputFormat, resp.Msg)
		}

		_, _ = fmt.Fprintf(errw, "created operation %s (step %s)\n", resp.Msg.GetOperation().GetName(), step)

		if err := watchOperation(ctx, c, resp.Msg.GetOperation(), out, errw); err != nil {
			return err
		}
	}
}

// clusterPlanText is what `upgrade cluster` shows before it starts the next step: the whole rollout's
// steps, and which one starts now.
func clusterPlanText(cluster string, p *pb.PlanClusterUpgradeResponse) string {
	var b strings.Builder

	rs := p.GetReleaseSet()
	fmt.Fprintf(&b, "Bring cluster %q to the release set of Ichor %s: Talos %s, version contract %s, Kubernetes %s, Cilium %s.\n",
		cluster, version.Version, rs.GetTalosVersion(), rs.GetVersionContract(), rs.GetKubernetesVersion(), rs.GetCiliumChartVersion())

	for i, s := range p.GetSteps() {
		line := ""

		switch s.GetStep() {
		case "talos":
			line = fmt.Sprintf("Talos %s -> %s: each machine is upgraded and REBOOTS, one at a time; the version contract moves to %s.",
				s.GetFrom(), s.GetTo(), rs.GetVersionContract())
		case "config":
			line = "Configuration: the pending change the new contract needs is applied; a machine that needs a reboot REBOOTS."
		case "kubernetes":
			line = fmt.Sprintf("Kubernetes %s -> %s: restarts the control plane components and kubelets, no reboot.", s.GetFrom(), s.GetTo())
		case "cilium":
			line = fmt.Sprintf("Cilium %s -> %s: the Cilium Helm release is upgraded.", s.GetFrom(), s.GetTo())
		}

		fmt.Fprintf(&b, "  %d. %s\n", i+1, line)
	}

	fmt.Fprintf(&b, "This starts step 1 only (%s). ", p.GetSteps()[0].GetStep())
	if upgradeClusterFollow {
		b.WriteString("With --follow the next steps follow when each one finishes.\n")
	} else {
		b.WriteString("Run the command again for the next step, or use --follow to go through all of them.\n")
	}

	return b.String()
}

func addStartFlags(cmd *cobra.Command, f *startFlags) {
	fl := cmd.Flags()
	fl.StringVar(&f.Cluster, "cluster", "", "the cluster (default: the only one in the namespace)")
	fl.Int32Var(&f.MaxUnavailable, "max-unavailable", 0, "workers handled at once (default 1)")
	fl.BoolVar(&f.Follow, "follow", false, "after creating the operation, follow its progress until it finishes")
}

func init() {
	rootCmd.AddCommand(upgradeCmd)
	addAPIFlags(upgradeCmd)
	upgradeCmd.AddCommand(upgradeClusterCmd, upgradeTalosCmd, upgradeKubernetesCmd, upgradeIchorCmd)

	for _, c := range []*cobra.Command{upgradeTalosCmd, upgradeKubernetesCmd, upgradeIchorCmd} {
		addStartFlags(c, &upgradeFlags)
	}

	upgradeClusterCmd.Flags().StringVar(&upgradeFlags.Cluster, "cluster", "", "the cluster (default: the only one in the namespace)")
	upgradeClusterCmd.Flags().BoolVar(&upgradeClusterFollow, "follow", false, "keep going through the steps until the cluster runs the release set")

	markWrites(upgradeClusterCmd, upgradeTalosCmd, upgradeKubernetesCmd, upgradeIchorCmd)
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

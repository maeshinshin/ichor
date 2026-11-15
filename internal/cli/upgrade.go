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
	"regexp"
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
	Use:         "upgrade",
	GroupID:     groupMaintain,
	RunE:        groupRunE,
	Annotations: groupingAnnotations(),
	Short:       "Upgrade the cluster's Talos, Kubernetes or Cilium, or Ichor itself",
	Example: `  ichor upgrade talos --list
  ichor upgrade kubernetes v1.36.3 --cluster prod --follow`,
	Long: `Upgrade one component of the cluster, or Ichor itself. Talos, Kubernetes and Cilium move
independently: the release set of the Ichor that runs the cluster is the version of a
first install, and any valid target is allowed after that.

` + "`ichor upgrade talos`" + ` and ` + "`ichor upgrade kubernetes`" + ` offer the versions the cluster can
move to (` + "`--list`" + `). Without a VERSION they use the recommended one, the release set's version
when it is a valid upgrade, else the newest patch of the running minor. With a VERSION they move to it.
` + "`ichor upgrade ichor`" + ` moves Ichor itself and ` + "`ichor upgrade cni`" + ` moves Cilium to the chart this Ichor ships. Before the installer Job runs, the controller pins the
installer image to a digest and checks its signature; --skip-signature-check is the opt-out for
images nobody signs, and it is recorded in the IchorInstallation, not applied to one run only
(see ` + "`ichor upgrade ichor --help`" + `).

Each upgrade changes the version in the spec that names it and then starts the
operation that rolls it out node by node: control planes one at a time,
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

	// Only Talos and Kubernetes have a recommended version and --list; Ichor's VERSION is required.
	versionHelp := "VERSION is required."
	if optional {
		versionHelp = "Without VERSION it uses the recommended version (see `--list`)."
	}

	return &cobra.Command{
		Use:   usage,
		Short: short,
		Long: short + ".\n\n" + versionHelp + ` It shows which nodes restart
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
				if err := checkInstallerFlags(); err != nil {
					return err
				}

				if err := refuseHelmManagedInstall(cmd.Context(), cmd.ErrOrStderr()); err != nil {
					return err
				}
			}

			listOnly := optional && upgradeList

			start := func() error {
				return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
					if listOnly {
						m, err := upgradeTargets(ctx, c, upgradeFlags.Cluster, kind)
						if err != nil {
							return err
						}

						return printUpgradeTargets(cmd.OutOrStdout(), kind, m)
					}

					if optional && target == "" {
						t, err := chooseUpgradeTarget(ctx, c, cmd.ErrOrStderr(), kind, upgradeFlags.Cluster)
						if err != nil {
							return err
						}

						target = t
					}

					return runStartOperation(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), startSpec{Kind: kind, Cluster: upgradeFlags.Cluster, Target: target, MaxUnavailable: upgradeFlags.MaxUnavailable, Follow: upgradeFlags.Follow, Override: overrideOf(apiOpts), Installer: installerOptionsFor(kind)})
				})
			}

			if kind != pb.OperationKind_OPERATION_KIND_ICHOR || listOnly {
				return start()
			}

			// The Ichor this installs does not start without a sign-in: set it up first, and show the password last.
			k, err := newKubeClient(kubeconfigPath)
			if err != nil {
				return fmt.Errorf("upgrade: building the Kubernetes client: %w", err)
			}

			return upgradeWithSignIn(cmd.Context(), k, cmd.InOrStdin(), cmd.ErrOrStderr(), upgradeSignIn, assumeYes, start)
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

	_ = renderUpgradeTargets(newHuman(errw), kind, m)

	return "", fmt.Errorf("no %s version to upgrade to by default: name one, as `ichor upgrade %s VERSION` (see --list)",
		kindName(kind), kindName(kind))
}

// printUpgradeTargets writes the versions the cluster can move to: a table for a person, the wire
// message for -o yaml|json.
func printUpgradeTargets(w io.Writer, kind pb.OperationKind, m *pb.ListUpgradeTargetsResponse) error {
	return printAPI(w, m, func(h *human, m *pb.ListUpgradeTargetsResponse) error { return renderUpgradeTargets(h, kind, m) })
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

// upgradeInstallerDigest and upgradeSkipSignature are the installer image flags of `ichor upgrade ichor`.
var (
	upgradeInstallerDigest string
	upgradeSkipSignature   bool
)

var (
	upgradeTalosCmd      = newUpgradeCmd("talos", "Upgrade Talos on every node of a cluster (each node reboots)", pb.OperationKind_OPERATION_KIND_TALOS)
	upgradeKubernetesCmd = newUpgradeCmd("kubernetes", "Upgrade Kubernetes on a cluster (no reboot)", pb.OperationKind_OPERATION_KIND_KUBERNETES)
	upgradeIchorCmd      = newUpgradeCmd("ichor", "Upgrade Ichor itself (IchorInstallation.spec.version)", pb.OperationKind_OPERATION_KIND_ICHOR)
)

// skipSignatureUsage and skipSignatureNote are the help of --skip-signature-check on `ichor upgrade ichor` and
// `ichor init`; each command adds the note to its own Long. The skip is a spec change, not a one-shot bypass, and
// the help says so. skipSignatureUpgradeNote is only true of an upgrade, which rewrites the installer settings.
const (
	skipSignatureUsage = "run the installer image WITHOUT checking its signature (for builds nobody signs: development and demo images); " +
		"not one-shot: recorded as IchorInstallation spec.installerSignature.mode skip, it stays until an upgrade without the flag"

	skipSignatureNote = "--skip-signature-check does not apply to one run only. It writes spec.installerSignature.mode: skip into the\n" +
		"IchorInstallation \"ichor\" and leaves it there; while the skip stands it also governs the Cilium Helm Job. The next\n" +
		"`ichor upgrade ichor` without the flag checks the signature again. To undo the skip without upgrading, run\n" +
		"`kubectl edit ichorinstallation ichor` and delete spec.installerSignature (or set mode: required); that needs the update\n" +
		"verb on ichorinstallations/version, the right an upgrade needs."

	skipSignatureUpgradeNote = "The Job still runs by digest: an upgrade writes spec.installerDigest from --installer-digest and clears it when the flag\n" +
		"is not given, and the controller then resolves the version's tag to a digest itself."
)

var installerDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// checkInstallerFlags refuses installer image flags the server would refuse, before anything is reached.
// --skip-signature-check is a bare flag: the skip is shown and recorded, no reason is asked for.
func checkInstallerFlags() error {
	if upgradeInstallerDigest != "" && !installerDigestPattern.MatchString(upgradeInstallerDigest) {
		return fmt.Errorf("--installer-digest %q is not sha256: followed by 64 lowercase hex characters", upgradeInstallerDigest)
	}

	return nil
}

// installerOptionsFor is the installer image settings of an `ichor upgrade ichor`, nil for the other kinds.
// An Ichor upgrade always sends them, so a pin or a skip from an earlier upgrade is cleared unless it is given again.
func installerOptionsFor(kind pb.OperationKind) *pb.IchorInstallerOptions {
	if kind != pb.OperationKind_OPERATION_KIND_ICHOR {
		return nil
	}

	return &pb.IchorInstallerOptions{Digest: upgradeInstallerDigest, SkipSignature: upgradeSkipSignature}
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
	upgradeCmd.AddCommand(upgradeTalosCmd, upgradeKubernetesCmd, upgradeIchorCmd)

	for _, c := range []*cobra.Command{upgradeTalosCmd, upgradeKubernetesCmd, upgradeIchorCmd} {
		addStartFlags(c, &upgradeFlags)
	}

	for _, c := range []*cobra.Command{upgradeTalosCmd, upgradeKubernetesCmd} {
		c.Flags().BoolVar(&upgradeList, "list", false, "print the versions the cluster can move to, the recommended one first, and exit")
	}

	upgradeIchorCmd.Flags().StringVar(&upgradeInstallerDigest, "installer-digest", "",
		"pin the installer image to this digest (sha256:...) instead of resolving the version's tag")
	upgradeIchorCmd.Flags().BoolVar(&upgradeSkipSignature, "skip-signature-check", false, skipSignatureUsage)
	upgradeIchorCmd.Long += "\n\n" + skipSignatureNote + "\n" + skipSignatureUpgradeNote

	f := upgradeIchorCmd.Flags()
	f.StringVar(&upgradeSignIn.UIURL, "ui-url", "",
		"if ichor-ui has no sign-in yet: the https URL at which browsers and the CLI reach it, the issuer of every token (default "+defaultUIURL+")")
	f.StringVar(&upgradeSignIn.AdminUser, "admin-user", "",
		"if ichor-ui has no sign-in yet: name of the first administrator (default "+defaultAdminUser+"); its generated password is printed once to stderr")
	f.StringVar(&upgradeSignIn.UITLSSecret, "ui-tls-secret", "", "if ichor-ui has no sign-in yet: serve it over TLS with this existing kubernetes.io/tls Secret")
	f.StringVar(&upgradeSignIn.UITLSCert, "ui-tls-cert", "", "if ichor-ui has no sign-in yet: PEM certificate file for its TLS; stored with --ui-tls-key as a Secret")
	f.StringVar(&upgradeSignIn.UITLSKey, "ui-tls-key", "", "with --ui-tls-cert: PEM private key file (never printed)")
	f.StringVar(&upgradeSignIn.KubeAPIServerURL, "kube-api-server-url", "", "if ichor-ui has no sign-in yet: https URL of the Kubernetes API put in users' kubeconfigs")
	f.StringVar(&upgradeSignIn.KubeAPITLSServerName, "kube-api-tls-server-name", "", "if ichor-ui has no sign-in yet: tls-server-name in users' kubeconfigs")

	markWrites(upgradeTalosCmd, upgradeKubernetesCmd, upgradeIchorCmd)

	upgradeTalosCmd.Example = `  ichor upgrade talos --list
  ichor upgrade talos v1.14.1 --cluster prod --follow`
	upgradeKubernetesCmd.Example = `  ichor upgrade kubernetes --list
  ichor upgrade kubernetes v1.36.3 --cluster prod --follow`
	upgradeIchorCmd.Long += `

The ichor-ui this installs does not start without a sign-in. If yours has none (an install made before
sign-in became the default), the upgrade first turns Ichor's own sign-in on, as ichor init does: it creates the
first administrator, switches ichor-ui to sign-in at --ui-url (default ` + defaultUIURL + `) and sets
spec.authentication.ichor on each TalosCluster that can carry it (Talos v1.14 or later). That last step marks
every control plane APIServerRestartRequired; kube-apiserver restarts on each control plane only when you run
"ichor rollout config". It asks first (--yes skips the question) and prints the generated password once, at the
end. An Ichor that Helm installed is not touched: set ichorUI.users.enabled or ichorUI.oidc in the chart values.`
	upgradeIchorCmd.Example = `  ichor upgrade ichor v0.3.1 --follow`
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

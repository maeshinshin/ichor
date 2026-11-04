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
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var cniFlags startFlags

var cniCmd = &cobra.Command{
	Use:   "cni",
	Short: "Operate on the cluster's CNI settings Ichor owns",

	PersistentPreRunE: cniPreRun,
}

// refuseUnmanagedCNI fails when the cluster's CNI is not managed by Ichor. It is the one check every `ichor cni ...`
// subcommand shares, run before the subcommand.
func refuseUnmanagedCNI(ctx context.Context, c *apiClients, cluster string) error {
	ref, err := resolveCluster(ctx, c, cluster)
	if err != nil {
		return err
	}

	resp, err := c.Cluster.GetCluster(ctx, connect.NewRequest(&pb.GetClusterRequest{Cluster: ref}))
	if err != nil {
		return err
	}

	if resp.Msg.GetCluster().GetCniPreset() == ichorv1alpha1.TalosClusterCNIPresetUnmanaged {
		return errors.New(ichorv1alpha1.CNINotManagedMessage)
	}

	return nil
}

// cniPreRun is cniCmd's PersistentPreRunE. cobra runs only the nearest hook, so it calls the
// root's skew check itself. The hidden `helm` group is what the Cilium Job runs and never
// reaches an unmanaged cluster. `bootstrap` runs before Ichor exists, so it has no cluster to ask.
func cniPreRun(cmd *cobra.Command, args []string) error {
	if err := skewPreRun(cmd, args); err != nil {
		return err
	}

	for p := cmd; p != nil; p = p.Parent() {
		if p == cniHelmCmd || p == cniBootstrapCmd {
			return nil
		}
	}

	var cluster string
	if f := cmd.Flags().Lookup("cluster"); f != nil {
		cluster = f.Value.String()
	}

	return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
		return refuseUnmanagedCNI(ctx, c, cluster)
	})
}

var cniKubeProxyCmd = &cobra.Command{
	Use:   "kube-proxy replaced|enabled",
	Short: "Switch kube-proxy between enabled and replaced by the CNI",
	Long: `kube-proxy sets spec.cni.kubeProxy and starts the operation that carries the switch out
node by node: the new side comes up and is verified before the old side is turned off.
The admission policy that guards this field wants a confirmation annotation; the
server sets it (to the new mode) for this one write and removes it again, so it never stays behind as
a standing consent. Ichor owns the Cilium release (a Helm release applied by a Job), so the
operation applies the new kube-proxy mode to it itself: replacing starts Cilium's replacement
first and verifies it on every node before kube-proxy is turned off; enabling restores
kube-proxy first and then switches Cilium's replacement off. Writing spec.cni.kubeProxy alone
changes nothing in Cilium; only this operation does.

It shows which machines are touched and in which order, and asks for confirmation unless --yes.`,
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{"replaced", "enabled"},
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			f := cniFlags

			return runStartOperation(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
				startSpec{Kind: pb.OperationKind_OPERATION_KIND_CNI, Cluster: f.Cluster, Target: args[0], MaxUnavailable: f.MaxUnavailable, Follow: f.Follow})
		})
	},
}

var (
	// cniCluster is --cluster on the values and upgrade commands.
	cniCluster string
	// cniValuesFile is --filename on set and diff.
	cniValuesFile string
	// cniLayer is --layer on values get.
	cniLayer string
)

// Layers of `ichor cni values get --layer`.
const (
	cniLayerUser      = "user"
	cniLayerDefaults  = "defaults"
	cniLayerEffective = "effective"
	cniLayerOwned     = "owned"
)

var cniValuesCmd = &cobra.Command{
	Use:   "values",
	Short: "Show, edit and diff Cilium's Helm values (the user layer on the TalosCluster)",
	Long: `Cilium's Helm values are Ichor's defaults, then your layer (spec.cni.cilium.values)
merged on top, then the keys Ichor owns. Only your layer is written; the controller applies it.`,
}

var cniValuesGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Print your layer of Cilium's values (--layer shows the others)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runCniValuesGet(ctx, c, cmd.OutOrStdout(), cniCluster, cniLayer)
		})
	},
}

var cniValuesSetCmd = &cobra.Command{
	Use:   "set -f FILE",
	Short: "Replace your layer of Cilium's values with a YAML file (- is stdin; an empty file clears it)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		text, err := readValuesFile(cmd.InOrStdin(), cniValuesFile)
		if err != nil {
			return err
		}

		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runCniValuesSet(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), cniCluster, text)
		})
	},
}

var cniValuesDiffCmd = &cobra.Command{
	Use:   "diff -f FILE",
	Short: "Show how the effective values would change if FILE became your layer",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		text, err := readValuesFile(cmd.InOrStdin(), cniValuesFile)
		if err != nil {
			return err
		}

		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runCniValuesDiff(ctx, c, cmd.OutOrStdout(), cniCluster, text)
		})
	},
}

var cniValuesEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Edit your layer of Cilium's values in $EDITOR, validate it, show the diff and save",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runCniValuesEdit(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), cniCluster, runEditor)
		})
	},
}

var cniUpgradeList bool

var cniUpgradeCmd = &cobra.Command{
	Use:   "upgrade [VERSION]",
	Short: "Upgrade Cilium to the chart this Ichor ships, or to a verified patch of it",
	Long: `upgrade checks that a newer Cilium chart is available and that your values still
validate against it, then starts the cni upgrade operation.

With VERSION (a patch such as v1.20.5, newer than the chart) the release keeps the embedded chart and
runs that patch's agent, operator and hubble-relay images, pinned by digest. The images must verify
against the Cilium release signature first; a patch that does not is not offered and is refused.
--list prints the patches on offer and exits.

A verified patch the cluster's Kubernetes is not tested with is refused; --override (with the override
RBAC verb on the TalosCluster) lifts that, and --override-reason records why. An unverified patch is
never overridable.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		version := ""
		if len(args) == 1 {
			version = args[0]
		}

		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runCniUpgrade(ctx, c, cmd.OutOrStdout(), cniCluster, version, cniUpgradeList, overrideOf(apiOpts))
		})
	},
}

// readValuesFile reads the YAML at path; - is stdin.
func readValuesFile(stdin io.Reader, path string) (string, error) {
	var (
		raw []byte
		err error
	)

	if path == "-" {
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(path)
	}

	if err != nil {
		return "", fmt.Errorf("cni values: reading %s: %w", path, err)
	}

	return string(raw), nil
}

func runCniValuesGet(ctx context.Context, c *apiClients, out io.Writer, cluster, layer string) error {
	ref, err := resolveCluster(ctx, c, cluster)
	if err != nil {
		return err
	}

	resp, err := c.Cni.GetCiliumValues(ctx, connect.NewRequest(&pb.GetCiliumValuesRequest{Cluster: ref}))
	if err != nil {
		return err
	}

	switch layer {
	case cniLayerUser:
		_, err = io.WriteString(out, resp.Msg.GetUserYaml())
	case cniLayerDefaults:
		_, err = io.WriteString(out, resp.Msg.GetDefaultsYaml())
	case cniLayerEffective:
		_, err = io.WriteString(out, resp.Msg.GetEffectiveYaml())
	case cniLayerOwned:
		for _, o := range resp.Msg.GetOwned() {
			if _, err = fmt.Fprintf(out, "%s: %s  # %s\n", o.GetPath(), o.GetValueYaml(), o.GetReason()); err != nil {
				break
			}
		}
	default:
		return fmt.Errorf("cni values get: --layer %q is not %s, %s, %s or %s", layer, cniLayerUser, cniLayerDefaults, cniLayerEffective, cniLayerOwned)
	}

	return err
}

// cniValidate prints every problem with a proposed layer to errw and returns an error if there is one.
func cniValidate(ctx context.Context, c *apiClients, errw io.Writer, ref *pb.ObjectRef, text string) error {
	v, err := c.Cni.ValidateCiliumValues(ctx, connect.NewRequest(&pb.ValidateCiliumValuesRequest{Cluster: ref, UserYaml: text}))
	if err != nil {
		return err
	}

	if v.Msg.GetValid() {
		return nil
	}

	for _, e := range v.Msg.GetErrors() {
		_, _ = fmt.Fprintf(errw, "  - %s\n", e)
	}

	return fmt.Errorf("cni values: %d problem(s) in the values, nothing was written", len(v.Msg.GetErrors()))
}

func cniDiff(ctx context.Context, c *apiClients, ref *pb.ObjectRef, text string) (string, error) {
	d, err := c.Cni.DiffCiliumValues(ctx, connect.NewRequest(&pb.DiffCiliumValuesRequest{Cluster: ref, UserYaml: text}))
	if err != nil {
		return "", err
	}

	return d.Msg.GetDiff(), nil
}

func runCniValuesDiff(ctx context.Context, c *apiClients, out io.Writer, cluster, text string) error {
	ref, err := resolveCluster(ctx, c, cluster)
	if err != nil {
		return err
	}

	diff, err := cniDiff(ctx, c, ref, text)
	if err != nil {
		return err
	}

	if outputFormat == outputHuman {
		return renderValuesDiff(newHuman(out), diff)
	}

	if diff == "" {
		diff = noValueChange + "\n"
	}

	_, err = io.WriteString(out, diff)

	return err
}

// runCniValuesSet validates text, shows the diff, asks (unless --yes) and writes it.
func runCniValuesSet(ctx context.Context, c *apiClients, in io.Reader, out, errw io.Writer, cluster, text string) error {
	ref, err := resolveCluster(ctx, c, cluster)
	if err != nil {
		return err
	}

	if err := cniValidate(ctx, c, errw, ref, text); err != nil {
		return err
	}

	diff, err := cniDiff(ctx, c, ref, text)
	if err != nil {
		return err
	}

	if diff == "" {
		_, _ = fmt.Fprintln(errw, "No change to the effective values.")
	} else if err := confirmPlan(in, errw, "cni values", "Effect on Cilium's effective values:\n"+diff+"\n"); err != nil {
		return err
	}

	resp, err := c.Cni.SetCiliumValues(ctx, connect.NewRequest(&pb.SetCiliumValuesRequest{Cluster: ref, UserYaml: text}))
	if err != nil {
		return err
	}

	return printAPI(out, resp.Msg, func(h *human, _ *pb.SetCiliumValuesResponse) error { return renderValuesSet(h, ref) })
}

// runEditor opens path in $VISUAL, $EDITOR or vi and waits. A var so a test can stand in for the editor.
var runEditor = func(path string) error {
	editor := cmp.Or(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	parts := strings.Fields(editor)

	cmd := exec.Command(parts[0], append(parts[1:], path)...) //nolint:gosec // the editor is the user's own choice
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	return cmd.Run()
}

// runCniValuesEdit opens the current user layer in the editor, validates what comes back
// (nothing is written on a problem) and saves it through runCniValuesSet.
func runCniValuesEdit(
	ctx context.Context, c *apiClients, in io.Reader, out, errw io.Writer, cluster string, edit func(path string) error,
) error {
	ref, err := resolveCluster(ctx, c, cluster)
	if err != nil {
		return err
	}

	cur, err := c.Cni.GetCiliumValues(ctx, connect.NewRequest(&pb.GetCiliumValuesRequest{Cluster: ref}))
	if err != nil {
		return err
	}

	f, err := os.CreateTemp("", "ichor-cilium-values-*.yaml")
	if err != nil {
		return err
	}

	defer func() { _ = os.Remove(f.Name()) }()

	header := "# Your layer of Cilium's Helm values (spec.cni.cilium.values). Ichor-owned keys cannot be set;\n" +
		"# `ichor cni values get --layer owned` lists them. An empty file clears your layer.\n"

	if _, err := f.WriteString(header + cur.Msg.GetUserYaml()); err != nil {
		_ = f.Close()

		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	if err := edit(f.Name()); err != nil {
		return fmt.Errorf("cni values: editor: %w", err)
	}

	raw, err := os.ReadFile(f.Name())
	if err != nil {
		return err
	}

	text := string(raw)
	if strings.TrimSpace(strings.TrimPrefix(text, header)) == strings.TrimSpace(cur.Msg.GetUserYaml()) {
		_, _ = fmt.Fprintln(errw, "No changes.")

		return nil
	}

	return runCniValuesSet(ctx, c, in, out, errw, cluster, text)
}

// runCniUpgrade starts the Cilium upgrade, or with list prints the patches on offer. version names a patch; empty
// upgrades to the embedded chart.
func runCniUpgrade(ctx context.Context, c *apiClients, out io.Writer, cluster, version string, list bool, bg *pb.Override) error {
	if list {
		m, err := upgradeTargets(ctx, c, cluster, pb.OperationKind_OPERATION_KIND_CNI)
		if err != nil {
			return err
		}

		return printUpgradeTargets(out, pb.OperationKind_OPERATION_KIND_CNI, m)
	}

	ref, err := resolveCluster(ctx, c, cluster)
	if err != nil {
		return err
	}

	resp, err := c.Cni.StartCniUpgrade(ctx, connect.NewRequest(&pb.StartCniUpgradeRequest{Cluster: ref, Version: version, Override: bg}))
	if err != nil {
		return err
	}

	return printAPI(out, resp.Msg, renderCniUpgradeStarted)
}

func init() {
	rootCmd.AddCommand(cniCmd)
	addAPIFlags(cniCmd)
	cniCmd.AddCommand(cniKubeProxyCmd, cniValuesCmd, cniUpgradeCmd)
	addStartFlags(cniKubeProxyCmd, &cniFlags)
	markWrites(cniKubeProxyCmd)

	cniValuesCmd.AddCommand(cniValuesGetCmd, cniValuesSetCmd, cniValuesDiffCmd, cniValuesEditCmd)

	for _, c := range []*cobra.Command{cniValuesGetCmd, cniValuesSetCmd, cniValuesDiffCmd, cniValuesEditCmd, cniUpgradeCmd} {
		c.Flags().StringVar(&cniCluster, "cluster", "", "the cluster (default: the only one in the namespace)")
	}

	cniValuesGetCmd.Flags().StringVar(&cniLayer, "layer", cniLayerUser, "which layer to print: user, defaults, effective or owned")

	for _, c := range []*cobra.Command{cniValuesSetCmd, cniValuesDiffCmd} {
		c.Flags().StringVarP(&cniValuesFile, "filename", "f", "", "YAML file holding your layer of the values; - reads stdin (required)")
		_ = c.MarkFlagRequired("filename")
	}

	cniUpgradeCmd.Flags().BoolVar(&cniUpgradeList, "list", false, "print the Cilium patches on offer and exit")

	markWrites(cniValuesSetCmd, cniValuesEditCmd, cniUpgradeCmd)
}

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
	"fmt"
	"io"
	"os"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	sigsyaml "sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/uiapi"
)

// patchFile is --filename on apply and preview.
var patchFile, patchListCluster string

func patchFileCmd(use, short string, run func(ctx context.Context, c *apiClients, cmd *cobra.Command, p *ichorv1alpha1.TalosConfigPatch) error) *cobra.Command {
	cmd := &cobra.Command{
		Use: use, Short: short, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := readPatchFile(cmd.InOrStdin(), patchFile, apiOpts.Namespace)
			if err != nil {
				return err
			}

			return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error { return run(ctx, c, cmd, p) })
		},
	}
	cmd.Flags().StringVarP(&patchFile, "filename", "f", "", "TalosConfigPatch manifest (YAML or JSON); - reads stdin (required)")
	_ = cmd.MarkFlagRequired("filename")

	return cmd
}

var getPatchesCmd = &cobra.Command{
	Use: "patches", Short: "List patches", Args: cobra.NoArgs,
	Example: `  ichor get patches
  ichor get patches --cluster prod`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runPatchList(ctx, c, cmd.OutOrStdout(), patchListCluster)
		})
	},
}

var describePatchCmd = &cobra.Command{
	Use: "patch NAME", Short: "Show one patch", Args: cobra.ExactArgs(1),
	Example: `  ichor describe patch ingress-sysctls`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return showAPIHuman(cmd, false, func(ctx context.Context, c *apiClients) (*connect.Response[pb.GetPatchResponse], error) {
			return c.Patch.GetPatch(ctx, connect.NewRequest(&pb.GetPatchRequest{Patch: objectRef(apiOpts.Namespace, args[0])}))
		}, renderPatchGet)
	},
}

var deletePatchCmd = &cobra.Command{
	Use: "patch NAME", Short: "Delete a patch", Args: cobra.ExactArgs(1),
	Example: `  ichor delete patch ingress-sysctls`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runPatchDelete(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0])
		})
	},
}

var previewCmd = patchFileCmd("preview -f FILE", "Show a patch's impact (reboot, install-time-only fields) without writing it",
	func(ctx context.Context, c *apiClients, cmd *cobra.Command, p *ichorv1alpha1.TalosConfigPatch) error {
		return runPatchPreview(ctx, c, cmd.OutOrStdout(), p)
	})

// applyCmd is both `ichor apply -f FILE` (a TalosConfigPatch) and the parent of `ichor apply cni-values`.
var applyCmd = patchFileCmd("apply -f FILE", "Create or update a TalosConfigPatch from a file (shows its impact first; confirms if it needs a reboot or reinstall)",
	func(ctx context.Context, c *apiClients, cmd *cobra.Command, p *ichorv1alpha1.TalosConfigPatch) error {
		return runPatchApply(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), p)
	})

// runPatchApply updates the patch when one of that name exists and creates it when none does, the way kubectl apply does.
func runPatchApply(ctx context.Context, c *apiClients, in io.Reader, out, errw io.Writer, p *ichorv1alpha1.TalosConfigPatch) error {
	_, err := c.Patch.GetPatch(ctx, connect.NewRequest(&pb.GetPatchRequest{Patch: objectRef(p.Namespace, p.Name)}))
	if err != nil && connect.CodeOf(err) != connect.CodeNotFound {
		return err
	}

	return runPatchWrite(ctx, c, in, out, errw, p, err == nil)
}

// readPatchFile decodes path ("-" is stdin) into a TalosConfigPatch with
// unknown fields rejected, the same strictness the API server applies on
// the write (a typo is an error here, not a silently dropped field).
func readPatchFile(stdin io.Reader, path, defaultNamespace string) (*ichorv1alpha1.TalosConfigPatch, error) {
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
		return nil, fmt.Errorf("patch: reading %s: %w", path, err)
	}

	var p ichorv1alpha1.TalosConfigPatch
	if err := sigsyaml.UnmarshalStrict(raw, &p); err != nil {
		return nil, fmt.Errorf("patch: decoding %s as a TalosConfigPatch: %w", path, err)
	}

	if p.Kind != "" && p.Kind != "TalosConfigPatch" {
		return nil, fmt.Errorf("patch: %s is a %s, not a TalosConfigPatch", path, p.Kind)
	}

	if p.Name == "" {
		return nil, fmt.Errorf("patch: %s has no metadata.name", path)
	}

	if p.Namespace == "" {
		p.Namespace = defaultNamespace
	}

	return &p, nil
}

func runPatchList(ctx context.Context, c *apiClients, out io.Writer, cluster string) error {
	resp, err := c.Patch.ListPatches(ctx, connect.NewRequest(&pb.ListPatchesRequest{Cluster: clusterFilter(apiOpts.Namespace, cluster)}))
	if err != nil {
		return err
	}

	return printAPI(out, resp.Msg, renderPatchList)
}

func runPatchDelete(ctx context.Context, c *apiClients, in io.Reader, out, errw io.Writer, name string) error {
	if !assumeYes {
		if err := confirmOrAbort(in, errw, "delete patch", fmt.Sprintf("Delete patch %s/%s? The controller will re-render the machines it applied to. [y/N] ", apiOpts.Namespace, name), "aborted"); err != nil {
			return err
		}
	}

	resp, err := c.Patch.DeletePatch(ctx, connect.NewRequest(&pb.DeletePatchRequest{Patch: objectRef(apiOpts.Namespace, name)}))
	if err != nil {
		return err
	}

	return printAPI(out, resp.Msg, func(h *human, _ *pb.DeletePatchResponse) error {
		return doneLine(h, "%s %s %s", verbDelete, kindPatch, nsName(objectRef(apiOpts.Namespace, name)))
	})
}

func runPatchPreview(ctx context.Context, c *apiClients, out io.Writer, p *ichorv1alpha1.TalosConfigPatch) error {
	wire, err := uiapi.PatchToWire(p)
	if err != nil {
		return err
	}

	resp, err := c.Patch.PreviewImpact(ctx, connect.NewRequest(&pb.PreviewImpactRequest{Patch: wire}))
	if err != nil {
		return err
	}

	return printAPI(out, resp.Msg, renderPatchImpact)
}

// runPatchWrite is create (update=false) or update (update=true). It
// previews first and asks before a write that needs a reboot, touches an
// install-time-only field (a reset and reinstall) or one this cluster's
// Talos is too old for -- the same impact the UI shows before saving.
func runPatchWrite(
	ctx context.Context, c *apiClients, in io.Reader, out, errw io.Writer, p *ichorv1alpha1.TalosConfigPatch, update bool,
) error {
	wire, err := uiapi.PatchToWire(p)
	if err != nil {
		return err
	}

	impact, err := c.Patch.PreviewImpact(ctx, connect.NewRequest(&pb.PreviewImpactRequest{Patch: wire}))
	if err != nil {
		return err
	}

	warn := impactWarning(impact.Msg)

	// stderr, so -o yaml|json on stdout stays the written object alone.
	_, _ = fmt.Fprintf(errw, "Impact of patch %s/%s: %s\n", p.Namespace, p.Name, cmp.Or(warn, "no reboot needed; nothing install-time-only or too new for the cluster's Talos."))

	if warn != "" && !assumeYes {
		if err := confirmOrAbort(in, errw, "patch", "Continue? [y/N] ", "aborted"); err != nil {
			return err
		}
	}

	if update {
		resp, err := c.Patch.UpdatePatch(ctx, connect.NewRequest(&pb.UpdatePatchRequest{Patch: wire}))
		if err != nil {
			return err
		}

		return printAPI(out, resp.Msg, func(h *human, _ *pb.UpdatePatchResponse) error {
			return doneLine(h, "%s %s %s", verbUpdate, kindPatch, nsName(wire.GetRef()))
		})
	}

	resp, err := c.Patch.CreatePatch(ctx, connect.NewRequest(&pb.CreatePatchRequest{Patch: wire}))
	if err != nil {
		return err
	}

	return printAPI(out, resp.Msg, func(h *human, _ *pb.CreatePatchResponse) error {
		return doneLine(h, "%s %s %s", verbCreate, kindPatch, nsName(wire.GetRef()))
	})
}

// impactWarning is "" when the patch needs nothing special.
func impactWarning(r *pb.PreviewImpactResponse) string {
	var parts []string

	if r.GetRebootRequired() {
		parts = append(parts, "applying it needs a reboot.")
	}

	if len(r.GetInstallTimeOnly()) > 0 {
		parts = append(parts, "these fields only take effect at install time (a reset and reinstall): "+strings.Join(r.GetInstallTimeOnly(), ", ")+".")
	}

	if len(r.GetUnsupported()) > 0 {
		parts = append(parts, "these fields need a newer Talos than the cluster is pinned to: "+strings.Join(r.GetUnsupported(), ", ")+".")
	}

	return strings.Join(parts, " ")
}

func init() {
	getPatchesCmd.Flags().StringVar(&patchListCluster, flagCluster, "", "only patches of this TalosCluster")

	for _, c := range []*cobra.Command{getPatchesCmd, describePatchCmd, deletePatchCmd, previewCmd, applyCmd} {
		addAPIFlags(c)
	}

	previewCmd.GroupID = groupInspect
	previewCmd.Example = `  ichor preview -f ingress-sysctls.yaml
  cat ingress-sysctls.yaml | ichor preview -f -`

	applyCmd.GroupID = groupChange
	applyCmd.Long = `apply writes the TalosConfigPatch in FILE: it creates the patch when none of that name exists in the
namespace and replaces its spec when one does. It shows the impact first (a reboot, a reset and
reinstall for install-time-only fields, fields too new for the cluster's Talos) and asks for confirmation
when there is any, unless --yes. - reads the manifest from stdin.

The manifest is a typed TalosConfigPatch: spec.documents[] holds Talos machine configuration documents.

apply cni-values writes your layer of Cilium's Helm values instead.`
	applyCmd.Example = `  ichor apply -f ingress-sysctls.yaml
  ichor apply -f ingress-sysctls.yaml --yes`

	getCmd.AddCommand(getPatchesCmd)
	describeCmd.AddCommand(describePatchCmd)
	deleteCmd.AddCommand(deletePatchCmd)
}

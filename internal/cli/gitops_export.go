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

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/gitops"
	"github.com/maeshinshin/ichor/pkg/gitopstracking"
)

// exportOpts backs `ichor gitops export`'s flags.
var exportOpts gitopsExportOptions

// gitopsExportOptions is runGitopsExport's input, kept as its own type
// (rather than loose parameters) the same way initOptions is, so a test
// can build one directly without going through cobra.
type gitopsExportOptions struct {
	Cluster         string
	Namespace       string
	Dir             string
	ArgoCDNamespace string
	RepoURL         string
	RepoPath        string
}

// exportCmd implements `ichor gitops export` (docs/design/architecture.md,
// "GitOps and Argo CD", "Repository skeleton"): it writes the CRs that
// belong in Git for an existing cluster -- the TalosCluster, its approved
// TalosMachines, TalosConfigPatches and TalosStorages, and the CNI's own
// Application -- to a local directory, the same skeleton `ichor init
// --gitops-dir` writes for a brand-new one.
//
// What never lands in the skeleton: a Secret (pkg/gitops.WriteObject
// refuses one outright), an unapproved TalosMachine (not yet an operator
// decision, so not yet part of the cluster's committed state), and
// anything else this command simply never lists -- new CRD kinds (such as
// a TalosOperation, imperative by nature) are excluded by never being
// read in the first place, not by a deny-list naming them.
var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Write the cluster's CRs as a GitOps repository skeleton",
	Long: `export writes the subset of an existing cluster's CRs that belongs in Git
-- the TalosCluster, its approved TalosMachines, TalosConfigPatches and
TalosStorages, and the CNI's own Argo CD Application -- to --dir, in the
same bootstrap/, apps/, clusters/<cluster>/ layout 'ichor init --gitops-dir'
produces for a brand-new cluster.

It never writes a Secret, never writes an unapproved TalosMachine, and
warns (without exporting it) if an unapproved TalosMachine already appears
to be tracked by Argo CD or Flux outside of Ichor.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		k8sClient, err := newKubeClient(kubeconfigPath)
		if err != nil {
			return fmt.Errorf("gitops export: building the Kubernetes client: %w", err)
		}

		return runGitopsExport(cmd.Context(), exportOpts, k8sClient, cmd.ErrOrStderr())
	},
}

func init() {
	gitopsCmd.AddCommand(exportCmd)

	flags := exportCmd.Flags()
	flags.StringVar(&exportOpts.Cluster, flagCluster, "", "name of the TalosCluster to export (required)")
	flags.StringVarP(&exportOpts.Namespace, "namespace", "n", ichorControllerNamespace,
		"namespace the TalosCluster and its TalosMachines/TalosConfigPatches/TalosStorages live in")
	flags.StringVar(&exportOpts.Dir, "dir", "", "local directory to write the GitOps repository skeleton to (required)")
	flags.StringVar(&exportOpts.ArgoCDNamespace, "argocd-namespace", "argocd",
		"namespace Argo CD runs in, used only to read argocd-cm when checking whether a resource is already tracked")
	flags.StringVar(&exportOpts.RepoURL, "repo", "", "Git repository URL; if set, also writes a root Application to bootstrap/root-app.yaml")
	flags.StringVar(&exportOpts.RepoPath, "path", "", "path within --repo for this cluster's CRs (defaults to clusters/<cluster>)")

	for _, name := range []string{flagCluster, "dir"} {
		_ = exportCmd.MarkFlagRequired(name)
	}
}

// runGitopsExport is exportCmd's RunE, factored out so a test can call it
// directly against a fake client instead of going through cobra.
func runGitopsExport(ctx context.Context, opts gitopsExportOptions, k8sClient client.Client, warn io.Writer) error {
	var cluster ichorv1alpha1.TalosCluster
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: opts.Namespace, Name: opts.Cluster}, &cluster); err != nil {
		return fmt.Errorf("gitops export: fetching TalosCluster %s/%s: %w", opts.Namespace, opts.Cluster, err)
	}

	crs := map[string]any{"talos-cluster.yaml": &cluster}

	if err := exportMachines(ctx, k8sClient, opts, crs, warn); err != nil {
		return err
	}

	if err := exportConfigPatches(ctx, k8sClient, opts, crs); err != nil {
		return err
	}

	if err := exportStorages(ctx, k8sClient, opts, crs); err != nil {
		return err
	}

	cniApp, err := exportCNIApplication(&cluster)
	if err != nil {
		return err
	}

	var rootApp *gitops.Application

	if opts.RepoURL != "" {
		path := opts.RepoPath
		if path == "" {
			path = "clusters/" + opts.Cluster
		}

		rootApp, err = buildRootApplication(opts.RepoURL, path)
		if err != nil {
			return fmt.Errorf("gitops export: building root Application: %w", err)
		}
	}

	skeleton := buildSkeleton(&cluster, rootApp, cniApp, crs)

	if err := skeleton.Write(opts.Dir); err != nil {
		return fmt.Errorf("gitops export: writing skeleton to %s: %w", opts.Dir, err)
	}

	_, _ = fmt.Fprintf(warn, "gitops export: wrote %d object(s) to %s\n", len(crs), opts.Dir)

	return nil
}

// exportMachines adds one entry per approved TalosMachine belonging to
// opts.Cluster to crs. An unapproved machine is never added -- it is not
// yet part of the cluster's committed state -- but if it already looks
// tracked by Argo CD or Flux (gitopstracking.IsTracked), that is
// surprising enough on its own to warn about even though export still
// leaves it out.
func exportMachines(ctx context.Context, k8sClient client.Client, opts gitopsExportOptions, crs map[string]any, warn io.Writer) error {
	var machines ichorv1alpha1.TalosMachineList
	if err := k8sClient.List(ctx, &machines, client.InNamespace(opts.Namespace)); err != nil {
		return fmt.Errorf("gitops export: listing TalosMachines: %w", err)
	}

	for i := range machines.Items {
		m := &machines.Items[i]
		if m.Spec.ClusterRef.Name != opts.Cluster {
			continue
		}

		if !m.Spec.IsApproved() {
			if tracked, by, err := gitopstracking.IsTracked(ctx, k8sClient, opts.ArgoCDNamespace, m); err != nil {
				_, _ = fmt.Fprintf(warn, "gitops export: warning: checking whether TalosMachine %s is tracked: %v\n", m.Name, err)
			} else if tracked {
				_, _ = fmt.Fprintf(warn,
					"gitops export: warning: TalosMachine %s is not approved but already appears to be tracked by %s; it will NOT be exported\n",
					m.Name, by)
			}

			continue
		}

		crs["talos-machine-"+m.Name+".yaml"] = m
	}

	return nil
}

// exportConfigPatches adds one entry per TalosConfigPatch belonging to
// opts.Cluster to crs.
func exportConfigPatches(ctx context.Context, k8sClient client.Client, opts gitopsExportOptions, crs map[string]any) error {
	var patches ichorv1alpha1.TalosConfigPatchList
	if err := k8sClient.List(ctx, &patches, client.InNamespace(opts.Namespace)); err != nil {
		return fmt.Errorf("gitops export: listing TalosConfigPatches: %w", err)
	}

	for i := range patches.Items {
		p := &patches.Items[i]
		if p.Spec.ClusterRef.Name != opts.Cluster {
			continue
		}

		crs["talos-config-patch-"+p.Name+".yaml"] = p
	}

	return nil
}

// exportStorages adds one entry per TalosStorage belonging to
// opts.Cluster to crs.
func exportStorages(ctx context.Context, k8sClient client.Client, opts gitopsExportOptions, crs map[string]any) error {
	var storages ichorv1alpha1.TalosStorageList
	if err := k8sClient.List(ctx, &storages, client.InNamespace(opts.Namespace)); err != nil {
		return fmt.Errorf("gitops export: listing TalosStorages: %w", err)
	}

	for i := range storages.Items {
		s := &storages.Items[i]
		if s.Spec.ClusterRef.Name != opts.Cluster {
			continue
		}

		crs["talos-storage-"+s.Name+".yaml"] = s
	}

	return nil
}

// exportCNIApplication returns the CNI's own Argo CD Application, the
// same way initArgoCD builds it for a brand-new cluster, or nil if the
// preset is "custom" (ADR 0013: a custom CNI has no Preset implementation
// to compute one from) or unset. flannel's own Preset.ArgoCDApplication
// already returns nil, nil (it is never handed to Argo CD), so no
// preset-by-preset special case is needed beyond excluding "custom".
func exportCNIApplication(cluster *ichorv1alpha1.TalosCluster) (*gitops.Application, error) {
	preset := cluster.Spec.CNI.Preset
	if preset == "" || preset == "custom" {
		return nil, nil //nolint:nilnil // "no Application to build" is a legitimate outcome, not a placeholder
	}

	p, err := cniPreset(preset)
	if err != nil {
		return nil, fmt.Errorf("gitops export: %w", err)
	}

	kubeProxy := cni.KubeProxyMode(cluster.Spec.CNI.KubeProxy)
	if kubeProxy == "" {
		kubeProxy = cni.KubeProxyEnabled
	}

	app, err := p.ArgoCDApplication(kubeProxy)
	if err != nil {
		return nil, fmt.Errorf("gitops export: rendering cni Argo CD Application: %w", err)
	}

	return app, nil
}

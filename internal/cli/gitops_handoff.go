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
	"slices"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// handoffComponents lists every component `ichor gitops handoff` accepts
// as its positional argument. CNI is the only one today -- ADR 0024's
// CNIHandoverReconciler is the only hand-over reconciler that exists --
// so this is a slice, not an enum-shaped switch, to make adding the next
// one later an addition here, not a restructuring.
var handoffComponents = []string{"cni"}

// handoffOpts backs `ichor gitops handoff`'s flags.
var handoffOpts gitopsHandoffOptions

// gitopsHandoffOptions is runGitopsHandoff's input, mirroring
// gitopsExportOptions so a test can build one without going through
// cobra.
type gitopsHandoffOptions struct {
	Cluster   string
	Namespace string
}

// handoffCmd implements `ichor gitops handoff <component>`
// (docs/design/architecture.md, "GitOps and Argo CD"): a manual trigger
// for a hand-over that is otherwise only reconciled automatically.
//
// This command does not reimplement any part of what
// internal/controller.CNIHandoverReconciler (ADR 0024) already does --
// watching the CNI's Argo CD Application for Synced+Healthy, then
// removing the inline manifest. Its entire job is the one field flip the
// controller is already waiting on: TalosCluster.spec.cni.handover ->
// "automatic". Everything after that -- polling, the actual removal --
// is the reconciler's, started the moment this command's patch lands.
var handoffCmd = &cobra.Command{
	Use:   "handoff <component>",
	Short: "Manually trigger a hand-over that is otherwise reconciled automatically",
	Long: `handoff flips a single TalosCluster field so the controller's own
hand-over reconciler (ADR 0024, for "cni" today) takes over from there.

It is only useful when spec.cni.handover is currently "manual" or
"disabled": handoff sets it to "automatic" and then gets out of the way.
It never talks to Argo CD or a Talos node itself -- the controller does
that, continuously, once the field is set.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		component := args[0]
		if !slices.Contains(handoffComponents, component) {
			return fmt.Errorf("gitops handoff: unknown component %q: must be one of %v", component, handoffComponents)
		}

		k8sClient, err := newKubeClient(kubeconfigPath)
		if err != nil {
			return fmt.Errorf("gitops handoff: building the Kubernetes client: %w", err)
		}

		return runGitopsHandoff(cmd.Context(), handoffOpts, k8sClient, cmd.OutOrStdout())
	},
}

func init() {
	gitopsCmd.AddCommand(handoffCmd)

	flags := handoffCmd.Flags()
	flags.StringVar(&handoffOpts.Cluster, flagCluster, "", "name of the TalosCluster to trigger hand-over for (required)")
	flags.StringVarP(&handoffOpts.Namespace, "namespace", "n", ichorControllerNamespace, "namespace the TalosCluster lives in")

	_ = handoffCmd.MarkFlagRequired(flagCluster)
}

// runGitopsHandoff is handoffCmd's RunE, factored out so a test can call
// it directly against a fake client. component is validated by the
// caller; today it can only be "cni", which is why this only ever
// touches spec.cni.handover.
func runGitopsHandoff(ctx context.Context, opts gitopsHandoffOptions, k8sClient client.Client, out io.Writer) error {
	var cluster ichorv1alpha1.TalosCluster
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: opts.Namespace, Name: opts.Cluster}, &cluster); err != nil {
		return fmt.Errorf("gitops handoff: fetching TalosCluster %s/%s: %w", opts.Namespace, opts.Cluster, err)
	}

	previous := cluster.Spec.CNI.Handover
	if previous == cniHandoverAutomatic {
		_, _ = fmt.Fprintf(out, "gitops handoff: TalosCluster %s/%s is already \"automatic\"; nothing to do\n", opts.Namespace, opts.Cluster)
		return nil
	}

	cluster.Spec.CNI.Handover = cniHandoverAutomatic

	if err := k8sClient.Update(ctx, &cluster); err != nil {
		return fmt.Errorf("gitops handoff: updating TalosCluster %s/%s: %w", opts.Namespace, opts.Cluster, err)
	}

	_, _ = fmt.Fprintf(out, "gitops handoff: TalosCluster %s/%s cni.handover: %q -> %q; the controller will take it from here\n",
		opts.Namespace, opts.Cluster, previous, cniHandoverAutomatic)

	return nil
}

// cniHandoverAutomatic is TalosClusterCNI.Handover's "automatic" value
// (api/v1alpha1/taloscluster_types.go), duplicated here as a literal, not
// logic, since it is the CRD's own fixed enum value, not something this
// package computes.
const cniHandoverAutomatic = "automatic"

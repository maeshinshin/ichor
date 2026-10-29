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

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"

	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/internal/installmanifest"
)

var (
	adoptCluster       string
	adoptNamespace     string
	adoptSecretsBundle string
	adoptRecover       bool
	adoptRecoverFrom   string
	adoptContext       string
	adoptCNIUnmanaged  bool
	adoptPlanOnly      bool
	adoptSeed          string
	adoptContract      string
	adoptAcceptChanges bool
	adoptAcceptDiff    bool
)

// adoptCmd implements `ichor adopt` (docs/design/architecture.md,
// "Bootstrap"): it brings an existing, already-running Talos cluster under
// Ichor's management by diffing each of its TalosMachines' live
// configuration against a fresh render and storing the difference as
// initial TalosConfigPatches, so that ownership transfers with zero drift.
//
// adopt is one of the design document's three exceptions to CLI/UI parity
// ("init, adopt and full-loss recovery, which predate the cluster"): it
// never calls the Connect API, and talks to the Talos API directly and
// read-only through the operator's own talosconfig, since the cluster it
// is adopting has no reason to have an Ichor-issued credential yet.
var adoptCmd = &cobra.Command{
	Use:   "adopt",
	Short: "Bring an existing Talos cluster under Ichor's management with zero drift",
	Long: `adopt brings an existing, already-running Talos cluster under Ichor's
management. For every TalosMachine already recorded for --cluster, it reads
the node's live configuration over the Talos API (using --talosconfig, the
same credential talosctl itself would use), renders a fresh configuration
from the TalosCluster/TalosMachine/TalosConfigPatch objects already in the
Kubernetes API (using --kubeconfig), and turns the difference into a new
TalosConfigPatch -- so that applying it on top of that fresh render
reproduces the node's live configuration byte for byte. adopt verifies this
itself before ever proposing a patch: if it cannot achieve zero drift for a
machine automatically, it reports the differing fields and leaves that
machine untouched rather than writing a patch that would not.

adopt never applies anything to a Talos node; it only ever reads one, and
only ever writes TalosConfigPatch objects to the Kubernetes API.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := validateOutputFormat(outputFormat); err != nil {
			return err
		}

		k8sClient, err := newKubeClient(kubeconfigPath)
		if err != nil {
			return fmt.Errorf("adopt: building the Kubernetes client: %w", err)
		}

		dial := func(ctx context.Context, endpoint string) (talosConfigReader, error) {
			return dialTalos(ctx, talosconfigPath, adoptContext, endpoint)
		}

		bundle, _, err := resolveAdoptBundle(cmd.Context(), adoptSecretsOptions{
			File: adoptSecretsBundle, Recover: adoptRecover, RecoverFrom: adoptRecoverFrom,
			Cluster:  adoptCluster,
			Contract: clusterVersionContract(cmd.Context(), k8sClient, adoptNamespace, adoptCluster),
		}, dial, cmd.ErrOrStderr())
		if err != nil {
			return err
		}

		restCfg, err := loadKubeconfig(kubeconfigPath)
		if err != nil {
			return err
		}

		kube, err := kubernetes.NewForConfig(restCfg)
		if err != nil {
			return fmt.Errorf("adopt: building a Kubernetes client: %w", err)
		}

		deps := adoptDeps{
			K8s:       k8sClient,
			Bundle:    bundle,
			DialTalos: dial,
			Stdout:    cmd.OutOrStdout(),
			Stderr:    cmd.ErrOrStderr(),
			Stdin:     cmd.InOrStdin(),
			AssumeYes: assumeYes,
			Output:    outputFormat,

			Target:       k8sClient,
			CNIUnmanaged: adoptCNIUnmanaged,

			Preflight:           newCiliumPreflight(kube, k8sClient),
			AcceptCiliumChanges: adoptAcceptChanges,
			AcceptCiliumDiff:    adoptAcceptDiff,

			PlanOnly:         adoptPlanOnly,
			Seed:             cmp.Or(adoptSeed, adoptRecoverFrom),
			ContractOverride: adoptContract,
			ReadIdentity: func(ctx context.Context, address string) (controller.HardwareIdentity, error) {
				t, err := dialTalos(ctx, talosconfigPath, adoptContext, address)
				if err != nil {
					return controller.HardwareIdentity{}, err
				}

				tc, ok := t.(*talosClient)
				if !ok {
					return controller.HardwareIdentity{}, errors.New("the Talos client cannot read a hardware identity")
				}

				defer tc.Close() //nolint:errcheck // read-only connection

				return tc.Identity(ctx)
			},
			Ichor: &adoptWriteIchor{Manifest: installmanifest.Manifest, NetworkPolicies: installmanifest.NetworkPolicies},
		}

		return runAdopt(cmd.Context(), adoptCluster, adoptNamespace, deps)
	},
}

func init() {
	rootCmd.AddCommand(adoptCmd)

	adoptCmd.Flags().StringVar(&adoptCluster, flagCluster, "", "name of the TalosCluster to adopt (required)")
	adoptCmd.Flags().StringVarP(&adoptNamespace, "namespace", "n", "default", "namespace the TalosCluster and its TalosMachines live in")
	adoptCmd.Flags().StringVar(&adoptSecretsBundle, "secrets-bundle", "",
		"path to the cluster's existing secrets bundle (the file produced by 'talosctl gen secrets'); give this or --recover-secrets")
	adoptCmd.Flags().BoolVar(&adoptRecover, "recover-secrets", false,
		"rebuild the secrets bundle from a control plane's machine config (read-only) and store it under ~/.ichor/clusters/<cluster>/ with mode 0600")
	adoptCmd.Flags().StringVar(&adoptRecoverFrom, "recover-from", "",
		"address of the control plane --recover-secrets reads the machine config from")
	adoptCmd.Flags().StringVar(&adoptContext, "context", "",
		"context of the talosconfig to use (default: the file's selected context)")
	adoptCmd.Flags().BoolVar(&adoptCNIUnmanaged, "cni-unmanaged", false,
		"adopt the cluster with its CNI left exactly as it is: Ichor will not install, upgrade or configure it")

	adoptCmd.Flags().BoolVar(&adoptPlanOnly, "plan", false, "print the plan and write nothing")
	adoptCmd.Flags().StringVar(&adoptSeed, "node", "",
		"address of one control plane; adopt discovers the cluster's machines from its membership when the TalosCluster does not exist yet")
	adoptCmd.Flags().StringVar(&adoptContract, "version-contract", "",
		"use this Talos version contract (v1.12 to the running minor) instead of the probed one")

	adoptCmd.Flags().BoolVar(&adoptAcceptChanges, "accept-cilium-changes", false,
		"accept the Ichor-owned Cilium settings the takeover will change on the running data plane (never IPAM or the tunnel protocol)")
	adoptCmd.Flags().BoolVar(&adoptAcceptDiff, "accept-cilium-diff", false,
		"accept the differences between the live Cilium objects and the pinned chart")

	_ = adoptCmd.MarkFlagRequired(flagCluster)
	adoptCmd.MarkFlagsMutuallyExclusive("secrets-bundle", "recover-secrets")
	adoptCmd.MarkFlagsOneRequired("secrets-bundle", "recover-secrets")
}

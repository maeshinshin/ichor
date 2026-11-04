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
	"os"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/internal/installmanifest"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

var (
	adoptCluster       string
	adoptNamespace     string
	adoptSecretsBundle string
	adoptRecover       bool
	adoptRecoverFrom   string
	adoptContext       string
	adoptCNIUnmanaged  bool
	adoptTunnel        string
	adoptCiliumValues  string
	adoptPlanOnly      bool
	adoptRequireIchor  bool
	adoptSeed          string
	adoptContract      string
	adoptAcceptChanges bool
	adoptAcceptDiff    bool
	adoptWaitTimeout   time.Duration
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

adopt runs in four phases, and nothing is applied to a node in any of them:

  1. Plan: reads the nodes and the Kubernetes side, detects the CNI, classifies
     the drift and prints the plan. --plan stops here and writes nothing.
  2. Consent: shows what will be written (and any foreign Cilium Helm release
     record that will be deleted after a backup) and asks; --yes skips the question.
  3. Write: creates the secrets bundle Secret, the TalosCluster and held
     TalosMachines (annotated as adopted), the initial TalosConfigPatch objects,
     and installs Ichor itself. A Cilium that Ichor does not own yet is then taken
     over as Ichor's Helm release.
  4. Release: lifts the hold one machine at a time, control planes first, each
     waiting for Ready, so the first render is applied with zero drift.

Every node must run Talos v1.12 or later; older ones are refused and nothing is
written. The cluster's secrets are required: pass --secrets-bundle FILE, or
--recover-secrets with --recover-from ADDRESS to read them from a control
plane's machine config (not both).

A Cilium cluster is taken over after a diff check against the pinned chart;
--accept-cilium-diff accepts differences from the live objects and
--accept-cilium-changes the Ichor-owned settings that will change on the running
data plane (IPAM and the tunnel protocol are never changed). Any other CNI is
adopted only with --cni-unmanaged, which leaves the CNI exactly as it is: Ichor
will not install, upgrade or configure it. A cluster with two CNIs is refused.

adopt only reads the Talos nodes (using --talosconfig); it never applies
configuration to them. Afterwards it advises a CA rotation, since adopted
clusters keep the old CAs, but never starts one.`,
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

		// For a person, adopt shows its steps; the bundle reminder is then a warning among them.
		var (
			steps     *progress
			nagWriter = cmd.ErrOrStderr()
		)

		if outputFormat == outputHuman {
			steps = newProgress(cmd.ErrOrStderr())
			nagWriter = bundleNagWriter{steps}
		}

		bundle, saveBundle, err := resolveAdoptBundle(cmd.Context(), adoptSecretsOptions{
			File: adoptSecretsBundle, Recover: adoptRecover, RecoverFrom: adoptRecoverFrom,
			Cluster:  adoptCluster,
			Contract: clusterVersionContract(cmd.Context(), k8sClient, adoptNamespace, adoptCluster),
		}, dial, nagWriter)
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

		ciliumValues, err := readCiliumValuesFile(adoptCiliumValues)
		if err != nil {
			return fmt.Errorf("adopt: %w", err)
		}

		deps := adoptDeps{
			K8s:        k8sClient,
			Bundle:     bundle,
			SaveBundle: saveBundle,
			DialTalos:  dial,
			Stdout:     cmd.OutOrStdout(),
			Stderr:     cmd.ErrOrStderr(),
			Stdin:      cmd.InOrStdin(),
			AssumeYes:  assumeYes,
			Output:     outputFormat,
			Steps:      steps,

			Target:       k8sClient,
			CNIUnmanaged: adoptCNIUnmanaged,
			Tunnel:       adoptTunnel,
			CiliumValues: ciliumValues,
			BootstrapCilium: func(ctx context.Context, c *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode) error {
				_, err := cilium.Bootstrap(ctx, restCfg, c, mode, cilium.DefaultApplyTimeout)

				return err
			},

			Preflight:           newCiliumPreflight(kube, k8sClient),
			AcceptCiliumChanges: adoptAcceptChanges,
			AcceptCiliumDiff:    adoptAcceptDiff,

			PlanOnly:         adoptPlanOnly,
			WebhookWait:      adoptWaitTimeout,
			RequireIchor:     adoptRequireIchor,
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

		return silenceIfShown(cmd, runAdopt(cmd.Context(), adoptCluster, adoptNamespace, deps))
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

	adoptCmd.Flags().DurationVar(&adoptWaitTimeout, "wait-timeout", defaultAdoptWebhookWait,
		"how long to wait for Ichor's admission webhooks before giving up")
	adoptCmd.Flags().StringVar(&adoptTunnel, "tunnel", "",
		"tunnel protocol (vxlan or geneve) of the Cilium adopt installs on a cluster with no CNI; immutable afterwards")
	adoptCmd.Flags().BoolVar(&adoptRequireIchor, "require-ichor", false,
		"fail when Ichor is not installed (for example with Helm) instead of installing it")
	adoptCmd.Flags().StringVar(&adoptCiliumValues, "cilium-values", "",
		"YAML file of Cilium Helm values for the Cilium adopt installs on a cluster with no CNI (Ichor-owned keys are refused)")
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

// readCiliumValuesFile reads --cilium-values: a YAML mapping, or nil when the flag is not given.
func readCiliumValuesFile(path string) (map[string]any, error) {
	if path == "" {
		return nil, nil //nolint:nilnil // no file, no values
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--cilium-values: %w", err)
	}

	var vals map[string]any
	if err := yaml.Unmarshal(raw, &vals); err != nil {
		return nil, fmt.Errorf("--cilium-values %s: %w", path, err)
	}

	return vals, nil
}

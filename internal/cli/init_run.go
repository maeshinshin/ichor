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
	"net"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// initOptions bundles every flag ichor init's RunE collects.
type initOptions struct {
	initPlanInput
	NodeEndpoint string

	DiskRoles diskRoleOptions

	ArgoCD          bool
	ArgoCDNamespace string
	RepoURL         string
	RepoPath        string
	RepoUsername    string
	RepoPassword    string

	GitOpsDir string
}

// initDeps bundles init's side effects -- hardware identity, the Talos
// API in both its maintenance and admin forms, and the terminal -- behind
// small interfaces/funcs, mirroring adoptDeps, so runInit is driven by a
// test with no real node, cluster or network on either end.
type initDeps struct {
	Identity        func(ctx context.Context, endpoint string) (controller.HardwareIdentity, error)
	DialMaintenance func(ctx context.Context, endpoint string) (maintenanceApplier, error)
	DialAdmin       func(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, endpoint string, bundle *secrets.Bundle) (bootstrapClient, error)
	NewK8sClient    func(kubeconfig []byte) (client.Client, error)

	// IchorManifest is applied to the new cluster right after the bundle
	// Secret (design step 3/4): production wires
	// installmanifest.Manifest; a test supplies its own fixture bytes.
	IchorManifest []byte

	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	AssumeYes bool
	Output    string

	// BootstrapWait and BootstrapInterval bound how long init waits for the node to come back on
	// its new configuration before bootstrapping etcd; zero means the defaults.
	BootstrapWait     time.Duration
	BootstrapInterval time.Duration
}

// runInit implements `ichor init` steps 1-5 (docs/design/architecture.md,
// "Bootstrap"). Step 6 (hand-over) is a different unit's work: it is a
// continuous reconciliation ("once Argo CD reports each bootstrapped
// component Synced and Healthy, remove it from inlineManifests"), not
// something a one-shot CLI command does.
func runInit(ctx context.Context, opts initOptions, deps initDeps) error {
	// Step 2: settle the cluster's shape.
	cluster, err := buildInitPlan(opts.initPlanInput, deps.Stderr)
	if err != nil {
		return err
	}

	preset, err := cniPreset(opts.CNIPreset)
	if err != nil {
		return err
	}

	cniBoot, err := buildCNIBootstrap(preset, cluster.Name, cluster.Namespace, opts.ArgoCD)
	if err != nil {
		return err
	}

	identity, err := deps.Identity(ctx, opts.NodeEndpoint)
	if err != nil {
		return fmt.Errorf("init: reading the first node's hardware identity: %w", err)
	}

	if identity.UUID == "" {
		return fmt.Errorf(
			"init: the node at %q reported no SMBIOS UUID; init needs a strong hardware identity for the first control plane",
			opts.NodeEndpoint)
	}

	installDisk, err := parseInstallDisk(opts.InstallDisk)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}

	machine := &ichorv1alpha1.TalosMachine{
		Name: identity.UUID, Namespace: cluster.Namespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
			Identity: ichorv1alpha1.TalosMachineIdentity{
				UUID: identity.UUID, Serial: identity.Serial, MACAddresses: identity.MACAddresses,
			},
			Endpoint: opts.NodeEndpoint,
			Role:     roleControlPlane,
			Install:  &ichorv1alpha1.TalosMachineInstall{Disk: *installDisk},
			// init is the special case of approving the first control
			// plane (architecture.md's "Bootstrap": "Adding later nodes
			// goes through the normal approval flow").
			ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
		},
	}

	// Step 1: generate the secrets bundle and back it up locally before
	// anything else happens -- losing it after this point is the one
	// mistake this command cannot undo for the operator.
	contract, err := coreconfig.ParseContractFromVersion(opts.VersionContract)
	if err != nil {
		return fmt.Errorf("init: parsing --version-contract: %w", err)
	}

	bundle, err := secrets.NewBundle(secrets.NewClock(), contract)
	if err != nil {
		return fmt.Errorf("init: generating the secrets bundle: %w", err)
	}

	bundlePath, err := saveBundleFile(cluster.Name, bundle)
	if err != nil {
		return fmt.Errorf("init: saving the secrets bundle: %w", err)
	}

	nagBackup(deps.Stderr, bundlePath)

	// Step 3: render the first control plane's configuration.
	candidates := candidatePatches(cluster, machine, cniBoot, opts.NodeEndpoint, opts.DiskRoles)

	// --registry-mirror only builds anonymous mirrors, and the new cluster has no management
	// API to read an authSecretRef from yet; refuse rather than render a mirror with no
	// credentials if a mirror ever carries one.
	for _, r := range cluster.Spec.Registries {
		if r.AuthSecretRef != nil {
			return fmt.Errorf("init: registry %q has an authSecretRef; credentials are not supported at bootstrap, add them after init", r.Host)
		}
	}

	rendered, _, err := render.Render(cluster, machine, bundle, candidates, nil)
	if err != nil {
		return fmt.Errorf("init: rendering the first node's configuration: %w", err)
	}

	if !deps.AssumeYes {
		ok, err := confirm(deps.Stdin, deps.Stderr, fmt.Sprintf(
			"Apply this configuration to %s now? This is IRREVERSIBLE: Talos's insecure apply does not "+
				"authenticate the node (a separate provisioning network is assumed), and the node will install "+
				"and reboot. [y/N]: ",
			opts.NodeEndpoint))
		if err != nil {
			return fmt.Errorf("init: reading confirmation: %w", err)
		}

		if !ok {
			return fmt.Errorf("init: aborted before applying to the node; nothing was changed")
		}
	}

	maint, err := deps.DialMaintenance(ctx, opts.NodeEndpoint)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}

	if err := maint.ApplyInsecure(ctx, rendered); err != nil {
		return fmt.Errorf("init: applying configuration to %q: %w", opts.NodeEndpoint, err)
	}

	// Step 4: bootstrap etcd, fetch the kubeconfig, store the bundle as a
	// Secret.
	admin, err := deps.DialAdmin(ctx, cluster, opts.NodeEndpoint, bundle)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}

	if err := bootstrapWhenUp(ctx, admin, deps.BootstrapWait, deps.BootstrapInterval, deps.Stderr); err != nil {
		return fmt.Errorf("init: bootstrapping etcd: %w", err)
	}

	kubeconfig, err := admin.Kubeconfig(ctx)
	if err != nil {
		return fmt.Errorf("init: fetching the kubeconfig: %w", err)
	}

	kubeconfigPath, err := saveKubeconfigFile(cluster.Name, kubeconfig)
	if err != nil {
		return fmt.Errorf("init: saving the kubeconfig: %w", err)
	}

	_, _ = fmt.Fprintf(deps.Stderr, "init: kubeconfig written to %s\n", kubeconfigPath)

	k8sClient, err := deps.NewK8sClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("init: connecting to the new cluster: %w", err)
	}

	bundleSecret, err := buildBundleSecret(cluster, bundle)
	if err != nil {
		return fmt.Errorf("init: preparing the secrets bundle Secret: %w", err)
	}

	if err := k8sClient.Create(ctx, bundleSecret); err != nil {
		return fmt.Errorf("init: creating the secrets bundle Secret: %w", err)
	}

	// Ichor itself, applied through the API server now that kubeconfig is
	// in hand -- not inlined into the node's boot config: its
	// CRDs must exist before step 5 can create any of them.
	if err := applyIchorManifest(ctx, k8sClient, deps.IchorManifest, deps.Stderr); err != nil {
		return err
	}

	if err := createIchorInstallation(ctx, k8sClient, deps.Stderr); err != nil {
		return err
	}

	// Step 5: create the CRs.
	if err := createCRs(ctx, k8sClient, cluster, machine, candidates, deps.Stdout, deps.Output); err != nil {
		return err
	}

	if opts.ArgoCD {
		if err := initArgoCD(ctx, opts, cluster, machine, candidates, cniBoot.ArgoCDApplication, k8sClient, deps.Stderr); err != nil {
			return err
		}
	}

	return nil
}

// createCRs implements design step 5, "create the CRs": the
// TalosCluster, the first control plane's TalosMachine, and every
// candidate TalosConfigPatch, then prints what it created in the
// requested output format (design: "-o yaml|json").
func createCRs(
	ctx context.Context, k8sClient client.Client,
	cluster *ichorv1alpha1.TalosCluster, machine *ichorv1alpha1.TalosMachine, candidates []ichorv1alpha1.TalosConfigPatch,
	stdout io.Writer, output string,
) error {
	if err := k8sClient.Create(ctx, cluster); err != nil {
		return fmt.Errorf("init: creating TalosCluster: %w", err)
	}

	if err := k8sClient.Create(ctx, machine); err != nil {
		return fmt.Errorf("init: creating TalosMachine: %w", err)
	}

	for _, p := range candidates {
		if err := k8sClient.Create(ctx, &p); err != nil {
			return fmt.Errorf("init: creating TalosConfigPatch %q: %w", p.Name, err)
		}
	}

	out := []client.Object{cluster, machine}
	for i := range candidates {
		out = append(out, &candidates[i])
	}

	return writeOutputObjects(stdout, output, out)
}

// candidatePatches collects every TalosConfigPatch init creates alongside
// the TalosCluster/TalosMachine: the CNI's own Talos-side patch (if any)
// and the inline-manifests patch carrying the CNI's bootstrap manifest
// (if the preset has one).
func candidatePatches(
	cluster *ichorv1alpha1.TalosCluster, machine *ichorv1alpha1.TalosMachine, cniBoot *cniBootstrap, nodeEndpoint string,
	diskRoles diskRoleOptions,
) []ichorv1alpha1.TalosConfigPatch {
	var out []ichorv1alpha1.TalosConfigPatch

	if cniBoot.Patch != nil {
		out = append(out, *cniBoot.Patch)
	}

	var manifests []*typedconfig.ClusterInlineManifest
	if cniBoot.InlineManifest != nil {
		manifests = []*typedconfig.ClusterInlineManifest{cniBoot.InlineManifest}
	}

	// Always built, even for flannel (no inline manifest) or a CNI with
	// no TalosPatches: its machine.type and machine.certSANs are required by the patch CRD.
	bootstrap := buildInlineManifestsPatch(cluster, machine, nodeEndpoint, manifests)
	bootstrap.Spec.Documents = append(bootstrap.Spec.Documents, buildDiskRoleDocuments(diskRoles)...)
	out = append(out, bootstrap)

	return out
}

// buildInlineManifestsPatch builds the cluster-layer Config-kind patch
// that carries cluster.inlineManifests (design step 3: "cluster.
// inlineManifests carry Ichor itself, the CNI (unless flannel), and, with
// --argocd, Argo CD plus a root Application"). Only the CNI's manifest is
// populated today -- Ichor's own and Argo CD's own install manifests are
// not sourced by this command yet.
//
// This is authored directly, not reconstructed from a diff, so the
// required-but-non-secret machine.type/machine.certSANs fields
// (config/crd/bases/ichor.maesh.dev_talosconfigpatches.yaml's
// machine.required) get real, correct values -- the node's own role and
// address -- never placeholders.
func buildInlineManifestsPatch(
	cluster *ichorv1alpha1.TalosCluster, machine *ichorv1alpha1.TalosMachine, nodeEndpoint string,
	manifests []*typedconfig.ClusterInlineManifest,
) ichorv1alpha1.TalosConfigPatch {
	host := nodeEndpoint
	if h, _, err := net.SplitHostPort(nodeEndpoint); err == nil {
		host = h
	}

	return ichorv1alpha1.TalosConfigPatch{
		Name: cluster.Name + "-bootstrap-manifests", Namespace: cluster.Namespace,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
			Layer:      cniPatchLayer,
			Documents: []ichorv1alpha1.TalosConfigPatchDocument{{
				Kind: "Config",
				Config: &typedconfig.Config{
					ConfigVersion: "v1alpha1",
					MachineConfig: &typedconfig.MachineConfig{
						MachineType:     machine.Spec.Role,
						MachineCertSANs: []string{host},
					},
					ClusterConfig: &typedconfig.ClusterConfig{ClusterInlineManifests: manifests},
				},
			}},
		},
	}
}

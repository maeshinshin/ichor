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
	"net"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// initOptions bundles every flag ichor init's RunE collects.
type initOptions struct {
	initPlanInput
	NodeEndpoint string

	DiskRoles diskRoleOptions

	// UIURL turns Ichor's own users on (ADR 0094); AdminUser names the first admin.
	UIURL     string
	AdminUser string

	// With UIURL: TLS inside ichor-ui (an existing Secret, or a cert/key init stores) and the
	// kubeconfig's server, passed to ichor-ui.
	UITLSSecret, UITLSCert, UITLSKey       string
	KubeAPIServerURL, KubeAPITLSServerName string
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
	// Secret (design step 3/4, ADR 0019's revision): production wires
	// installmanifest.Manifest; a test supplies its own fixture bytes.
	IchorManifest []byte

	// NetworkPolicies is applied after Cilium is Ready (ADR 0100): production wires
	// installmanifest.NetworkPolicies; empty skips them. CiliumWait bounds that wait.
	NetworkPolicies []byte
	CiliumWait      time.Duration

	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	AssumeYes bool
	Output    string

	// BootstrapWait and BootstrapInterval bound how long init waits for the node to come back on
	// its new configuration before bootstrapping etcd; zero means the defaults.
	BootstrapWait     time.Duration
	BootstrapInterval time.Duration
	// WebhookWait bounds how long init waits for Ichor's admission webhooks before creating
	// the first CRs; zero means defaultWebhookWait.
	WebhookWait time.Duration
	// HandoverWait bounds the wait for the Cilium hand-over (CNIHandover); zero means
	// defaultHandoverWait. HandoverInterval is its poll interval; zero means the bootstrap default.
	HandoverWait     time.Duration
	HandoverInterval time.Duration
}

// runInit implements `ichor init` (docs/design/architecture.md, "Bootstrap"): steps 1-5, then it
// waits for the Cilium hand-over (ADR 0099), a
// reconciliation the CLI only watches.
func runInit(ctx context.Context, opts initOptions, deps initDeps) (err error) {
	// One maintenance pin for the whole run: the node that answered the identity read is the node
	// that receives the configuration (ADR 0108).
	ctx = controller.WithMaintenancePin(ctx, &controller.MaintenancePin{})

	opts, err = prepareInitUsers(opts)
	if err != nil {
		return err
	}

	// The generated admin password is printed once, at the very end, also when a later step fails: by then
	// the user exists and nothing else could show it.
	var adminPassword string

	defer func() { initUsersEpilogue(deps.Stderr, opts, adminPassword) }()

	// Step 2: settle the cluster's shape.
	cluster, err := buildInitPlan(opts.initPlanInput, deps.Stderr)
	if err != nil {
		return err
	}

	preset, err := cilium.New().WithTunnel(opts.CNITunnel)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}

	preset = preset.WithControlPlanes(int(cluster.Spec.ControlPlane.Count))

	cniBoot, err := buildCNIBootstrap(preset, cluster.Name, cluster.Namespace)
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
	if err := rejectRegistryCredentials(cluster); err != nil {
		return err
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
	k8sClient, err := initBootstrapAccess(ctx, opts, deps, cluster, bundle)
	if err != nil {
		return err
	}

	bundleSecret, err := buildBundleSecret(cluster, bundle)
	if err != nil {
		return fmt.Errorf("init: preparing the secrets bundle Secret: %w", err)
	}

	// The API server comes up some time after etcd is bootstrapped.
	if err := waitForAPIServer(ctx, k8sClient, deps.BootstrapWait, deps.BootstrapInterval, deps.Stderr); err != nil {
		return err
	}

	// Ichor itself, applied through the API server now that kubeconfig is
	// in hand -- not inlined into the node's boot config (ADR 0019): its
	// CRDs must exist before step 5 can create any of them, and its
	// namespace before the bundle Secret can.
	if err := applyIchorManifest(ctx, k8sClient, deps.IchorManifest, deps.Stderr); err != nil {
		return err
	}

	// A Create that timed out may still have gone through; createRetrying counts that as success.
	if err := createRetrying(ctx, k8sClient, bundleSecret, deps.Stderr); err != nil {
		return fmt.Errorf("init: creating the secrets bundle Secret: %w", err)
	}

	created, err := createIchorInstallation(ctx, k8sClient, deps.Stderr, 0)
	if err != nil {
		return err
	}

	// ADR 0094 unit 9: the first admin exists before the switch that makes every request need a token.
	if adminPassword, err = initUsers(ctx, opts, k8sClient, deps.Stderr); err != nil {
		return err
	}

	// Step 5: create the CRs.
	create := func(ctx context.Context, obj client.Object) error {
		return createWhenWebhooksUp(ctx, k8sClient, obj, cmp.Or(deps.WebhookWait, deps.BootstrapWait, defaultWebhookWait), deps.BootstrapInterval, deps.Stderr)
	}

	if err := createCRs(ctx, create, cluster, machine, candidates, deps.Stdout, deps.Output); err != nil {
		return err
	}

	// Once Cilium is Ready: their CRD does not exist when the install manifest is applied (ADR 0100).
	if err := applyNetworkPolicies(ctx, k8sClient, deps.NetworkPolicies, opts.CNIPreset,
		cmp.Or(deps.CiliumWait, defaultCiliumWait), deps.BootstrapInterval, deps.Stderr); err != nil {
		return err
	}

	if err := setClusterAuthentication(ctx, k8sClient, cluster, opts.UIURL, deps.Stderr); err != nil {
		return err
	}

	return waitForCiliumHandover(ctx, k8sClient, created, cluster, deps)
}

// waitForCiliumHandover is the end of init (ADR 0099): the controller, not the CLI, hands Cilium
// over to Ichor's Helm release (the ichor-cni Job). A dev build creates no IchorInstallation, so
// there is nothing to wait for.
func waitForCiliumHandover(
	ctx context.Context, k8sClient client.Client, created bool, cluster *ichorv1alpha1.TalosCluster, deps initDeps,
) error {
	if !created {
		_, _ = fmt.Fprintln(deps.Stderr, "init: not waiting for the Cilium hand-over: no IchorInstallation was created")

		return nil
	}

	if err := waitForCNIHandover(ctx, k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name},
		cmp.Or(deps.HandoverWait, defaultHandoverWait), deps.HandoverInterval, deps.Stderr); err != nil {
		if cluster.Spec.ControlPlane.Schedulable == schedulableFalse {
			err = fmt.Errorf("%w (the control planes are tainted: if workloads are what is pending, approve a worker with `ichor machine approve`)", err)
		}

		return fmt.Errorf("init: Cilium was not handed over to Ichor: %w", err)
	}

	_, _ = fmt.Fprintln(deps.Stderr, "init: Cilium is now a Helm release owned by Ichor")

	return nil
}

// initBootstrapAccess is step 4's first half: bootstrap etcd, fetch and save the kubeconfig, and connect.
func initBootstrapAccess(
	ctx context.Context, opts initOptions, deps initDeps, cluster *ichorv1alpha1.TalosCluster, bundle *secrets.Bundle,
) (client.Client, error) {
	admin, err := deps.DialAdmin(ctx, cluster, opts.NodeEndpoint, bundle)
	if err != nil {
		return nil, fmt.Errorf("init: %w", err)
	}

	if err := bootstrapWhenUp(ctx, admin, deps.BootstrapWait, deps.BootstrapInterval, deps.Stderr); err != nil {
		return nil, fmt.Errorf("init: bootstrapping etcd: %w", err)
	}

	kubeconfig, err := admin.Kubeconfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("init: fetching the kubeconfig: %w", err)
	}

	kubeconfigPath, err := saveKubeconfigFile(cluster.Name, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("init: saving the kubeconfig: %w", err)
	}

	_, _ = fmt.Fprintf(deps.Stderr, "init: kubeconfig written to %s\n", kubeconfigPath)

	k8sClient, err := deps.NewK8sClient(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("init: connecting to the new cluster: %w", err)
	}

	return k8sClient, nil
}

func rejectRegistryCredentials(cluster *ichorv1alpha1.TalosCluster) error {
	for _, r := range cluster.Spec.Registries {
		if r.AuthSecretRef != nil {
			return fmt.Errorf("init: registry %q has an authSecretRef; credentials are not supported at bootstrap, add them after init", r.Host)
		}
	}

	return nil
}

// createCRs implements design step 5, "create the CRs": the
// TalosCluster, the first control plane's TalosMachine, and every
// candidate TalosConfigPatch, then prints what it created in the
// requested output format (design: "-o yaml|json").
func createCRs(
	ctx context.Context, create func(context.Context, client.Object) error,
	cluster *ichorv1alpha1.TalosCluster, machine *ichorv1alpha1.TalosMachine, candidates []ichorv1alpha1.TalosConfigPatch,
	stdout io.Writer, output string,
) error {
	if err := create(ctx, cluster); err != nil {
		return fmt.Errorf("init: creating TalosCluster: %w", err)
	}

	if err := create(ctx, machine); err != nil {
		return fmt.Errorf("init: creating TalosMachine: %w", err)
	}

	for _, p := range candidates {
		if err := create(ctx, &p); err != nil {
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
// inlineManifests carry Ichor itself and the CNI"). Only the CNI's manifest is
// populated today -- Ichor's own install manifest is not sourced by this
// command yet; see ADR 0019.
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
			// machine.type is the first node's role, so the patch must not reach machines of
			// another role: at the cluster layer a worker approved later rendered as a control plane.
			Layer:  "role",
			Target: &ichorv1alpha1.TalosConfigPatchTarget{Role: machine.Spec.Role},
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

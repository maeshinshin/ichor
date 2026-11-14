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
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/internal/uiapi"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/release"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// initOptions bundles every flag ichor init's RunE collects.
type initOptions struct {
	initPlanInput
	NodeEndpoint string

	DiskRoles diskRoleOptions

	// UIURL turns Ichor's own users on; AdminUser names the first admin.
	UIURL     string
	AdminUser string

	// With UIURL: TLS inside ichor-ui (an existing Secret, or a cert/key init stores) and the
	// kubeconfig's server, passed to ichor-ui.
	UITLSSecret, UITLSCert, UITLSKey       string
	KubeAPIServerURL, KubeAPITLSServerName string

	// BundlePassphrase seals the local secrets bundle; BundlePlaintext (--bundle-plaintext)
	// stores it unencrypted instead. One of them is required: init never picks plaintext on its own.
	// The passphrase is secret material and is never printed.
	BundlePassphrase string
	BundlePlaintext  bool

	// MergeKubeconfig (--merge-kubeconfig) merges the cluster-admin kubeconfig into the user's
	// own kubeconfig; mergedContext is the context it ended up as, set by runInit for the epilogue.
	MergeKubeconfig bool
	mergedContext   string

	// InstallerDigest and SkipSignatureCheck are recorded in the IchorInstallation (spec.installerDigest,
	// spec.installerSignature.mode skip) so the controller can run the Cilium Job without resolving
	// the installer's tag: a build nobody signs, served by a mirror the controller cannot reach (a demo).
	InstallerDigest    string
	SkipSignatureCheck bool
}

// installerPin is the installer image settings init writes into the IchorInstallation.
func (o initOptions) installerPin() installerPin {
	return installerPin{Digest: o.InstallerDigest, SkipSignature: o.SkipSignatureCheck}
}

// initDeps bundles init's side effects -- hardware identity, the Talos
// API in both its maintenance and admin forms, and the terminal -- behind
// small interfaces/funcs, mirroring adoptDeps, so runInit is driven by a
// test with no real node, cluster or network on either end.
type initDeps struct {
	// ReadNode reads the first node's identity and, when wantInventory, its disks and links, over one connection.
	ReadNode        func(ctx context.Context, endpoint string, wantInventory bool) (nodeReading, error)
	DialMaintenance func(ctx context.Context, endpoint string) (maintenanceApplier, error)
	DialAdmin       func(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, endpoint string, bundle *secrets.Bundle) (bootstrapClient, error)
	NewK8sClient    func(kubeconfig []byte) (client.Client, error)

	// IchorManifest is applied to the new cluster right after the bundle
	// Secret (design step 3/4): production wires
	// installmanifest.Manifest; a test supplies its own fixture bytes.
	IchorManifest []byte

	// NetworkPolicies is applied after Cilium is Ready: production wires
	// installmanifest.NetworkPolicies; empty skips them. CiliumWait bounds that wait.
	NetworkPolicies []byte
	CiliumWait      time.Duration

	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// Progress renders init's steps on Stderr; nil means a new one on Stderr.
	Progress *progress

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

// progress is the renderer of init's steps: Progress, or one on Stderr.
func (d initDeps) progress() *progress {
	if d.Progress != nil {
		return d.Progress
	}

	return newProgress(d.Stderr)
}

// runInit implements `ichor init` (docs/design/architecture.md, "Bootstrap"): steps 1-5, then it
// waits for the Cilium hand-over, a
// reconciliation the CLI only watches.
func runInit(ctx context.Context, opts initOptions, deps initDeps) (err error) {
	// Refuse before any step: the bundle is never stored unencrypted unless asked for.
	if opts.BundlePassphrase == "" && !opts.BundlePlaintext {
		return errNoSealPassphrase
	}

	if err := refuseExistingBundle(opts.Cluster); err != nil {
		return err
	}

	// One maintenance pin for the whole run: the node that answered the identity read is the node
	// that receives the configuration.
	ctx = controller.WithMaintenancePin(ctx, &controller.MaintenancePin{})

	opts, err = prepareInitUsers(opts)
	if err != nil {
		return err
	}

	if err := validateInitInstaller(opts); err != nil {
		return err
	}

	steps := deps.progress()
	deps.Progress = steps

	// The generated admin password is printed once, at the very end, also when a later step fails: by then
	// the user exists and nothing else could show it.
	var adminPassword string

	defer func() { initEpilogue(steps, opts, adminPassword, err == nil) }()

	steps.Header("Ichor init · "+opts.Cluster, initSummary(opts))

	// Read the merge target now: a malformed kubeconfig must stop init before the irreversible apply.
	if opts.MergeKubeconfig {
		if err := planKubeconfigMerge(opts.Cluster, steps); err != nil {
			return err
		}
	}

	// Step 2: settle the cluster's shape.
	cluster, err := planInitCluster(opts, steps.WarnWriter())
	if err != nil {
		return err
	}

	// --install-disk unset on a terminal is asked for below, once the node can list its disks.
	pickDisk := opts.InstallDisk == "" && bundleTerminal()
	if opts.InstallDisk == "" && !pickDisk {
		return errors.New("init: --install-disk is required")
	}

	preset, err := cilium.New().WithTunnel(opts.CNITunnel)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}

	preset = preset.WithControlPlanes(int(cluster.Spec.ControlPlane.Count))

	cniBoot, err := buildCNIBootstrap(preset, cluster.Name, cluster.Namespace, opts.kubeProxyMode())
	if err != nil {
		return err
	}

	identity, installDisk, network, err := readFirstNode(ctx, opts, deps, pickDisk)
	if err != nil {
		return err
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
			Network:  network,
			// init is the special case of approving the first control
			// plane (architecture.md's "Bootstrap": "Adding later nodes
			// goes through the normal approval flow").
			ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
		},
	}

	// Step 1: generate the secrets bundle and back it up locally before
	// anything else happens -- losing it after this point is the one
	// mistake this command cannot undo for the operator.
	var (
		bundle      *secrets.Bundle
		savedBundle string
	)

	err = steps.Run("Secrets bundle", func(s *step) (string, error) {
		contract, err := coreconfig.ParseContractFromVersion(opts.VersionContract)
		if err != nil {
			return "", fmt.Errorf("init: parsing --version-contract: %w", err)
		}

		if bundle, err = secrets.NewBundle(secrets.NewClock(), contract); err != nil {
			return "", fmt.Errorf("init: generating the secrets bundle: %w", err)
		}

		bundlePath, err := saveBundleFile(cluster.Name, bundle, opts.BundlePassphrase)
		if err != nil {
			return "", fmt.Errorf("init: saving the secrets bundle: %w", err)
		}

		savedBundle = bundlePath

		// The cluster's only root of trust: without it and the Secret in the cluster, nothing can
		// be recovered. The call to action stays on the line, in yellow on a terminal.
		if opts.BundlePassphrase == "" {
			s.Attn("back this up now; stored UNENCRYPTED (--bundle-plaintext)")
		} else {
			s.Attn("back this up now, with its passphrase")
		}

		return tildePath(bundlePath), nil
	})
	if err != nil {
		return err
	}

	// Until ApplyInsecure is called, no node holds this bundle and the file is nothing to keep: a
	// failed run (a wrong address, a declined prompt) leaves nothing that blocks the rerun. From the
	// call on, whether it succeeds or not, the node may hold the bundle and the file stays. Only a file
	// this run wrote is ever removed: refuseExistingBundle passed before it was saved.
	defer discardBundleBeforeApply(&deps, &savedBundle, &err)()

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

	// Step 4: bootstrap etcd, fetch the kubeconfig, store the bundle as a
	// Secret.
	k8sClient, mergedContext, err := initBootstrapAccess(ctx, opts, deps, cluster, bundle, rendered)
	opts.mergedContext = mergedContext

	if err != nil {
		return err
	}

	bundleSecret, err := buildBundleSecret(cluster, bundle)
	if err != nil {
		return fmt.Errorf("init: preparing the secrets bundle Secret: %w", err)
	}

	// The API server comes up some time after etcd is bootstrapped.
	err = steps.Run("Kubernetes API", func(s *step) (string, error) {
		s.Update("waiting for the API server")

		return "reachable", waitForAPIServer(ctx, k8sClient, deps.BootstrapWait, deps.BootstrapInterval, s.Rewrite(initDetail))
	})
	if err != nil {
		return err
	}

	var created bool

	created, adminPassword, err = initInstall(ctx, opts, deps, k8sClient, bundleSecret, cluster, machine, candidates)
	if err != nil {
		return err
	}

	if err := waitForCiliumHandover(ctx, k8sClient, created, cluster, deps); err != nil {
		return err
	}

	return waitForUIReady(ctx, k8sClient, deps)
}

// discardBundleBeforeApply makes deps note whether ApplyInsecure is called, and returns the function to
// defer: it removes the bundle file at *path when the run failed (*err) and the node was never sent the
// configuration. Once ApplyInsecure has been called, whatever it returned, the file stays.
func discardBundleBeforeApply(deps *initDeps, path *string, err *error) func() {
	applied := false
	dial := deps.DialMaintenance

	deps.DialMaintenance = func(ctx context.Context, endpoint string) (maintenanceApplier, error) {
		m, dialErr := dial(ctx, endpoint)
		if dialErr != nil {
			return nil, dialErr
		}

		return applyRecorder{m, &applied}, nil
	}

	return func() {
		if *err != nil && !applied {
			discardBundleFile(*path)
		}
	}
}

// applyRecorder notes that ApplyInsecure was called, whatever it returned.
type applyRecorder struct {
	maintenanceApplier
	called *bool
}

func (a applyRecorder) ApplyInsecure(ctx context.Context, cfg []byte) error {
	*a.called = true

	return a.maintenanceApplier.ApplyInsecure(ctx, cfg)
}

// initInstall is the second half of init, once the API server answers: Ichor's own install
// manifest and the bundle Secret, the IchorInstallation (which waits for the controller's
// webhooks), the first admin with --ui-url, the CRs, the network policies and the issuer switch.
// It returns whether the IchorInstallation was created and the generated admin password, which
// the caller prints even when a later step fails.
func initInstall(
	ctx context.Context, opts initOptions, deps initDeps, k8sClient client.Client, bundleSecret *corev1.Secret,
	cluster *ichorv1alpha1.TalosCluster, machine *ichorv1alpha1.TalosMachine, candidates []ichorv1alpha1.TalosConfigPatch,
) (created bool, adminPassword string, err error) {
	steps := deps.progress()

	// Ichor itself, applied through the API server now that kubeconfig is
	// in hand -- not inlined into the node's boot config: its
	// CRDs must exist before step 5 can create any of them, and its
	// namespace before the bundle Secret can.
	err = steps.Run("Install Ichor", func(s *step) (string, error) {
		s.Update("applying Ichor's install manifest")

		if err := applyIchorManifest(ctx, k8sClient, deps.IchorManifest, s.Rewrite(initDetail), opts.CNIPreset == cniCilium); err != nil {
			return "", err
		}

		if opts.GatewayAPI {
			if err := applyGatewayAPICRDs(ctx, k8sClient, s.Rewrite(initDetail)); err != nil {
				return "", err
			}
		}

		objects := s.Detail()

		// A Create that timed out may still have gone through; createRetrying counts that as success.
		if err := createRetrying(ctx, k8sClient, bundleSecret, s); err != nil {
			return "", fmt.Errorf("init: creating the secrets bundle Secret: %w", err)
		}

		return objects, nil
	})
	if err != nil {
		return created, adminPassword, err
	}

	err = steps.Run("Ichor controller", func(s *step) (string, error) {
		s.Update("waiting for the controller to roll out")

		var err error
		if created, err = createIchorInstallation(ctx, k8sClient, s.Rewrite(initDetail), deps.WebhookWait, opts.installerPin()); err != nil {
			return "", err
		}

		if !created {
			s.Warn(s.Detail())

			return "", nil
		}

		return "serving Ichor's webhooks", nil
	})
	if err != nil {
		return created, adminPassword, err
	}

	// The first admin exists before the switch that makes every request need a token.
	if opts.UIURL != "" {
		err = steps.Run("Ichor users", func(s *step) (string, error) {
			var err error

			adminPassword, err = initUsers(ctx, opts, k8sClient, s)

			return s.Detail(), err
		})
		if err != nil {
			return created, adminPassword, err
		}
	}

	// Step 5: create the CRs.
	err = steps.Run("Cluster resources", func(s *step) (string, error) {
		create := func(ctx context.Context, obj client.Object) error {
			return createWhenWebhooksUp(ctx, k8sClient, obj, cmp.Or(deps.WebhookWait, deps.BootstrapWait, defaultWebhookWait), deps.BootstrapInterval, s)
		}

		err := createCRs(ctx, create, cluster, machine, candidates, deps.Stdout, deps.Output, s)

		return fmt.Sprintf("TalosCluster, TalosMachine, TalosConfigPatch ×%d", len(candidates)), err
	})
	if err != nil {
		return created, adminPassword, err
	}

	// Once Cilium is Ready: their CRD does not exist when the install manifest is applied.
	if len(deps.NetworkPolicies) > 0 {
		err = steps.Run("Network policies", func(s *step) (string, error) {
			s.Update("waiting for Cilium to be Ready")

			err := applyNetworkPolicies(ctx, k8sClient, deps.NetworkPolicies, opts.CNIPreset,
				cmp.Or(deps.CiliumWait, defaultCiliumWait), deps.BootstrapInterval, s.Rewrite(initDetail))

			return s.Detail(), err
		})
		if err != nil {
			return created, adminPassword, err
		}
	}

	if opts.UIURL != "" {
		err = steps.Run("Authentication", func(s *step) (string, error) {
			return "spec.authentication.ichor is set", setClusterAuthentication(ctx, k8sClient, cluster, opts.UIURL, s)
		})
		if err != nil {
			return created, adminPassword, err
		}
	}

	return created, adminPassword, nil
}

// initSummary is the second line of init's header: the preset and the release set it installs.
func initSummary(opts initOptions) string {
	return strings.Join([]string{
		opts.Preset,
		"Talos " + cmp.Or(opts.TalosVersion, release.Pinned.Talos),
		"Kubernetes " + cmp.Or(opts.KubernetesVersion, release.Pinned.Kubernetes),
		"Cilium " + release.Pinned.Cilium,
	}, " · ")
}

// tildePath shortens a path under the home directory to ~/...
func tildePath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + strings.TrimPrefix(path, home)
	}

	return path
}

// waitForUIReady ends init once the ichor-ui Deployment is rolled out. Running out of time is a
// warning, not a failure: the cluster itself is fine and ichor-ui comes up on its own.
func waitForUIReady(ctx context.Context, k8sClient client.Client, deps initDeps) error {
	timeout := cmp.Or(deps.WebhookWait, defaultWebhookWait)

	s := deps.progress().Start("ichor-ui")
	s.Update("waiting for ichor-ui to roll out")

	err := waitForUI(ctx, k8sClient, timeout, deps.BootstrapInterval, s.Rewrite(initDetail))
	if ctx.Err() != nil {
		return s.Fail(ctx.Err())
	}

	if err != nil {
		s.Warn(fmt.Sprintf("not ready after %s (%v); the cluster is fine, "+
			"and the first ichor commands may warn that ichor-ui is not reachable until it is", timeout, err))

		return nil
	}

	s.Done("ready")

	return nil
}

// waitForCiliumHandover is the end of init: the controller, not the CLI, hands Cilium
// over to Ichor's Helm release (the ichor-cni Job). A dev build creates no IchorInstallation, so
// there is nothing to wait for.
func waitForCiliumHandover(
	ctx context.Context, k8sClient client.Client, created bool, cluster *ichorv1alpha1.TalosCluster, deps initDeps,
) error {
	s := deps.progress().Start("Cilium hand-over")

	if !created {
		s.Warn("not waiting: no IchorInstallation was created")

		return nil
	}

	s.Update("waiting for the controller to hand Cilium over")

	if err := waitForCNIHandover(ctx, k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name},
		cmp.Or(deps.HandoverWait, defaultHandoverWait), deps.HandoverInterval, s.Rewrite(initDetail)); err != nil {
		if cluster.Spec.ControlPlane.Schedulable == schedulableFalse {
			err = fmt.Errorf("%w (the control planes are tainted: if workloads are what is pending, approve a worker with `ichor approve node`)", err)
		}

		return s.Fail(fmt.Errorf("init: Cilium was not handed over to Ichor: %w", err))
	}

	s.Done("Helm release owned by Ichor")

	return nil
}

// readFirstNode reads the first node's identity and settles its install disk: the --install-disk
// value, or, when pickDisk, one chosen from the node's own disk list on the same connection. For a vip
// endpoint it also settles the primary link and its addressing (see chooseNIC and vipNetwork) as the machine's
// spec.network; it is nil for any other type, where the node keeps Talos's default of DHCP on every physical link. It runs
// before the first write; the apply to the node is initBootstrapAccess's, steps later.
func readFirstNode(
	ctx context.Context, opts initOptions, deps initDeps, pickDisk bool,
) (controller.HardwareIdentity, *ichorv1alpha1.TalosMachineDisk, *ichorv1alpha1.TalosMachineNetwork, error) {
	vip := opts.EndpointType == endpointVIP

	node, err := deps.ReadNode(ctx, opts.NodeEndpoint, pickDisk || vip)
	if err != nil {
		return node.Identity, nil, nil, fmt.Errorf("init: reading the first node's hardware identity: %w", err)
	}

	if node.Identity.UUID == "" {
		return node.Identity, nil, nil, fmt.Errorf(
			"init: the node at %q reported no SMBIOS UUID; init needs a strong hardware identity for the first control plane",
			opts.NodeEndpoint)
	}

	if pickDisk {
		if opts.InstallDisk, err = chooseInstallDisk(deps.Stdin, deps.Stderr, node.Disks, node.InventoryErr); err != nil {
			return node.Identity, nil, nil, fmt.Errorf("init: %w", err)
		}
	}

	installDisk, err := parseDiskSelector("--install-disk", opts.InstallDisk)
	if err != nil {
		return node.Identity, nil, nil, fmt.Errorf("init: %w", err)
	}

	if !vip {
		return node.Identity, installDisk, nil, nil
	}

	nic, err := chooseNIC(deps.Stdin, deps.Stderr, opts.NIC, node.Links, node.InventoryErr)
	if err != nil {
		return node.Identity, nil, nil, fmt.Errorf("init: %w", err)
	}

	network, err := vipNetwork(opts, nic, node.Identity)
	if err != nil {
		return node.Identity, nil, nil, fmt.Errorf("init: %w", err)
	}

	return node.Identity, installDisk, network, nil
}

// vipNetwork is the first node's spec.network for a vip cluster, from the primary link and --address, --gateway,
// --dns and --vlan, checked as `approve node` checks them (networkFromFlags, then the API's own NetworkFromPB):
// DHCP on the link with none of them, static with --address. pkg/render then emits the net0 alias and, on a
// control plane, the VIP on it.
func vipNetwork(opts initOptions, nic string, id controller.HardwareIdentity) (*ichorv1alpha1.TalosMachineNetwork, error) {
	n, err := networkFromFlags(machineApproveOptions{
		NIC: nic, Addresses: opts.Addresses, Gateway: opts.Gateway, DNS: opts.DNS, VLAN: opts.VLAN,
	})
	if err != nil {
		return nil, err
	}

	return uiapi.NetworkFromPB(n, ichorv1alpha1.TalosMachineIdentity{MACAddresses: id.MACAddresses}, nil)
}

// initBootstrapAccess is step 4: apply the rendered configuration to the node and wait for it to
// come back (Configure node), then bootstrap etcd, fetch and save the kubeconfig, and connect
// (Bootstrap etcd). The wait is the Bootstrap call itself, retried until the node answers.
func initBootstrapAccess(
	ctx context.Context, opts initOptions, deps initDeps, cluster *ichorv1alpha1.TalosCluster, bundle *secrets.Bundle,
	rendered []byte,
) (client.Client, string, error) {
	steps := deps.progress()

	var admin bootstrapClient

	err := steps.Run("Configure node", func(s *step) (string, error) {
		s.Update("applying the configuration")

		maint, err := deps.DialMaintenance(ctx, opts.NodeEndpoint)
		if err != nil {
			return "", fmt.Errorf("init: %w", err)
		}

		if err := maint.ApplyInsecure(ctx, rendered); err != nil {
			return "", fmt.Errorf("init: applying configuration to %q: %w", opts.NodeEndpoint, err)
		}

		s.Update("installing Talos and rebooting")

		if admin, err = deps.DialAdmin(ctx, cluster, opts.NodeEndpoint, bundle); err != nil {
			return "", fmt.Errorf("init: %w", err)
		}

		if err := bootstrapWhenUp(ctx, admin, deps.BootstrapWait, deps.BootstrapInterval, s.Rewrite(initDetail)); err != nil {
			return "", fmt.Errorf("init: bootstrapping etcd: %w", err)
		}

		return opts.NodeEndpoint + " installed Talos and rebooted", nil
	})
	if err != nil {
		return nil, "", err
	}

	var (
		k8sClient     client.Client
		mergedContext string
	)

	err = steps.Run("Bootstrap etcd", func(*step) (string, error) {
		kubeconfig, err := admin.Kubeconfig(ctx)
		if err != nil {
			return "", fmt.Errorf("init: fetching the kubeconfig: %w", err)
		}

		kubeconfigPath, err := saveKubeconfigFile(cluster.Name, kubeconfig)
		if err != nil {
			return "", fmt.Errorf("init: saving the kubeconfig: %w", err)
		}

		detail := "kubeconfig saved to " + tildePath(kubeconfigPath)

		if opts.MergeKubeconfig {
			// The saved copy exists and the cluster is up: a failed merge is a warning, not a failed init.
			if mergedContext, err = mergeIntoUserKubeconfig(cluster.Name, kubeconfig); err != nil {
				steps.Warn(fmt.Sprintf("--merge-kubeconfig: %v; use the saved copy instead", err))
			} else {
				detail += ", merged as context " + mergedContext
			}
		}

		if k8sClient, err = deps.NewK8sClient(kubeconfig); err != nil {
			return "", fmt.Errorf("init: connecting to the new cluster: %w", err)
		}

		return detail, nil
	})

	return k8sClient, mergedContext, err
}

// initDetail turns a message of the wait helpers into the short detail of a step.
func initDetail(msg string) string {
	switch {
	case strings.Contains(msg, "code = Unavailable"):
		return "waiting for the node's Talos API"
	case strings.Contains(msg, "bootstrap is not available yet"):
		return "waiting for etcd to start"
	case strings.Contains(msg, "connection refused"):
		return "connection refused"
	}

	if m := deploymentRolloutRE.FindStringSubmatch(msg); m != nil {
		return fmt.Sprintf("rolling out (%s/%s available)", m[1], m[2])
	}

	if m := appliedObjectsRE.FindStringSubmatch(msg); m != nil {
		return m[1] + " objects"
	}

	return msg
}

var (
	deploymentRolloutRE = regexp.MustCompile(`^the Deployment \S+ is not rolled out \(\d+ updated, (\d+) available of (\d+)\)$`)
	appliedObjectsRE    = regexp.MustCompile(`^applied .* \((\d+) objects\)$`)
)

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
// requested output format (design: "-o yaml|json"). Each object created is the step's detail;
// stdout carries only the objects themselves, and only with -o.
func createCRs(
	ctx context.Context, create func(context.Context, client.Object) error,
	cluster *ichorv1alpha1.TalosCluster, machine *ichorv1alpha1.TalosMachine, candidates []ichorv1alpha1.TalosConfigPatch,
	stdout io.Writer, output string, s *step,
) error {
	created := func(kind string, obj client.Object) {
		s.Update(fmt.Sprintf("created %s %s/%s", kind, obj.GetNamespace(), obj.GetName()))
	}

	if err := create(ctx, cluster); err != nil {
		return fmt.Errorf("init: creating TalosCluster: %w", err)
	}

	created("TalosCluster", cluster)

	if err := create(ctx, machine); err != nil {
		return fmt.Errorf("init: creating TalosMachine: %w", err)
	}

	created("TalosMachine", machine)

	for i := range candidates {
		if err := create(ctx, &candidates[i]); err != nil {
			return fmt.Errorf("init: creating TalosConfigPatch %q: %w", candidates[i].Name, err)
		}

		created("TalosConfigPatch", &candidates[i])
	}

	// With no -o given (output ""), only the steps' lines; the objects themselves only on -o yaml|json.
	if output == "" {
		return nil
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
// command yet.
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

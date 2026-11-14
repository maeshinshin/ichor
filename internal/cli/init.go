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
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/internal/installmanifest"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/release"
)

// checkPinnedFlags refuses a version flag that names anything but the release set of this Ichor
// build. An empty value means the pinned one, as the flag default does.
func checkPinnedFlags(talos, kubernetes, contract string) error {
	for _, f := range []struct{ flag, value, pinned string }{
		{"--talos-version", talos, release.Pinned.Talos},
		{"--kubernetes-version", kubernetes, release.Pinned.Kubernetes},
		{"--version-contract", contract, release.Pinned.VersionContract},
	} {
		if f.value != "" && f.value != f.pinned {
			return fmt.Errorf("%s %s is refused: this Ichor build supports %s; "+
				"use the Ichor release that supports the version you want", f.flag, f.value, f.pinned)
		}
	}

	return nil
}

// checkInitCompat refuses a first install whose Talos, Kubernetes and Cilium the compatibility table
// does not hold together. The pinned set passes by construction; the check guards a set
// that is edited without the table.
func checkInitCompat(talos, kubernetes, cilium string) error {
	if err := release.TalosKubernetesError(talos, kubernetes); err != nil {
		return fmt.Errorf("init: %w", err)
	}

	if err := release.CiliumKubernetesError(cilium, kubernetes); err != nil {
		return fmt.Errorf("init: %w", err)
	}

	return nil
}

var initOpts initOptions

// initFile is -f/--file; initPrintTemplate is --print-config-template.
var (
	initFile          string
	initPrintTemplate bool
)

// initWaitTimeout is --wait-timeout: how long init waits for Ichor's controller to serve its webhooks.
var initWaitTimeout = defaultWebhookWait

// initCmd implements `ichor init`, steps 1-5 of
// docs/design/architecture.md's "Bootstrap" (step 6, hand-over, is a
// continuous reconciliation and a different unit's work). Alongside
// `adopt` and full-loss recovery, it is one of the three CLI operations
// the design document says predate the cluster and so never go through
// the Connect API.
var initCmd = &cobra.Command{
	Use:     "init",
	GroupID: groupStart,
	Short:   "Bootstrap a brand-new Talos cluster's first control plane",
	Example: `  ichor init --print-config-template > init.yaml
  ichor init -f init.yaml`,
	Long: `init brings a single, freshly-booted Talos node (in maintenance mode, not
yet configured) under Ichor's management as the first control plane of a
new cluster:

  1. Generates a secrets bundle locally and backs it up to
     ~/.ichor/clusters/<cluster>/secrets.yaml (0600), encrypted with a
     passphrase (--bundle-passphrase-file, $ICHOR_BUNDLE_PASSPHRASE, or one
     generated and shown once on a terminal) -- this is the
     cluster's only root of trust. BACK IT UP, and the passphrase, before
     doing anything else.
  2. Settles the cluster's shape from flags (topology preset, CNI, endpoint).
  3. Renders and applies that configuration to --node-endpoint using
     Talos's insecure apply. THIS STEP IS IRREVERSIBLE: insecure apply does
     not authenticate the node, so a separate provisioning network is
     assumed; the node will install Talos and reboot.
  4. Bootstraps etcd, fetches the kubeconfig (saved alongside the secrets
     bundle), and stores the bundle as a Secret in the new cluster.
  5. Creates the TalosCluster, TalosMachine and any TalosConfigPatch
     objects the CNI needs, and the IchorInstallation.
  6. Waits for the controller to hand Cilium over to Ichor's Helm release
     (TalosCluster CNIHandover).

With --endpoint-type vip the endpoint address is a shared IP that Ichor puts on
every control plane, on its primary link: init takes that link from the node
(the only physical link, or one you pick; --nic names it by link name or permanent MAC),
gives the node DHCP on that link only, and the configuration refers to it as
net0. A control plane approved later needs its own ` + "`--nic`" + ` for the same reason.
The address must be a free IP, not the node's own.

init never applies anything to a node again after step 3; every later
change to this cluster goes through the controller, by writing CRs.

The settings can come from a file instead of flags: -f init.yaml (an
InitConfig; --print-config-template prints an example). A flag on the command
line overrides the file; relative paths in the file are relative to it.

On a terminal, a required setting given by neither is asked for; with no
terminal init fails as before, naming the missing flag.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if initPrintTemplate {
			_, err := fmt.Fprint(cmd.OutOrStdout(), initConfigTemplate)

			return err
		}

		if initFile != "" {
			if err := applyInitFile(cmd.Flags(), initFile); err != nil {
				return err
			}
		}

		if bundleTerminal() {
			if err := promptInitMissing(cmd.Flags(), cmd.InOrStdin(), cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("init: %w", err)
			}
		}

		if err := checkInitRequired(cmd.Flags()); err != nil {
			return err
		}

		if err := validateOutputFormat(outputFormat); err != nil {
			return err
		}

		if err := checkPinnedFlags(initOpts.TalosVersion, initOpts.KubernetesVersion, initOpts.VersionContract); err != nil {
			return err
		}

		if err := checkInitCompat(release.Pinned.Talos, release.Pinned.Kubernetes, release.Pinned.Cilium); err != nil {
			return err
		}

		if err := checkCiliumFeatures(initOpts.initPlanInput, initOpts.NodeEndpoint); err != nil {
			return err
		}

		// Before the passphrase is generated and shown for a file init would not write.
		if err := refuseExistingBundle(initOpts.Cluster); err != nil {
			return err
		}

		if err := chooseInitBundleProtection(cmd, &initOpts); err != nil {
			return err
		}

		deps := initDeps{
			ReadNode:        readNodeOverMaintenance,
			DialMaintenance: dialMaintenance,
			DialAdmin:       dialAdmin,
			NewK8sClient:    newKubeClientFromBytes,
			IchorManifest:   installmanifest.Manifest,
			NetworkPolicies: installmanifest.NetworkPolicies,
			Stdout:          cmd.OutOrStdout(),
			Stderr:          cmd.ErrOrStderr(),
			Stdin:           cmd.InOrStdin(),
			AssumeYes:       assumeYes,
			Output:          initOutput(cmd),
			WebhookWait:     initWaitTimeout,
		}

		return silenceIfShown(cmd, runInit(cmd.Context(), initOpts, deps))
	},
}

// chooseInitBundleProtection decides how the local secrets bundle will be stored before init
// touches the network: a passphrase from --bundle-passphrase-file or $ICHOR_BUNDLE_PASSPHRASE, one
// generated and shown once on a terminal (init then waits for Enter, so it is not scrolled away unseen),
// or plaintext when --bundle-plaintext asks for it. With none of those it refuses.
func chooseInitBundleProtection(cmd *cobra.Command, opts *initOptions) error {
	pass, generated, err := resolveSealPassphrase(opts.BundlePlaintext)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}

	opts.BundlePassphrase = pass

	if !generated {
		return nil
	}

	dir, err := bundleDir(opts.Cluster)
	if err != nil {
		return err
	}

	announceBundlePassphrase(cmd.ErrOrStderr(), tildePath(filepath.Join(dir, secretsbundle.SecretKey)), pass)

	if _, err := readLine(cmd.InOrStdin(), cmd.ErrOrStderr(), "Press Enter once you have saved the passphrase: "); err != nil {
		return fmt.Errorf("init: reading the confirmation: %w", err)
	}

	return nil
}

// silenceIfShown keeps cobra from printing an error a failed step already showed in full; any other
// error is still printed.
func silenceIfShown(cmd *cobra.Command, err error) error {
	if errorShown(err) {
		cmd.SilenceErrors = true
	}

	return err
}

// initOutput is the -o format when it was given, and "" (one line per created object) when it was
// left at its default: the full objects are about two thousand lines of YAML.
func initOutput(cmd *cobra.Command) string {
	if cmd.Flags().Changed("output") {
		return outputFormat
	}

	return ""
}

// nodeReading is what init reads from the first node over one maintenance connection.
type nodeReading struct {
	Identity controller.HardwareIdentity
	// Disks and Links are the node's installable disks and physical links, read only when asked for;
	// a failed read is reported in InventoryErr and does not fail the call.
	Disks        []ichorv1alpha1.TalosMachineDiscoveredDisk
	Links        []ichorv1alpha1.TalosMachineDiscoveredLink
	InventoryErr error
}

// readNodeOverMaintenance reads a node's hardware identity the same way
// discovery does (internal/controller.NewMachineryMaintenanceClient,
// dialed in maintenance mode), and its disks and links too when wantInventory, on the same client; it closes the
// connection afterward: init only ever needs one read per run, not a held connection.
func readNodeOverMaintenance(ctx context.Context, endpoint string, wantInventory bool) (nodeReading, error) {
	c, err := controller.NewMachineryMaintenanceClient(ctx, endpoint)
	if err != nil {
		return nodeReading{}, fmt.Errorf("dialing maintenance Talos API at %q: %w", endpoint, err)
	}

	if closer, ok := c.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}

	var r nodeReading

	if r.Identity, err = c.Identity(ctx); err != nil {
		return r, err
	}

	if wantInventory {
		var inv controller.Inventory

		inv, r.InventoryErr = c.Inventory(ctx)
		r.Disks, r.Links = inv.Disks, inv.Links
	}

	return r, nil
}

// bindInitFlags registers every setting flag of `ichor init` on flags, bound to o and wait. The
// required ones are checked by checkInitRequired, since -f/--file can stand in for them, and asked for
// by promptInitMissing on a terminal.
func bindInitFlags(flags *pflag.FlagSet, o *initOptions, wait *time.Duration) {
	flags.DurationVar(wait, "wait-timeout", defaultWebhookWait,
		"how long to wait for Ichor's controller to serve its admission webhooks before writing the first resources")
	flags.StringVar(&o.Cluster, flagCluster, "", "name of the TalosCluster to create (required)")
	flags.StringVarP(&o.Namespace, "namespace", "n", ichorControllerNamespace,
		"namespace the TalosCluster, its TalosMachine and the secrets bundle Secret live in; "+
			"Ichor's own install manifest always deploys its controller to \""+ichorControllerNamespace+"\"")
	flags.StringVar(&o.Preset, "preset", defaultPreset,
		"topology preset: single (1 node, schedulable), compact-ha (3 control planes, schedulable), "+
			"standard (3 control planes + workers added later, tainted; recommended), or "+
			"large (5 control planes + workers added later, tainted)")
	flags.StringVar(&o.CNIPreset, "cni", cniCilium, "CNI preset; cilium is the only one")
	_ = flags.MarkHidden("cni")
	flags.StringVar(&o.KubeProxy, "kube-proxy", "enabled",
		"kube-proxy mode: enabled (Talos's own, the default) or replaced (Cilium replaces it and reaches the API through KubePrism on port 7445, "+
			"which Talos enables by default); --gateway-api and --l2-announcements need replaced; init only, an existing cluster is not changed by it")
	flags.StringVar(&o.CNITunnel, "cni-tunnel", "",
		"Cilium's tunnel protocol, vxlan or geneve (default: Cilium's vxlan); "+
			"geneve for virtualised networks that pass geneve but not vxlan")
	flags.BoolVar(&o.GatewayAPI, "gateway-api", false,
		"turn on Cilium's Gateway API and put its CRDs in the bootstrap; init only, an existing cluster is not changed by it; "+
			"needs kube-proxy replacement, which init does not build yet, so it is refused for now")
	flags.BoolVar(&o.L2Announcements, "l2-announcements", false,
		"turn on Cilium's L2 announcements, which answer ARP for LoadBalancer addresses on the LAN; init only, an existing cluster is not changed by it; "+
			"needs --l2-pool and kube-proxy replacement, which init does not build yet, so it is refused for now")
	flags.StringVar(&o.L2Pool, "l2-pool", "",
		"with --l2-announcements: the IPv4 CIDR that LoadBalancer addresses come from; init checks it against the node and the endpoint "+
			"and does not write the CiliumLoadBalancerIPPool, which you create yourself")
	flags.StringVar(&o.EndpointType, flagEndpointType, endpointTypes[0].Name, "cluster endpoint type: "+endpointTypeNames(", ", " or "))
	flags.StringVar(&o.EndpointAddress, "endpoint-address", "",
		"cluster endpoint address (required): an IP address that no node has yet for vip, which Ichor puts on the control planes")
	flags.StringVar(&o.NodeEndpoint, "node-endpoint", "",
		"Talos API address (host:port) of the first node, currently in maintenance mode (required)")
	// The versions are the release set of this Ichor build: the flags stay so that scripts
	// keep working, and accept only the value the build pins.
	flags.StringVar(&o.TalosVersion, "talos-version", release.Pinned.Talos, "Talos release (the release set of this Ichor build; any other value is refused)")
	flags.StringVar(&o.KubernetesVersion, "kubernetes-version", release.Pinned.Kubernetes, "Kubernetes release (the release set of this Ichor build; any other value is refused)")
	flags.StringVar(&o.VersionContract, "version-contract", release.Pinned.VersionContract, "Talos version contract (the release set of this Ichor build; any other value is refused)")
	for _, name := range []string{"talos-version", "kubernetes-version", "version-contract"} {
		_ = flags.MarkDeprecated(name, "the version is set by this Ichor build; the flag only accepts its value")
	}
	flags.StringVar(&o.InstallerDigest, "installer-digest", "",
		"pin Ichor's installer image to this digest (sha256:...) in the IchorInstallation instead of letting the controller resolve the version's tag; "+
			"for a cluster whose controller cannot reach the registry (a demo behind a mirror)")
	flags.BoolVar(&o.SkipSignatureCheck, "skip-signature-check", false,
		"run the installer image WITHOUT checking its signature (for builds nobody signs: development and demo images)")
	flags.StringVar(&o.NIC, flagNIC, "",
		"the first node's primary network link, by its name (enp1s0) or permanent MAC (aa:bb:cc:dd:ee:ff), "+
			"the link its addressing goes on (a vip is announced on it too); like approve node --nic. A name is looked up among the links the node reports and only "+
			"its MAC is kept. Needed only when the node has more than one physical link")
	flags.StringSliceVar(&o.Addresses, "address", nil,
		"static address of the first node's primary link with prefix length, 192.168.1.10/24 (repeatable); "+
			"without it the node uses DHCP on that link. Same as approve node --address")
	flags.StringVar(&o.Gateway, "gateway", "",
		"with --address: default gateway. Left out, the configuration adds no default route; a route the node already has from its "+
			"platform configuration (Talos merges the layers) is kept")
	flags.StringSliceVar(&o.DNS, flagDNS, nil, "DNS server address for the first node (repeatable)")
	flags.Int32Var(&o.VLAN, "vlan", 0, "VLAN ID (1-4094) to put the first node's addressing on")
	flags.StringVar(&o.InstallDisk, "install-disk", "", "the first node's install disk: "+installDiskUsage+" (required)")
	flags.StringSliceVar(&o.PodSubnets, "pod-subnets", nil,
		"pod CIDRs (spec.network.podSubnets; at most one per IP family); unset keeps Talos's default 10.244.0.0/16. Fixed once the cluster exists")
	flags.StringSliceVar(&o.ServiceSubnets, "service-subnets", nil,
		"service CIDRs (spec.network.serviceSubnets); unset keeps Talos's default 10.96.0.0/12. Fixed once the cluster exists")
	flags.StringArrayVar(&o.RegistryMirrors, "registry-mirror", nil,
		"anonymous registry mirror as HOST=URL (spec.registries), e.g. ghcr.io=http://10.0.0.5:5000; repeatable")
	flags.StringArrayVar(&o.CertSANs, "cert-san", nil,
		"extra name or IP address that the Talos API and kube-apiserver certificates both carry (spec.certSANs); repeatable. "+
			"Each node's own addresses are already in its certificates, and the endpoint is in the kube-apiserver one. "+
			"Changing the list after init reissues the certificates and reboots every node, so set it now")
	flags.StringArrayVar(&o.DiscoverAddresses, "discover-address", nil,
		"IPv4 address of a node booted into maintenance mode for the controller to probe (spec.discovery.addresses); repeatable")
	flags.StringArrayVar(&o.DiscoverCIDRs, "discover-cidr", nil,
		"IPv4 CIDR (/24 or smaller) of maintenance-mode nodes for the controller to probe (spec.discovery.cidrs); repeatable")
	flags.StringVar(&o.DiskRoles.EphemeralDiskSelector, "ephemeral-disk-selector", "",
		"CEL disk selector (e.g. disk.wwid == \"...\") for a dedicated EPHEMERAL disk, separate from --install-disk")
	flags.StringVar(&o.DiskRoles.StorageDiskSelector, "storage-disk-selector", "",
		"CEL disk selector for a disk prepared for a storage system (RawVolumeConfig)")
	flags.StringVar(&o.DiskRoles.StorageVolumeName, "storage-volume-name", "", "name for --storage-disk-selector's volume (default \"storage\")")
	flags.StringVar(&o.DiskRoles.UserVolumeDiskSelector, "user-volume-disk-selector", "", "CEL disk selector for a UserVolumeConfig disk")
	flags.StringVar(&o.DiskRoles.UserVolumeName, "user-volume-name", "", "name for --user-volume-disk-selector's volume (default \"data\")")

	flags.StringVar(&o.UIURL, "ui-url", "",
		"turn Ichor's own users on: the https URL at which browsers and the CLI reach ichor-ui, the issuer of every token; "+
			"needs --version-contract v1.14 or later")
	flags.StringVar(&o.AdminUser, "admin-user", "",
		"with --ui-url: name of the first admin (default "+defaultAdminUser+"); its generated password is printed once to stderr "+
			"and must be changed at the first sign-in")
	flags.StringVar(&o.UITLSSecret, "ui-tls-secret", "",
		"with --ui-url: serve ichor-ui over TLS with this existing kubernetes.io/tls Secret in "+ichorControllerNamespace+" (chart: ichorUI.tls.secretName)")
	flags.StringVar(&o.UITLSCert, "ui-tls-cert", "", "with --ui-url: PEM certificate file for ichor-ui's TLS; init stores it with --ui-tls-key as a Secret")
	flags.StringVar(&o.UITLSKey, "ui-tls-key", "", "with --ui-tls-cert: PEM private key file (never printed)")
	flags.StringVar(&o.KubeAPIServerURL, "kube-api-server-url", "",
		"with --ui-url: https URL of the Kubernetes API put in users' kubeconfigs (chart: ichorUI.users.kubeAPIServerURL)")
	flags.StringVar(&o.KubeAPITLSServerName, "kube-api-tls-server-name", "",
		"with --ui-url: tls-server-name in users' kubeconfigs (chart: ichorUI.users.kubeAPITLSServerName)")
}

func init() {
	rootCmd.AddCommand(initCmd)

	flags := initCmd.Flags()
	bindInitFlags(flags, &initOpts, &initWaitTimeout)
	flags.StringVarP(&initFile, "file", "f", "", "read the init settings from this "+initConfigKind+" file (see --print-config-template); a flag given on the command line overrides the file")
	flags.BoolVar(&initOpts.MergeKubeconfig, "merge-kubeconfig", false,
		"also merge the cluster-admin kubeconfig into your kubeconfig (--kubeconfig, else the first file of $KUBECONFIG, else ~/.kube/config) as context admin@CLUSTER, and make it current; a taken name gets a suffix")
	flags.BoolVar(&initOpts.BundlePlaintext, bundlePlaintextFlag, false,
		"store the secrets bundle unencrypted (default: encrypted with --bundle-passphrase-file, $"+bundlePassphraseEnv+", or a passphrase generated on a terminal)")
	flags.BoolVar(&initPrintTemplate, "print-config-template", false, "print an example "+initConfigKind+" file and exit")
}

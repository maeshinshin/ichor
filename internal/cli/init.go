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
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/internal/installmanifest"
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
	Use:   "init",
	Short: "Bootstrap a brand-new Talos cluster's first control plane",
	Long: `init brings a single, freshly-booted Talos node (in maintenance mode, not
yet configured) under Ichor's management as the first control plane of a
new cluster:

  1. Generates a secrets bundle locally and backs it up to
     ~/.ichor/clusters/<cluster>/secrets.yaml (0600) -- this is the
     cluster's only root of trust. BACK IT UP before doing anything else.
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

init never applies anything to a node again after step 3; every later
change to this cluster goes through the controller, by writing CRs.

The settings can come from a file instead of flags: -f init.yaml (an
InitConfig; --print-config-template prints an example). A flag on the command
line overrides the file; relative paths in the file are relative to it.`,
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

		deps := initDeps{
			Identity:        identityOverMaintenance,
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

// identityOverMaintenance reads a node's hardware identity the same way
// discovery does (internal/controller.NewMachineryMaintenanceClient,
// dialed in maintenance mode), closing the connection afterward: init
// only ever needs one identity read per run, not a held connection.
func identityOverMaintenance(ctx context.Context, endpoint string) (controller.HardwareIdentity, error) {
	c, err := controller.NewMachineryMaintenanceClient(ctx, endpoint)
	if err != nil {
		return controller.HardwareIdentity{}, fmt.Errorf("dialing maintenance Talos API at %q: %w", endpoint, err)
	}

	if closer, ok := c.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}

	return c.Identity(ctx)
}

// bindInitFlags registers every setting flag of `ichor init` on flags, bound to o and wait. The
// required ones are checked by checkInitRequired, since -f/--file can stand in for them.
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
	flags.StringVar(&o.CNITunnel, "cni-tunnel", "",
		"Cilium's tunnel protocol, vxlan or geneve (default: Cilium's vxlan); "+
			"geneve for virtualised networks that pass geneve but not vxlan")
	flags.StringVar(&o.EndpointType, "endpoint-type", "vip", "cluster endpoint type: vip, loadBalancer, dns or node")
	flags.StringVar(&o.EndpointAddress, "endpoint-address", "", "cluster endpoint address (required)")
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
	flags.StringVar(&o.InstallDisk, "install-disk", "", "the first node's install disk: "+installDiskUsage+" (required)")
	flags.StringSliceVar(&o.PodSubnets, "pod-subnets", nil,
		"pod CIDRs (spec.network.podSubnets; at most one per IP family); unset keeps Talos's default 10.244.0.0/16. Fixed once the cluster exists")
	flags.StringSliceVar(&o.ServiceSubnets, "service-subnets", nil,
		"service CIDRs (spec.network.serviceSubnets); unset keeps Talos's default 10.96.0.0/12. Fixed once the cluster exists")
	flags.StringArrayVar(&o.RegistryMirrors, "registry-mirror", nil,
		"anonymous registry mirror as HOST=URL (spec.registries), e.g. ghcr.io=http://10.0.0.5:5000; repeatable")
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
	flags.BoolVar(&initPrintTemplate, "print-config-template", false, "print an example "+initConfigKind+" file and exit")
}

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

	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/spf13/cobra"

	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/internal/installmanifest"
)

var initOpts initOptions

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
     objects the CNI needs. With --gitops-dir, also writes them as YAML
     and generates a GitOps repository skeleton.

init never applies anything to a node again after step 3; every later
change to this cluster goes through the controller, by writing CRs.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := validateOutputFormat(outputFormat); err != nil {
			return err
		}

		if err := validateKokumiOptions(initOpts); err != nil {
			return err
		}

		if initOpts.RepoPath == "" {
			initOpts.RepoPath = "clusters/" + initOpts.Cluster
		}

		deps := initDeps{
			Identity:        identityOverMaintenance,
			DialMaintenance: dialMaintenance,
			DialAdmin:       dialAdmin,
			NewK8sClient:    newKubeClientFromBytes,
			IchorManifest:   installmanifest.Manifest,
			Stdout:          cmd.OutOrStdout(),
			Stderr:          cmd.ErrOrStderr(),
			Stdin:           cmd.InOrStdin(),
			AssumeYes:       assumeYes,
			Output:          outputFormat,
		}

		return runInit(cmd.Context(), initOpts, deps)
	},
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

func init() {
	rootCmd.AddCommand(initCmd)

	flags := initCmd.Flags()
	flags.StringVar(&initOpts.Cluster, flagCluster, "", "name of the TalosCluster to create (required)")
	flags.StringVarP(&initOpts.Namespace, "namespace", "n", ichorControllerNamespace,
		"namespace the TalosCluster, its TalosMachine and the secrets bundle Secret live in; "+
			"Ichor's own install manifest always deploys its controller to \""+ichorControllerNamespace+"\"")
	flags.StringVar(&initOpts.Preset, "preset", defaultPreset,
		"topology preset: single (1 node, schedulable), compact-ha (3 control planes, schedulable), "+
			"standard (3 control planes + workers added later, tainted; recommended), or "+
			"large (5 control planes + workers added later, tainted)")
	flags.StringVar(&initOpts.CNIPreset, "cni", "", "CNI preset: flannel or cilium (required)")
	flags.StringVar(&initOpts.CNITunnel, "cni-tunnel", "",
		"with --cni cilium: Cilium's tunnel protocol, vxlan or geneve (default: Cilium's vxlan); "+
			"geneve for virtualised networks that pass geneve but not vxlan")
	flags.StringVar(&initOpts.EndpointType, "endpoint-type", "vip", "cluster endpoint type: vip, loadBalancer, dns or node")
	flags.StringVar(&initOpts.EndpointAddress, "endpoint-address", "", "cluster endpoint address (required)")
	flags.StringVar(&initOpts.NodeEndpoint, "node-endpoint", "",
		"Talos API address (host:port) of the first node, currently in maintenance mode (required)")
	flags.StringVar(&initOpts.TalosVersion, "talos-version", "", "Talos release this cluster renders configuration for (required, vX.Y.Z)")
	flags.StringVar(&initOpts.KubernetesVersion, "kubernetes-version", "v"+constants.DefaultKubernetesVersion, "Kubernetes release this cluster runs")
	flags.StringVar(&initOpts.VersionContract, "version-contract", "", "Talos version contract (required, vX.Y)")
	flags.StringVar(&initOpts.InstallDisk, "install-disk", "", "the first node's install disk: "+installDiskUsage+" (required)")
	flags.StringSliceVar(&initOpts.PodSubnets, "pod-subnets", nil,
		"pod CIDRs (spec.network.podSubnets; at most one per IP family); unset keeps Talos's default 10.244.0.0/16. Fixed once the cluster exists")
	flags.StringSliceVar(&initOpts.ServiceSubnets, "service-subnets", nil,
		"service CIDRs (spec.network.serviceSubnets); unset keeps Talos's default 10.96.0.0/12. Fixed once the cluster exists")
	flags.StringArrayVar(&initOpts.RegistryMirrors, "registry-mirror", nil,
		"anonymous registry mirror as HOST=URL (spec.registries), e.g. ghcr.io=http://10.0.0.5:5000; repeatable")
	flags.StringArrayVar(&initOpts.DiscoverAddresses, "discover-address", nil,
		"IPv4 address of a node booted into maintenance mode for the controller to probe (spec.discovery.addresses); repeatable")
	flags.StringArrayVar(&initOpts.DiscoverCIDRs, "discover-cidr", nil,
		"IPv4 CIDR (/24 or smaller) of maintenance-mode nodes for the controller to probe (spec.discovery.cidrs); repeatable")
	flags.StringVar(&initOpts.DiskRoles.EphemeralDiskSelector, "ephemeral-disk-selector", "",
		"CEL disk selector (e.g. disk.wwid == \"...\") for a dedicated EPHEMERAL disk, separate from --install-disk")
	flags.StringVar(&initOpts.DiskRoles.StorageDiskSelector, "storage-disk-selector", "",
		"CEL disk selector for a disk prepared for a storage system (RawVolumeConfig)")
	flags.StringVar(&initOpts.DiskRoles.StorageVolumeName, "storage-volume-name", "", "name for --storage-disk-selector's volume (default \"storage\")")
	flags.StringVar(&initOpts.DiskRoles.UserVolumeDiskSelector, "user-volume-disk-selector", "", "CEL disk selector for a UserVolumeConfig disk")
	flags.StringVar(&initOpts.DiskRoles.UserVolumeName, "user-volume-name", "", "name for --user-volume-disk-selector's volume (default \"data\")")

	flags.BoolVar(&initOpts.ArgoCD, "argocd", false, "bootstrap Argo CD's repository credentials and GitOps CRs for this cluster")
	flags.StringVar(&initOpts.ArgoCDNamespace, "argocd-namespace", "argocd", "namespace Argo CD runs in (for the repository credentials Secret)")
	flags.BoolVar(&initOpts.Kokumi, "kokumi", false,
		"with --argocd: also prepare the Argo CD repo-creds Secret for Kokumi's OCI registry (installs neither Argo CD nor Kokumi)")
	flags.StringVar(&initOpts.RepoURL, "repo", "", "Git repository URL for GitOps (required with --argocd)")
	flags.StringVar(&initOpts.RepoPath, "path", "", "path within --repo for this cluster's CRs (defaults to clusters/<cluster>)")
	flags.StringVar(&initOpts.RepoUsername, "repo-username", "", "username for --repo, if it is private")
	flags.StringVar(&initOpts.RepoPassword, "repo-password", "", "password or token for --repo, if it is private")

	flags.StringVar(&initOpts.UIURL, "ui-url", "",
		"turn Ichor's own users on: the https URL at which browsers and the CLI reach ichor-ui, the issuer of every token; "+
			"needs --version-contract v1.14 or later")
	flags.StringVar(&initOpts.AdminUser, "admin-user", "",
		"with --ui-url: name of the first admin (default "+defaultAdminUser+"); its generated password is printed once to stderr "+
			"and must be changed at the first sign-in")
	flags.StringVar(&initOpts.UITLSSecret, "ui-tls-secret", "",
		"with --ui-url: serve ichor-ui over TLS with this existing kubernetes.io/tls Secret in "+ichorControllerNamespace+" (chart: ichorUI.tls.secretName)")
	flags.StringVar(&initOpts.UITLSCert, "ui-tls-cert", "", "with --ui-url: PEM certificate file for ichor-ui's TLS; init stores it with --ui-tls-key as a Secret")
	flags.StringVar(&initOpts.UITLSKey, "ui-tls-key", "", "with --ui-tls-cert: PEM private key file (never printed)")
	flags.StringVar(&initOpts.KubeAPIServerURL, "kube-api-server-url", "",
		"with --ui-url: https URL of the Kubernetes API put in users' kubeconfigs (chart: ichorUI.users.kubeAPIServerURL)")
	flags.StringVar(&initOpts.KubeAPITLSServerName, "kube-api-tls-server-name", "",
		"with --ui-url: tls-server-name in users' kubeconfigs (chart: ichorUI.users.kubeAPITLSServerName)")
	flags.StringVar(&initOpts.GitOpsDir, "gitops-dir", "", "write the GitOps repository skeleton to this local directory")

	for _, name := range []string{flagCluster, cniKey, "endpoint-address", "node-endpoint", "talos-version", "version-contract", "install-disk"} {
		_ = initCmd.MarkFlagRequired(name)
	}
}

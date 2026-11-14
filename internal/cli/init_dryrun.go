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
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/semver"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
	"github.com/maeshinshin/ichor/pkg/render"
)

// keepReading, for a dry run, wraps read so that every call also reads the node's disks and links, and keeps the
// whole reading in *got: the plan names both, whatever the run would have asked for. Otherwise it returns read.
func keepReading(read func(context.Context, string, bool) (nodeReading, error), got *nodeReading, dry bool) func(context.Context, string, bool) (nodeReading, error) {
	if !dry {
		return read
	}

	return func(ctx context.Context, endpoint string, _ bool) (nodeReading, error) {
		r, err := read(ctx, endpoint, true)
		*got = r

		return r, err
	}
}

// dryRunInit ends a dry run in place of the first write. It runs what the real run would still refuse (the
// install manifest, the registry credentials, the render) with a bundle held in memory and kept nowhere, then
// prints the plan. The rendered configuration is never printed: it carries keys and tokens.
func dryRunInit(
	deps initDeps, opts initOptions, cluster *ichorv1alpha1.TalosCluster, machine *ichorv1alpha1.TalosMachine,
	cniBoot *cniBootstrap, reading nodeReading,
) error {
	if err := checkInstallManifest(deps.IchorManifest); err != nil {
		return err
	}

	if err := rejectRegistryCredentials(cluster); err != nil {
		return err
	}

	contract, err := coreconfig.ParseContractFromVersion(opts.VersionContract)
	if err != nil {
		return fmt.Errorf("init: parsing --version-contract: %w", err)
	}

	bundle, err := secrets.NewBundle(secrets.NewClock(), contract)
	if err != nil {
		return fmt.Errorf("init: generating the secrets bundle: %w", err)
	}

	candidates := candidatePatches(cluster, machine, cniBoot, opts.NodeEndpoint, opts.DiskRoles)
	if _, _, err := render.Render(cluster, machine, bundle, candidates, nil); err != nil {
		return fmt.Errorf("init: rendering the first node's configuration: %w", err)
	}

	dir, err := bundleDir(cluster.Name)
	if err != nil {
		return err
	}

	printInitPlan(deps.Stdout, opts, cluster, machine, reading, dir, len(candidates))

	return nil
}

// printInitPlan writes what the run would do, in the order it would do it, under the settings and the node as read.
// Nothing here is a value the run is not going to use, so each line can be checked against the node or the flags.
func printInitPlan(
	w io.Writer, opts initOptions, cluster *ichorv1alpha1.TalosCluster, machine *ichorv1alpha1.TalosMachine,
	reading nodeReading, dir string, patches int,
) {
	spec := machine.Spec
	line := func(label, text string) { _, _ = fmt.Fprintf(w, "%-10s%s\n", label, text) }

	_, _ = fmt.Fprintln(w, "Dry run: nothing is written, and nothing is sent to a node.")
	line("Shape", initSummary(opts))
	line("Endpoint", opts.EndpointType+" "+opts.EndpointAddress)
	line("Node", fmt.Sprintf("%s: UUID %s, serial %s, MAC %s, running Talos %s", opts.NodeEndpoint, spec.Identity.UUID,
		spec.Identity.Serial, strings.Join(spec.Identity.MACAddresses, ", "), reading.TalosVersion))
	line("Disk", installDiskLine(spec.Install.Disk, reading))
	line("Link", linkLine(spec.Network))

	encryption := "encrypted"
	if opts.BundlePlaintext {
		encryption = "UNENCRYPTED (--bundle-plaintext)"
	}

	_, _ = fmt.Fprintln(w, "Would:")
	_, _ = fmt.Fprintf(w, "  1. save the secrets bundle to %s (%s)\n", tildePath(filepath.Join(dir, "secrets.yaml")), encryption)
	_, _ = fmt.Fprintf(w, "  2. apply the first control plane's configuration to %s; IRREVERSIBLE: the node installs Talos and reboots\n", opts.NodeEndpoint)
	_, _ = fmt.Fprintf(w, "  3. bootstrap etcd and save the kubeconfig to %s\n", tildePath(filepath.Join(dir, kubeconfigFilename)))

	applied := "Ichor's install manifest and the secrets bundle Secret"
	if opts.GatewayAPI {
		applied += " and the Gateway API CRDs"
	}

	_, _ = fmt.Fprintf(w, "  4. apply through the Kubernetes API: %s\n", applied)

	created := fmt.Sprintf("TalosCluster %s, TalosMachine %s, %d TalosConfigPatch", cluster.Name, spec.Identity.UUID, patches)
	if semver.IsValid(version.Version) {
		created += ", IchorInstallation"
	} else {
		created += fmt.Sprintf(" (no IchorInstallation: this binary is %q, not a released version)", version.Version)
	}

	_, _ = fmt.Fprintf(w, "  5. create %s, in namespace %s\n", created, cluster.Namespace)
	_, _ = fmt.Fprintln(w, "Result: the run would proceed.")
}

// installDiskLine names the disk the install selector matches, as the node reports it. A selector that matches no
// reported disk is said so, since the run would still send it on.
func installDiskLine(sel ichorv1alpha1.TalosMachineDisk, reading nodeReading) string {
	if reading.InventoryErr != nil {
		return fmt.Sprintf("the install disk is not checked: the node's disks could not be listed (%v)", reading.InventoryErr)
	}

	i := slices.IndexFunc(reading.Disks, func(d ichorv1alpha1.TalosMachineDiscoveredDisk) bool {
		return ichorv1alpha1.DiskSelectorMatches(sel, d)
	})
	if i < 0 {
		return "the install disk matches none of the disks the node reports"
	}

	d := reading.Disks[i]

	return fmt.Sprintf("%s (%s, serial %s), as --install-disk %s", d.DevPath, d.Model, d.Serial, diskFlagValue(d))
}

// linkLine names the primary link and its addressing, or says the node keeps DHCP on every physical link.
func linkLine(n *ichorv1alpha1.TalosMachineNetwork) string {
	if n == nil {
		return "DHCP on every physical link (no network flags given)"
	}

	parts := []string{n.MAC, n.Addressing}
	if len(n.Addresses) > 0 {
		parts = append(parts, strings.Join(n.Addresses, ", "))
	}

	return strings.Join(parts, ", ")
}

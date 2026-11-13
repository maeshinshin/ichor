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

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var machineApproveOpts machineApproveOptions

type machineApproveOptions struct {
	Role     string
	Labels   map[string]string
	Hostname string
	// InstallDisk is the system disk (see parseDiskSelector); InstallImage the installer image.
	InstallDisk  string
	InstallImage string

	// NIC is the primary link's permanent MAC; Addresses (CIDR), Gateway, DNS and VLAN describe how
	// it is addressed (see networkFromFlags). EphemeralDisk and UserVolumes are disk roles (see
	// disksFromFlags).
	NIC           string
	Addresses     []string
	Gateway       string
	DNS           []string
	VLAN          int32
	EphemeralDisk string
	UserVolumes   []string
}

var approveNodeCmd = &cobra.Command{
	Use:   "node NAME --role controlplane|worker",
	Short: "Approve a pending node so the controller starts configuring it",
	Long: `approve node sets the role (and optionally node labels, a hostname and the
install disk, network link and disk roles) of a discovered node and marks it approved; the controller then renders and
applies its configuration. It is refused for a node whose identity is
weak (missing UUID, serial number or permanent MAC) and for one already
approved -- by ichor-ui, or, when ichor-ui is unreachable, by the same
checks running in this process. It asks for confirmation unless --yes.`,
	Example: `  ichor approve node 3f1c2a --role worker
  ichor approve node 9b04de --role controlplane --hostname cp-1 --install-disk /dev/nvme0n1`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runMachineApprove(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], machineApproveOpts)
		})
	},
}

func runMachineApprove(
	ctx context.Context, c *apiClients, in io.Reader, out, errw io.Writer, name string, opts machineApproveOptions,
) error {
	role, err := parseRole(opts.Role)
	if err != nil {
		return err
	}

	var install *pb.MachineInstall

	switch {
	case opts.InstallDisk != "":
		disk, err := parseDiskSelector("--install-disk", opts.InstallDisk)
		if err != nil {
			return err
		}

		install = installDiskToPB(disk, opts.InstallImage)
	case opts.InstallImage != "":
		return fmt.Errorf("--install-image needs --install-disk")
	}

	network, err := networkFromFlags(opts)
	if err != nil {
		return err
	}

	disks, err := disksFromFlags(opts)
	if err != nil {
		return err
	}

	ref := objectRef(apiOpts.Namespace, name)

	if !assumeYes {
		got, err := c.Machine.GetMachine(ctx, connect.NewRequest(&pb.GetMachineRequest{Machine: ref}))
		if err != nil {
			return err
		}

		m := got.Msg.GetMachine()

		if err := confirmOrAbort(in, errw, "machine approve", fmt.Sprintf(
			"Approve machine %s/%s (uuid %q, endpoint %s) as %s? The controller will start configuring it. [y/N] ",
			ref.GetNamespace(), name, m.GetIdentity().GetUuid(), m.GetEndpoint(), opts.Role), "aborted"); err != nil {
			return err
		}
	}

	resp, err := c.Machine.ApproveMachine(ctx, connect.NewRequest(&pb.ApproveMachineRequest{
		Machine: ref, Role: role, NodeLabels: opts.Labels, Hostname: opts.Hostname,
		Install: install, Network: network, Disks: disks,
	}))
	if err != nil {
		return err
	}

	if outputFormat == outputHuman {
		return renderMachineApproved(newHuman(out), newHuman(errw), resp.Msg)
	}

	// A pick discovery did not record is advice, not a refusal: say so, then approve as asked.
	for _, w := range resp.Msg.GetWarnings() {
		if _, err := fmt.Fprintf(errw, "warning: %s\n", w); err != nil {
			return err
		}
	}

	return writeMessage(out, outputFormat, resp.Msg)
}

func init() {
	addAPIFlags(approveNodeCmd)
	approveCmd.AddCommand(approveNodeCmd)

	f := approveNodeCmd.Flags()
	f.StringVar(&machineApproveOpts.Role, "role", "", "controlplane or worker (required)")
	f.StringToStringVar(&machineApproveOpts.Labels, "label", nil, "node label KEY=VALUE (repeatable)")
	f.StringVar(&machineApproveOpts.Hostname, "hostname", "", "hostname to set")
	f.StringVar(&machineApproveOpts.InstallDisk, "install-disk", "", "system disk Talos installs onto: "+installDiskUsage)
	f.StringVar(&machineApproveOpts.InstallImage, "install-image", "", "installer image (default: the booted schematic's; the cluster's SecureBoot installer when spec.security.secureBoot.mode is Preferred or Required)")
	f.StringVar(&machineApproveOpts.NIC, "nic", "", "primary network link, by its permanent MAC (aa:bb:cc:dd:ee:ff); alone it means DHCP")
	f.StringSliceVar(&machineApproveOpts.Addresses, "address", nil, "static address with prefix length, 192.168.1.10/24 (repeatable; needs --nic)")
	f.StringVar(&machineApproveOpts.Gateway, "gateway", "", "default gateway for the static address")
	f.StringSliceVar(&machineApproveOpts.DNS, "dns", nil, "DNS server address (repeatable; needs --nic)")
	f.Int32Var(&machineApproveOpts.VLAN, "vlan", 0, "VLAN ID (1-4094) to put the addressing on (needs --nic)")
	f.StringVar(&machineApproveOpts.EphemeralDisk, "ephemeral-disk", "", "put EPHEMERAL on its own disk: "+installDiskUsage)
	f.StringArrayVar(&machineApproveOpts.UserVolumes, "user-volume", nil, "NAME=DISK[,size=200GiB] user volume on a disk (repeatable)")
	_ = approveNodeCmd.MarkFlagRequired("role")
}

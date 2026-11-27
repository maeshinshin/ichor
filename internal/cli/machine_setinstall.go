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
	"errors"
	"io"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

type setInstallDiskOptions struct{ Disk, Image string }

var setInstallDiskOpts setInstallDiskOptions

var setInstallDiskCmd = &cobra.Command{
	Use:   "install-disk NODE --install-disk DISK [--install-image REF]",
	Short: "Set the install disk of an approved node that has no configuration applied yet",
	Long: `Set spec.install of a node that was approved without an install disk (Ready is False with reason
InstallDiskUnknown). It is what the install-disk dialog of the UI does, through the same API call and so the same checks:
the node must be approved and must not have a configuration applied yet, because an install target only applies
before the first install. The installer image defaults to the one of the node's cluster.`,
	Example: `  ichor set install-disk 3f1c2a --install-disk /dev/nvme0n1`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runSetInstallDiskCmd(ctx, c, cmd.OutOrStdout(), args[0], setInstallDiskOpts)
		})
	},
}

func runSetInstallDiskCmd(ctx context.Context, c *apiClients, out io.Writer, name string, opts setInstallDiskOptions) error {
	if opts.Disk == "" {
		return errors.New("--install-disk is required (--install-image needs it too)")
	}

	disk, err := parseDiskSelector("--install-disk", opts.Disk)
	if err != nil {
		return err
	}

	resp, err := c.Machine.SetMachineInstall(ctx, connect.NewRequest(&pb.SetMachineInstallRequest{
		Machine: objectRef(apiOpts.Namespace, name), Install: installDiskToPB(disk, opts.Image),
	}))
	if err != nil {
		return err
	}

	if outputFormat != outputHuman {
		return writeMessage(out, outputFormat, resp.Msg)
	}

	h := newHuman(out)
	h.done("set the install disk of %s", machineLabel(name))

	return h.Err()
}

func init() {
	addAPIFlags(setInstallDiskCmd)
	setCmd.AddCommand(setInstallDiskCmd)
	markWrites(setInstallDiskCmd)

	f := setInstallDiskCmd.Flags()
	f.StringVar(&setInstallDiskOpts.Disk, "install-disk", "", "system disk Talos installs onto: "+installDiskUsage)
	f.StringVar(&setInstallDiskOpts.Image, "install-image", "", "installer image (default: the cluster's)")
	_ = setInstallDiskCmd.MarkFlagRequired("install-disk")
}

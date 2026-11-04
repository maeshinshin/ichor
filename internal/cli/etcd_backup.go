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

var etcdBackupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Work with the etcd backups (TalosEtcdBackup)",
}

var etcdBackupNowCmd = &cobra.Command{
	Use:   "now NAME",
	Short: "Take an etcd snapshot now, outside the schedule",
	Long: `now asks the controller of the TalosEtcdBackup NAME for one snapshot outside its schedule. It only sets an annotation on the TalosEtcdBackup; the controller takes, verifies,
encrypts and stores the snapshot, prunes to the retention, and records the request in
status.lastManualBackup. A suspended backup, or one with a request the controller has not started
yet, is refused. Check the result with 'ichor restore --live' (it lists the snapshots) or the
Backups page.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runEtcdBackupNow(ctx, c, cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0])
		})
	},
}

func runEtcdBackupNow(ctx context.Context, c *apiClients, out, errw io.Writer, name string) error {
	resp, err := c.Operation.RequestEtcdBackup(ctx, connect.NewRequest(&pb.RequestEtcdBackupRequest{
		Backup: objectRef(apiOpts.Namespace, name),
	}))
	if err != nil {
		return err
	}

	if outputFormat != outputHuman {
		_, _ = fmt.Fprintf(errw, "ichor: requested a snapshot of %s; the controller takes it shortly\n", name)
	}

	return printAPI(out, resp.Msg, func(h *human, _ *pb.RequestEtcdBackupResponse) error {
		return doneLine(h, "requested a snapshot of backup %s; the controller takes it shortly", nsName(objectRef(apiOpts.Namespace, name)))
	})
}

func init() {
	etcdCmd.AddCommand(etcdBackupCmd)
	etcdBackupCmd.AddCommand(etcdBackupNowCmd)
	markWrites(etcdBackupNowCmd)
}

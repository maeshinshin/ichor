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
	"os"

	"github.com/spf13/cobra"

	"github.com/maeshinshin/ichor/internal/etcdbackup"
)

var restoreOpts restoreOptions

// flagSnapshot names the flag that picks the snapshot, for both restore modes.
const flagSnapshot = "snapshot"

// restoreCmd implements `ichor restore`: --recover-cluster (full-loss
// recovery), one of the CLI operations that predate the cluster and
// so never go through the Connect API, and --live, which asks the
// controller of a running cluster to put a stored snapshot back.
var restoreCmd = &cobra.Command{
	Use:   "restore (--live | --recover-cluster)",
	Short: "Restore etcd from a backup snapshot: into the running cluster (--live) or after losing it (--recover-cluster)",
	Long: `restore --live rewinds a RUNNING cluster to a stored snapshot (disaster recovery after
data corruption or a bad change). It creates an etcd-restore operation; the controller wipes
etcd on every control plane, recovers the snapshot on one, lets the others rejoin and reconciles
every machine again. The Kubernetes API is unavailable meanwhile and EVERYTHING WRITTEN SINCE THE
SNAPSHOT IS LOST. It needs --override (and the override verb on the TalosCluster) and the
cluster's name typed (or --confirm NAME); --yes does not replace the name. Without --snapshot
the newest snapshot of the cluster's backup is used; --backup picks the backup when there are
several. Follow it with --follow.

restore --recover-cluster recovers a cluster whose control planes and
etcd data are gone, using only what survives outside it: the secrets bundle
(~/.ichor/clusters/<cluster>/secrets.yaml, or --secrets-bundle) and the S3
bucket the TalosEtcdBackup wrote to. It does not need Ichor, Kubernetes or
a Secret to be running.

It lists the snapshots under --s3-prefix, decrypts the newest one with the
key derived from the bundle, checks its integrity, and only then uploads it
to ONE freshly-installed control plane (--node-endpoint) and bootstraps etcd
from it, the same as 'talosctl bootstrap --recover-from'. That node must
already run Talos with a machine configuration made from the same secrets
bundle, and its etcd data directory must be empty. THE CLUSTER'S STATE
REVERTS TO THE SNAPSHOT'S TIME. Other control planes then join on their own.

S3 credentials come from ICHOR_S3_ACCESS_KEY_ID, ICHOR_S3_SECRET_ACCESS_KEY
and optionally ICHOR_S3_SESSION_TOKEN (falling back to the AWS_* variables).
They are never accepted as flags, so they do not show in 'ps'.

Without --snapshot the newest usable snapshot is used; one that fails to
decrypt or whose checksum does not match is skipped and reported. With
--snapshot, that snapshot or nothing.

With --live the snapshot is read and decrypted by the controller; no secrets bundle or S3
credentials are needed here.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if restoreLiveOpts.Live {
			if restoreOpts.RecoverCluster {
				return errors.New("restore: choose --live or --recover-cluster, not both")
			}

			// Only --live changes a cluster through Ichor, so only it is refused across a version skew;
			// --recover-cluster must not wait on a cluster that is gone.
			markWrites(cmd)

			if err := skewPreRun(cmd, nil); err != nil {
				return err
			}

			return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
				return runLiveRestore(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), liveRestoreSpec{
					Cluster: restoreOpts.Cluster, Backup: restoreLiveOpts.Backup, Snapshot: restoreOpts.Snapshot,
					Confirm: restoreLiveOpts.Confirm, Follow: restoreLiveOpts.Follow,
				})
			})
		}

		if !restoreOpts.RecoverCluster {
			return errors.New("restore: choose --live (into the running cluster) or --recover-cluster (after losing it)")
		}

		deps := restoreDeps{
			NewStore:  etcdbackup.NewS3Store,
			Dial:      dialRecover,
			Getenv:    os.Getenv,
			ReadFile:  os.ReadFile,
			Stdout:    cmd.OutOrStdout(),
			Stderr:    cmd.ErrOrStderr(),
			Stdin:     cmd.InOrStdin(),
			AssumeYes: assumeYes,
		}

		return runRecoverCluster(cmd.Context(), restoreOpts, deps)
	},
}

func init() {
	rootCmd.AddCommand(restoreCmd)

	addAPIFlags(restoreCmd)

	f := restoreCmd.Flags()
	f.BoolVar(&restoreLiveOpts.Live, "live", false, "restore into the running cluster: the controller wipes etcd on every control plane and recovers a stored snapshot")
	f.StringVar(&restoreLiveOpts.Backup, "backup", "", "with --live: the TalosEtcdBackup to restore from (default the cluster's only one)")
	f.StringVar(&restoreLiveOpts.Confirm, "confirm", "", "with --live: the cluster's name, to go on without being asked to type it")
	f.BoolVar(&restoreLiveOpts.Follow, "follow", false, "with --live: follow the operation until it finishes")
	f.BoolVar(&restoreOpts.RecoverCluster, "recover-cluster", false, "full-loss recovery: bootstrap one new control plane from a snapshot")
	f.StringVar(&restoreOpts.Cluster, flagCluster, "", "name of the cluster (--recover-cluster: locates ~/.ichor/clusters/<name>/secrets.yaml, required; --live: default the only one)")
	f.StringVar(&restoreOpts.SecretsBundle, "secrets-bundle", "", "path to the secrets bundle; default ~/.ichor/clusters/<cluster>/secrets.yaml")
	f.StringVar(&restoreOpts.NodeEndpoint, "node-endpoint", "", "Talos API address (host:port) of the new control plane to recover onto (required)")
	f.StringVar(&restoreOpts.Snapshot, flagSnapshot, "", "object name to restore (etcd-<time>.snapshot.age); default the newest one")
	f.StringVar(&restoreOpts.S3.Endpoint, "s3-endpoint", "", "S3 endpoint URL, https://host[:port] (required)")
	f.StringVar(&restoreOpts.S3.Bucket, "s3-bucket", "", "S3 bucket (required)")
	f.StringVar(&restoreOpts.S3.Region, "s3-region", "", "S3 region, if the service needs one")
	f.StringVar(&restoreOpts.S3Prefix, "s3-prefix", "", "key prefix the backups were written under (required)")
	f.StringVar(&restoreOpts.S3CAFile, "s3-ca-file", "", "PEM file with extra root CAs for the S3 endpoint")
}

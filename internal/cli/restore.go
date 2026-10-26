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
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/maeshinshin/ichor/internal/etcdbackup"
)

var restoreOpts restoreOptions

// restoreCmd implements `ichor restore`. Only --recover-cluster (full-loss
// recovery, ADR 0053) exists so far; it is one of the CLI operations that
// predate the cluster and so never go through the Connect API.
var restoreCmd = &cobra.Command{
	Use:   "restore --recover-cluster",
	Short: "Recover a lost cluster's etcd from a backup snapshot and the local secrets bundle",
	Long: `restore --recover-cluster recovers a cluster whose control planes and
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

Restoring into a LIVE cluster is not implemented yet.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if !restoreOpts.RecoverCluster {
			return errors.New("restore: only --recover-cluster is implemented; restoring into a live cluster is not yet available (ADR 0053)")
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

	f := restoreCmd.Flags()
	f.BoolVar(&restoreOpts.RecoverCluster, "recover-cluster", false, "full-loss recovery: bootstrap one new control plane from a snapshot")
	f.StringVar(&restoreOpts.Cluster, flagCluster, "", "name of the cluster (locates ~/.ichor/clusters/<name>/secrets.yaml) (required)")
	f.StringVar(&restoreOpts.SecretsBundle, "secrets-bundle", "", "path to the secrets bundle; default ~/.ichor/clusters/<cluster>/secrets.yaml")
	f.StringVar(&restoreOpts.NodeEndpoint, "node-endpoint", "", "Talos API address (host:port) of the new control plane to recover onto (required)")
	f.StringVar(&restoreOpts.Snapshot, "snapshot", "", "object name to restore (etcd-<time>.snapshot.age); default the newest usable one")
	f.StringVar(&restoreOpts.S3.Endpoint, "s3-endpoint", "", "S3 endpoint URL, https://host[:port] (required)")
	f.StringVar(&restoreOpts.S3.Bucket, "s3-bucket", "", "S3 bucket (required)")
	f.StringVar(&restoreOpts.S3.Region, "s3-region", "", "S3 region, if the service needs one")
	f.StringVar(&restoreOpts.S3Prefix, "s3-prefix", "", "key prefix the backups were written under (required)")
	f.StringVar(&restoreOpts.S3CAFile, "s3-ca-file", "", "PEM file with extra root CAs for the S3 endpoint")
}

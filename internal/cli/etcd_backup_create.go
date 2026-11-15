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
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/etcdbackup"
)

// createEtcdBackupOpts are the flags of `ichor create etcd-backup`.
var createEtcdBackupOpts struct {
	etcdbackup.NewBackup
	CAFile string
}

var createEtcdBackupCmd = &cobra.Command{
	Use:   "etcd-backup NAME --cluster CLUSTER --s3-endpoint URL --s3-bucket BUCKET",
	Short: "Create a scheduled, encrypted etcd backup to an S3 bucket: the TalosEtcdBackup and its credentials Secret",
	Long: `etcd-backup writes the two objects a scheduled etcd backup needs: a Secret with the S3 credentials, in
the controller's namespace (--ichor-namespace, where the controller reads it), and the TalosEtcdBackup
NAME in --namespace that names it. It does not create the bucket: the bucket must already exist, and
should be outside the cluster, because a restore takes the cluster's own Kubernetes API down and an
in-cluster store with it.

The S3 credentials are never accepted as flags, so they do not show in 'ps' or the shell history.
Set ICHOR_S3_ACCESS_KEY_ID and ICHOR_S3_SECRET_ACCESS_KEY, and optionally ICHOR_S3_SESSION_TOKEN
(falling back to the AWS_* variables), as 'ichor restore cluster' does. --s3-ca-file adds a CA bundle
that signs the endpoint to the Secret.

Snapshots are taken on --schedule (a five-field cron expression in UTC, default every hour), always
encrypted with a key derived from the cluster's secrets bundle, and the newest --keep-last (default 24)
are kept. The prefix defaults to the cluster's name; two clusters need two prefixes.

Setting the credentials Secret and the destination needs the "update" verb on
talosetcdbackups/secret-references. An existing Secret or TalosEtcdBackup of that name is never
overwritten. The controller takes the first snapshot as soon as it sees the object.`,
	Example: `  export ICHOR_S3_ACCESS_KEY_ID=... ICHOR_S3_SECRET_ACCESS_KEY=...
  ichor create etcd-backup nightly --cluster prod --s3-endpoint https://s3.example.com --s3-bucket ichor-backups
  ichor create etcd-backup nightly --cluster prod --s3-endpoint https://s3.example.com --s3-bucket ichor-backups \
    --s3-prefix prod/etcd --schedule '0 2 * * *' --keep-last 14`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Refused before the cluster is dialled: a refusal needs no cluster, and no Secret is written.
		o, creds, err := planEtcdBackup(args[0], apiOpts.Namespace, createEtcdBackupOpts.NewBackup, createEtcdBackupOpts.CAFile, os.Getenv, os.ReadFile)
		if err != nil {
			return err
		}

		k8s, err := newKubeClient(kubeconfigPath)
		if err != nil {
			return err
		}

		return runCreateEtcdBackup(cmd.Context(), k8s, cmd.OutOrStdout(), cmd.ErrOrStderr(), o, creds, apiOpts.IchorNamespace)
	},
}

// planEtcdBackup fills in the defaults, reads the credentials and validates, touching no cluster.
func planEtcdBackup(name, namespace string, o etcdbackup.NewBackup, caFile string,
	getenv func(string) string, readFile func(string) ([]byte, error)) (etcdbackup.NewBackup, etcdbackup.S3Config, error) {
	o.Name, o.Namespace = name, namespace

	if o.Prefix == "" {
		o.Prefix = o.Cluster
	}

	if o.SecretName == "" {
		o.SecretName = o.Name + "-s3"
	}

	if err := o.Validate(); err != nil {
		return o, etcdbackup.S3Config{}, err
	}

	creds, err := s3CredentialsFromEnv(getenv)
	if err != nil {
		return o, creds, err
	}

	if caFile != "" {
		if creds.CAPEM, err = readFile(caFile); err != nil {
			return o, creds, fmt.Errorf("reading --s3-ca-file: %w", err)
		}
	}

	return o, creds, nil
}

// s3CredentialsFromEnv reads the S3 credentials from the variables 'ichor restore cluster' reads.
func s3CredentialsFromEnv(getenv func(string) string) (etcdbackup.S3Config, error) {
	c := etcdbackup.S3Config{
		AccessKeyID:     firstEnv(getenv, "ICHOR_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID"),
		SecretAccessKey: firstEnv(getenv, "ICHOR_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY"),
		SessionToken:    firstEnv(getenv, "ICHOR_S3_SESSION_TOKEN", "AWS_SESSION_TOKEN"),
	}

	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return c, errors.New("set ICHOR_S3_ACCESS_KEY_ID and ICHOR_S3_SECRET_ACCESS_KEY (they are not accepted as flags)")
	}

	return c, nil
}

// runCreateEtcdBackup writes the credentials Secret in ichorNamespace, then the TalosEtcdBackup, and removes
// the Secret again if the TalosEtcdBackup is refused. o is already validated.
func runCreateEtcdBackup(ctx context.Context, k8s ctrlclient.Client, out, errw io.Writer,
	o etcdbackup.NewBackup, creds etcdbackup.S3Config, ichorNamespace string) error {
	var cluster ichorv1alpha1.TalosCluster
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.Cluster}, &cluster); err != nil {
		return fmt.Errorf("getting cluster %q in namespace %q: %w", o.Cluster, o.Namespace, err)
	}

	if reason := etcdbackup.InClusterReason(o.Endpoint, &cluster); reason != "" {
		_, _ = fmt.Fprintln(errw, "warning: "+etcdbackup.InClusterMessage(reason))
	}

	bk := o.Backup()
	if err := k8s.Get(ctx, ctrlclient.ObjectKeyFromObject(bk), &ichorv1alpha1.TalosEtcdBackup{}); err == nil {
		return fmt.Errorf("etcd backup %q already exists in namespace %q; it is not overwritten", o.Name, o.Namespace)
	} else if !apierrors.IsNotFound(err) {
		return err
	}

	sec := etcdbackup.CredentialsSecret(ichorNamespace, o.SecretName, creds)
	if err := k8s.Create(ctx, sec); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("the Secret %q already exists in namespace %q; it is not overwritten (pick another with --credentials-secret)", o.SecretName, ichorNamespace)
		}

		return fmt.Errorf("creating the credentials Secret %q in namespace %q: %w", o.SecretName, ichorNamespace, err)
	}

	if err := k8s.Create(ctx, bk); err != nil {
		// Only the Secret created above: the UID precondition keeps this from deleting a Secret that replaced it.
		uid := sec.UID
		if derr := k8s.Delete(ctx, sec, ctrlclient.Preconditions{UID: &uid}); derr != nil && !apierrors.IsNotFound(derr) {
			_, _ = fmt.Fprintf(errw, "warning: the credentials Secret %q in namespace %q was created and could not be removed again: %v\n",
				o.SecretName, ichorNamespace, derr)
		}

		return fmt.Errorf("creating etcd backup %q: %w", o.Name, err)
	}

	_, _ = fmt.Fprintf(out, "Created etcd backup %s/%s for cluster %s and its credentials Secret %s/%s (content not printed).\n"+
		"The bucket %q is not checked here; the controller takes the first snapshot shortly. "+
		"Look at status.conditions and status.lastSnapshot: kubectl -n %s get talosetcdbackup %s -o yaml\n",
		o.Namespace, o.Name, o.Cluster, ichorNamespace, o.SecretName, o.Bucket, o.Namespace, o.Name)

	return nil
}

func init() {
	addAPIFlags(createEtcdBackupCmd)
	createCmd.AddCommand(createEtcdBackupCmd)
	markWrites(createEtcdBackupCmd)

	f := createEtcdBackupCmd.Flags()
	f.StringVar(&createEtcdBackupOpts.Cluster, flagCluster, "", "name of the TalosCluster to back up (required)")
	f.StringVar(&createEtcdBackupOpts.Endpoint, "s3-endpoint", "", "S3 endpoint URL, https://host[:port] (required)")
	f.StringVar(&createEtcdBackupOpts.Bucket, "s3-bucket", "", "S3 bucket, which must already exist (required)")
	f.StringVar(&createEtcdBackupOpts.Prefix, "s3-prefix", "", "key prefix snapshots are written under (default the cluster's name)")
	f.StringVar(&createEtcdBackupOpts.Region, "s3-region", "", "S3 region, if the service needs one")
	f.StringVar(&createEtcdBackupOpts.CAFile, "s3-ca-file", "", "PEM file with the CA that signs the S3 endpoint, kept in the Secret as ca.crt")
	f.BoolVar(&createEtcdBackupOpts.Insecure, "s3-insecure", false, "allow a plain http:// endpoint: the access key id then crosses the network in clear")
	f.StringVar(&createEtcdBackupOpts.Schedule, "schedule", "", "five-field cron expression in UTC (default every hour, 0 * * * *)")
	f.Int32Var(&createEtcdBackupOpts.KeepLast, "keep-last", 0, "how many snapshots to keep (default 24)")
	f.StringVar(&createEtcdBackupOpts.SecretName, "credentials-secret", "", "name of the credentials Secret (default NAME-s3)")
	_ = createEtcdBackupCmd.MarkFlagRequired(flagCluster)
	_ = createEtcdBackupCmd.MarkFlagRequired("s3-endpoint")
	_ = createEtcdBackupCmd.MarkFlagRequired("s3-bucket")
}

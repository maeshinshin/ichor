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

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// exportBundleCmd implements `ichor export bundle`: it saves the cluster's current
// secrets bundle to ~/.ichor/clusters/<name>/secrets.yaml, and only after reading the saved file
// back and finding it is the live bundle does it tell the controller so, which lowers the
// BundleBackupStale condition a CA rotation raised.
var exportBundleCmd = &cobra.Command{
	Use:   "bundle NAME",
	Short: "Save the cluster's current secrets bundle locally and acknowledge it after a CA rotation",
	Long: `export bundle reads the secrets bundle the cluster is running with from its Secret and
writes it to ~/.ichor/clusters/<name>/secrets.yaml (mode 0600, encrypted with the passphrase of the file it
replaces, or a new one: --bundle-passphrase-file, $ICHOR_BUNDLE_PASSPHRASE, or generated on a terminal and shown
once). A CA rotation replaces the bundle, and a
copy saved before it no longer matches any node, so recovering a lost cluster (ichor restore
cluster) needs this one.

After the file is written it is read back; only if it holds the live bundle does the command mark the
TalosCluster as exported, which clears its BundleBackupStale condition. The bundle is never printed.
It refuses while a CA rotation is still under way, because the bundle is about to change.`,
	Example: `  ichor export bundle prod
  ichor export bundle prod --bundle-passphrase-file /run/secrets/bundle-pass`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k8s, err := newKubeClient(kubeconfigPath)
		if err != nil {
			return err
		}

		save := func(cluster string, bundle *secrets.Bundle) (string, error) {
			return saveSealedBundle(cluster, bundle, bundleExportPlaintext, true, cmd.ErrOrStderr())
		}

		return runBundleExport(cmd.Context(), bundleExportDeps{
			K8s: k8s, Save: save, Load: loadBundleFile,
			SaveRetired: func(cluster string, retired *secretsbundle.Retired, bundlePath string) (string, error) {
				return saveRetiredFile(cluster, retired, bundlePath, cmd.ErrOrStderr())
			},
			LoadRetired: loadRetiredFile, Stdout: cmd.OutOrStdout(),
		}, apiOpts.Namespace, apiOpts.IchorNamespace, args[0])
	},
}

// bundleExportDeps are runBundleExport's seams; the real ones touch ~/.ichor and a cluster.
type bundleExportDeps struct {
	K8s  client.Client
	Save func(cluster string, bundle *secrets.Bundle) (string, error)
	Load func(path string) (*secrets.Bundle, error)
	// SaveRetired and LoadRetired write and read back the keys a key rotation retired. They are
	// only called for a cluster that has retired keys; nil there is an error, not a silent skip.
	// The retired keys are stored like the bundle file next to which they go: sealed under the same
	// passphrase, or plaintext when the bundle is.
	SaveRetired func(cluster string, retired *secretsbundle.Retired, bundlePath string) (string, error)
	LoadRetired func(bundlePath string) (*secretsbundle.Retired, error)
	Stdout      io.Writer
}

func runBundleExport(ctx context.Context, d bundleExportDeps, namespace, ichorNamespace, name string) error {
	var cluster ichorv1alpha1.TalosCluster
	if err := d.K8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &cluster); err != nil {
		return fmt.Errorf("getting TalosCluster %s/%s: %w", namespace, name, err)
	}

	secretName := cluster.Spec.SecretsRef.Name

	var secret corev1.Secret
	if err := d.K8s.Get(ctx, types.NamespacedName{Namespace: ichorNamespace, Name: secretName}, &secret); err != nil {
		return fmt.Errorf("getting the secrets bundle Secret %s/%s: %w", ichorNamespace, secretName, err)
	}

	if _, rotating := secret.Data[secretsbundle.RotationKey]; rotating {
		return errors.New("a CA rotation is under way and the bundle is about to change; export it after the rotation completes")
	}

	bundle, err := secretsbundle.Load(ctx, d.K8s, ichorNamespace, secretName)
	if err != nil {
		return err
	}

	retired, err := secretsbundle.LoadRetired(ctx, d.K8s, ichorNamespace, secretName)
	if err != nil {
		return err
	}

	want, err := secretsbundle.ExportFingerprint(bundle, retired)
	if err != nil {
		return err
	}

	path, err := d.Save(name, bundle)
	if err != nil {
		return err
	}

	saved, err := d.Load(path)
	if err != nil {
		return err
	}

	// The retired keys go next to the bundle and are read back with it: a copy without them cannot open a backup
	// sealed before a key rotation.
	savedRetired := &secretsbundle.Retired{}

	if len(retired.Keys) > 0 {
		if d.SaveRetired == nil || d.LoadRetired == nil {
			return errors.New("this cluster rotated its keys and the retired keys cannot be saved; not acknowledging")
		}

		if _, err := d.SaveRetired(name, retired, path); err != nil {
			return err
		}

		if savedRetired, err = d.LoadRetired(path); err != nil {
			return err
		}
	}

	got, err := secretsbundle.ExportFingerprint(saved, savedRetired)
	if err != nil || got != want {
		return fmt.Errorf("the file written to %s does not hold the cluster's current bundle; not acknowledging", path)
	}

	base := cluster.DeepCopy()
	if cluster.Annotations == nil {
		cluster.Annotations = map[string]string{}
	}

	cluster.Annotations[ichorv1alpha1.BundleExportedAnnotation] = want

	if err := d.K8s.Patch(ctx, &cluster, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("marking TalosCluster %s/%s as exported: %w", namespace, name, err)
	}

	_, err = fmt.Fprintf(d.Stdout, "Saved the current secrets bundle to %s and marked %s as exported.\n"+
		"Keep that file safe: with it and an etcd snapshot a lost cluster can be recovered.\n", path, name)
	if err != nil || len(retired.Keys) == 0 {
		return err
	}

	_, err = fmt.Fprintf(d.Stdout, "The cluster rotated its keys: the retired keys were saved next to it as %s. Keep that file with the bundle, "+
		"it opens the backups sealed before the rotation.\n", retiredFilePath(path))

	return err
}

// bundleExportPlaintext is --bundle-plaintext.
var bundleExportPlaintext bool

func init() {
	addAPIFlags(exportBundleCmd)
	exportCmd.AddCommand(exportBundleCmd)
	exportBundleCmd.Flags().BoolVar(&bundleExportPlaintext, bundlePlaintextFlag, false,
		"store the bundle unencrypted (default: encrypted; an existing encrypted file keeps its passphrase)")
	markWrites(exportBundleCmd)
}

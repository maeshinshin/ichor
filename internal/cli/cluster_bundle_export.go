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

// clusterBundleExportCmd implements `ichor cluster bundle-export`: it saves the cluster's current
// secrets bundle to ~/.ichor/clusters/<name>/secrets.yaml, and only after reading the saved file
// back and finding it is the live bundle does it tell the controller so, which lowers the
// BundleBackupStale condition a CA rotation raised.
var clusterBundleExportCmd = &cobra.Command{
	Use:   "bundle-export NAME",
	Short: "Save the cluster's current secrets bundle locally and acknowledge it after a CA rotation",
	Long: `bundle-export reads the secrets bundle the cluster is running with from its Secret and
writes it to ~/.ichor/clusters/<name>/secrets.yaml (mode 0600, encrypted with the passphrase of the file it
replaces, or a new one: --bundle-passphrase-file, $ICHOR_BUNDLE_PASSPHRASE, or generated on a terminal and shown
once). A CA rotation replaces the bundle, and a
copy saved before it no longer matches any node, so recovering a lost cluster (ichor restore
--recover-cluster) needs this one.

After the file is written it is read back; only if it holds the live bundle does the command mark the
TalosCluster as exported, which clears its BundleBackupStale condition. The bundle is never printed.
It refuses while a CA rotation is still under way, because the bundle is about to change.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k8s, err := newKubeClient(kubeconfigPath)
		if err != nil {
			return err
		}

		save := func(cluster string, bundle *secrets.Bundle) (string, error) {
			return saveSealedBundle(cluster, bundle, bundleExportPlaintext, true, cmd.ErrOrStderr())
		}

		return runBundleExport(cmd.Context(), bundleExportDeps{K8s: k8s, Save: save, Load: loadBundleFile, Stdout: cmd.OutOrStdout()},
			apiOpts.Namespace, apiOpts.IchorNamespace, args[0])
	},
}

// bundleExportDeps are runBundleExport's seams; the real ones touch ~/.ichor and a cluster.
type bundleExportDeps struct {
	K8s    client.Client
	Save   func(cluster string, bundle *secrets.Bundle) (string, error)
	Load   func(path string) (*secrets.Bundle, error)
	Stdout io.Writer
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

	want, err := secretsbundle.BundleFingerprint(bundle)
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

	got, err := secretsbundle.BundleFingerprint(saved)
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

	return err
}

// bundleExportPlaintext is --bundle-plaintext.
var bundleExportPlaintext bool

func init() {
	clusterCmd.AddCommand(clusterBundleExportCmd)
	clusterBundleExportCmd.Flags().BoolVar(&bundleExportPlaintext, bundlePlaintextFlag, false,
		"store the bundle unencrypted (default: encrypted; an existing encrypted file keeps its passphrase)")
	markWrites(clusterBundleExportCmd)
}

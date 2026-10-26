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

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// clusterRotateCACmd implements `ichor cluster rotate-ca`: it writes a ca-rotation TalosOperation
// (ADR 0063). It changes nothing else; the controller runs the rotation. It writes the custom
// resource directly through the kubeconfig, as `ichor init` does, rather than through ichor-ui:
// a rotation is started rarely, and the operation entry points of ADR 0068 do not carry the kind.
var clusterRotateCACmd = &cobra.Command{
	Use:   "rotate-ca NAME",
	Short: "Replace the cluster's Talos API and Kubernetes API certificate authorities",
	Long: `rotate-ca starts a CA rotation operation: the Talos API CA and the Kubernetes API CA are
replaced in three passes over every machine (accept the new CA, make it the issuer, drop the old
one), each verified before the next. The etcd and aggregator CAs and the service-account key are not
rotated.

Before it starts, every machine must answer, run the configuration Ichor renders for it, and every
Kubernetes Node must be Ready; otherwise the operation pauses without changing anything. While it
runs, the machine controller does not reconcile the cluster's machines and other disruptive
operations are refused. It resumes by itself after the controller restarts, and does not advance past
a step it could not verify.

When it completes, the secrets bundle in the cluster has been replaced, and the copy saved on your
machine is stale: run 'ichor cluster bundle-export NAME' and keep the file safe.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k8s, err := newKubeClient(kubeconfigPath)
		if err != nil {
			return err
		}

		return runRotateCA(cmd.Context(), k8s, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), apiOpts.Namespace, args[0])
	},
}

func runRotateCA(ctx context.Context, k8s client.Client, in io.Reader, out, errw io.Writer, namespace, name string) error {
	var cluster ichorv1alpha1.TalosCluster
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &cluster); err != nil {
		return fmt.Errorf("getting TalosCluster %s/%s: %w", namespace, name, err)
	}

	var ops ichorv1alpha1.TalosOperationList
	if err := k8s.List(ctx, &ops, client.InNamespace(namespace)); err != nil {
		return err
	}

	for i := range ops.Items {
		o := &ops.Items[i]
		if o.Spec.ClusterRef.Name == name && o.Spec.Kind == ichorv1alpha1.TalosOperationKindCARotation &&
			o.Status.Phase != ichorv1alpha1.TalosOperationPhaseCompleted {
			return fmt.Errorf("TalosOperation %s is already rotating this cluster's CAs", o.Name)
		}
	}

	if !assumeYes {
		ok, err := confirm(in, errw, fmt.Sprintf("Rotate the Talos API and Kubernetes API CAs of cluster %s/%s? "+
			"Every machine is reconfigured three times; the secrets bundle is replaced at the end. [y/N] ", namespace, name))
		if err != nil {
			return err
		}

		if !ok {
			return errors.New("aborted")
		}
	}

	op := &ichorv1alpha1.TalosOperation{}
	op.GenerateName, op.Namespace = name+"-ca-rotation-", namespace
	op.Spec.ClusterRef.Name, op.Spec.Kind = name, ichorv1alpha1.TalosOperationKindCARotation

	if err := k8s.Create(ctx, op); err != nil {
		return fmt.Errorf("creating the CA rotation: %w", err)
	}

	_, err := fmt.Fprintf(out, "Started TalosOperation %s. Follow it with 'ichor operation get %s'; when it completes, "+
		"run 'ichor cluster bundle-export %s'.\n", op.Name, op.Name, name)

	return err
}

func init() {
	clusterCmd.AddCommand(clusterRotateCACmd)
	markWrites(clusterRotateCACmd)
}

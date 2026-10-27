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
	"time"

	"github.com/spf13/cobra"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var clusterRotateCAFollow bool

// clusterRotateCACmd implements `ichor cluster rotate-ca`: it starts a ca-rotation TalosOperation through
// OperationService like every other start, so the plan, the
// refusals and the permission check are the ones the UI has. The controller runs the rotation.
//
// Unlike the other kinds the command does not return when the operation exists: the rotation waits,
// after the nodes accept the new CAs, for the operator's credentials, and this command
// writes them while the old ones still work and tells the controller to go on.
var clusterRotateCACmd = &cobra.Command{
	Use:   "rotate-ca NAME",
	Short: "Replace the cluster's Talos API and Kubernetes API certificate authorities",
	Long: `rotate-ca starts a CA rotation operation: the Talos API CA and the Kubernetes API CA are
replaced in passes over every machine (accept the new CA, make it the issuer, drop the old one),
each verified before the next. The kubelet restarts on each machine in the passes that change the
Kubernetes CA. The etcd and aggregator CAs and the service-account key are not rotated.

Your kubeconfig and talosconfig stop working during the rotation: the Kubernetes API serves the new
CA from the second pass on, and the Talos API refuses the old CA after the third. So after every
machine accepts the new CAs, the operation WAITS, changing nothing, until you have new credentials.
This command waits for that point while your current credentials still work, then writes

  a kubeconfig and a talosconfig that trust the old and the new CAs and are signed by the new one
  (next to your current ones, with the suffix ` + credentialsSuffix + `; your current files are not
  touched; mode 0600),

checks that the new kubeconfig authenticates, and only then lets the operation go on. The files'
contents are never printed. If this command is interrupted while the operation waits, run
'ichor cluster credentials NAME --acknowledge' with your current kubeconfig. If you lost both,
'ichor cluster credentials NAME --bundle FILE' makes credentials from a current bundle file.

Before it starts, every machine must answer, run the configuration Ichor renders for it, and every
Kubernetes Node must be Ready; otherwise the operation pauses without changing anything. While it
runs, the machine controller does not reconcile the cluster's machines and other disruptive
operations are refused. It resumes by itself after the controller restarts, and does not advance past
a step it could not verify. Machines held with upgrade-hold are not skipped.

When it completes, the secrets bundle in the cluster has been replaced, and the copy saved on your
machine is stale: run 'ichor cluster bundle-export NAME' and keep the file safe.

It shows which machines are reconfigured and in which order, and asks for confirmation unless --yes.
--follow keeps reporting progress until the rotation completes, through the Kubernetes API with the
new kubeconfig (the ichor-ui tunnel does not survive the rotation).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			name := args[0]

			return runStartOperation(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), startSpec{
				Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, Cluster: name,
				AfterCreate: func(ctx context.Context, ref *pb.ObjectRef, created *pb.CreateOperationResponse) error {
					return afterRotateCACreate(ctx, cmd, name, ref, created)
				},
			})
		})
	},
}

func afterRotateCACreate(ctx context.Context, cmd *cobra.Command, cluster string, ref *pb.ObjectRef, created *pb.CreateOperationResponse) error {
	out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()

	k8s, err := newKubeClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("operation %s was created, but the credential hand-off needs your kubeconfig: %w; run "+
			"'ichor cluster credentials %s --acknowledge' to continue it", ref.GetName(), err, cluster)
	}

	req := credentialRequest{
		Cluster: cluster, Namespace: apiOpts.Namespace, IchorNamespace: apiOpts.IchorNamespace,
		KubeconfigIn: kubeconfigPath, TalosconfigIn: talosconfigPath,
	}

	_, _ = fmt.Fprintf(errw, "created operation %s\n", ref.GetName())

	files, err := runRotateCAHandoff(ctx, handoffDeps{K8s: k8s, Checks: defaultHandoffChecks(), Poll: opWatchInterval}, req, ref.GetName(), errw)
	if err != nil {
		return fmt.Errorf("%w\nThe operation %s keeps waiting; nothing was changed on your credentials. Rerun the hand-off with "+
			"'ichor cluster credentials %s --acknowledge'", err, ref.GetName(), cluster)
	}

	if !clusterRotateCAFollow {
		if err := writeMessage(out, outputFormat, created); err != nil {
			return err
		}

		_, _ = fmt.Fprintf(errw, "Follow it with: ichor operation get %s --watch --kubeconfig %s\n", ref.GetName(), files.Kubeconfig)
		rotateCAAfterwards(errw, cluster)

		return nil
	}

	// The tunnel to ichor-ui goes through the kube-apiserver, which restarts when the Kubernetes CA
	// changes, and the old kubeconfig stops working: follow through the Kubernetes API itself, with
	// the new kubeconfig.
	cfg, err := loadKubeconfig(files.Kubeconfig)
	if err != nil {
		return err
	}

	newClient, err := newKubeClientForConfig(cfg)
	if err != nil {
		return err
	}

	if err := watchCARotation(ctx, newClient, ref.GetNamespace(), ref.GetName(), errw); err != nil {
		return err
	}

	rotateCAAfterwards(errw, cluster)

	return nil
}

// rotateCAAfterwards is what to do once the rotation is over.
func rotateCAAfterwards(errw io.Writer, cluster string) {
	_, _ = fmt.Fprintf(errw, "When it completes: run 'ichor cluster credentials %s' for files that trust the new CA only, and "+
		"'ichor cluster bundle-export %s' to save the new secrets bundle.\n", cluster, cluster)
}

// runRotateCAHandoff waits, through the Kubernetes API, until the operation waits for credentials,
// then writes them, checks them and acknowledges. Nothing here uses the ichor-ui tunnel.
func runRotateCAHandoff(ctx context.Context, d handoffDeps, req credentialRequest, opName string, errw io.Writer) (*credentialFiles, error) {
	_, _ = fmt.Fprintln(errw, "Waiting for every machine to accept the new CAs; your credentials still work meanwhile, "+
		"and are replaced only after the new ones are written.")

	if err := waitForCredentialsStage(ctx, d, req.Namespace, opName, errw); err != nil {
		return nil, err
	}

	files, err := writeCredentials(ctx, d.K8s, req)
	if err != nil {
		return nil, fmt.Errorf("writing the new credentials: %w", err)
	}

	reportCredentials(errw, files)

	if err := acknowledgeWithChecks(ctx, d.K8s, d.Checks, req, files, opName, errw); err != nil {
		return nil, err
	}

	return files, nil
}

// waitForCredentialsStage polls the operation until it is in AwaitingCredentials.
func waitForCredentialsStage(ctx context.Context, d handoffDeps, namespace, name string, errw io.Writer) error {
	last := ""

	for {
		var op ichorv1alpha1.TalosOperation

		err := retryTransient(ctx, &apiClients{}, errw, func() error {
			return d.K8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &op)
		})
		if err != nil {
			return fmt.Errorf("reading operation %s/%s: %w", namespace, name, err)
		}

		if reason := op.Status.FailureReason; reason != "" {
			return fmt.Errorf("operation %s paused after a failure before the hand-off: %s", name, reason)
		}

		stage := ""
		if op.Status.CARotation != nil {
			stage = op.Status.CARotation.Stage
		}

		switch stage {
		case ichorv1alpha1.CARotationStageAwaitingCredentials:
			return nil
		case ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDropping, ichorv1alpha1.CARotationStageFinalizing,
			ichorv1alpha1.CARotationStageDone:
			return fmt.Errorf("operation %s is already in stage %s, past the hand-off", name, stage)
		}

		if line := caProgress(&op); line != last {
			_, _ = fmt.Fprintln(errw, line)
			last = line
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.Poll):
		}
	}
}

// caProgress is one line about a rotation, from its status alone.
func caProgress(op *ichorv1alpha1.TalosOperation) string {
	stage := "-"
	if op.Status.CARotation != nil && op.Status.CARotation.Stage != "" {
		stage = op.Status.CARotation.Stage
	}

	line := fmt.Sprintf("ca-rotation %s: phase %s, stage %s", op.Name, orDash(op.Status.Phase), stage)

	if c := apimeta.FindStatusCondition(op.Status.Conditions, "CARotation"); c != nil && c.Status != "True" && c.Message != "" {
		line += ": " + c.Message
	}

	return line
}

// watchCARotation follows the rotation through the Kubernetes API until it completes. Transient
// errors (the API server restarting for the new CA) are retried; a failed step is an error exit.
func watchCARotation(ctx context.Context, k8s ctrlclient.Client, namespace, name string, errw io.Writer) error {
	last := ""

	for {
		var op ichorv1alpha1.TalosOperation

		err := retryTransient(ctx, &apiClients{}, errw, func() error {
			return k8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &op)
		})
		if err != nil {
			return fmt.Errorf("following operation %s/%s: %w", namespace, name, err)
		}

		if line := caProgress(&op); line != last {
			_, _ = fmt.Fprintln(errw, line)
			last = line
		}

		switch {
		case op.Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted:
			return nil
		case op.Status.Phase == ichorv1alpha1.TalosOperationPhasePaused && op.Status.FailureReason != "":
			return fmt.Errorf("operation %s paused after a failure: %s", name, op.Status.FailureReason)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(opWatchInterval):
		}
	}
}

func init() {
	clusterCmd.AddCommand(clusterRotateCACmd)
	clusterRotateCACmd.Flags().BoolVar(&clusterRotateCAFollow, "follow", false,
		"after the credential hand-off, follow the rotation until it completes or fails")
	markWrites(clusterRotateCACmd)
}

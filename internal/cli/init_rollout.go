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
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

var errOperationStopped = errors.New("stopped")

// rollOutAuthentication ends a sign-in init: the kube-apiserver needs the authentication config that
// only exists once ichor-ui does (the issuer's CA and ClusterIP are made in the cluster), so the
// control plane boots without it and would stay APIServerRestartRequired. init creates the config
// operation that `ichor rollout config` would, and waits for it, so a new cluster ends Ready. It
// reports whether the rollout completed; a failure is a warning, the operation stays for the operator.
func rollOutAuthentication(
	ctx context.Context, k client.Client, cluster *ichorv1alpha1.TalosCluster, deps initDeps,
) (bool, error) {
	p := deps.progress()
	timeout := cmp.Or(deps.HandoverWait, defaultHandoverWait)

	if cluster.Spec.ControlPlane.Count == 1 {
		p.Warn("the Kubernetes API is down for a few minutes while the only control plane restarts its API server")
	}

	s := p.Start("API server authentication")
	s.Update("waiting for the controller to render the authentication config")

	err := pollWhile(ctx, timeout, deps.HandoverInterval, s.Rewrite(initDetail), "the machine to report the pending API server restart", func(ctx context.Context) error {
		var machines ichorv1alpha1.TalosMachineList
		if err := k.List(ctx, &machines, client.InNamespace(cluster.Namespace)); err != nil {
			return err
		}

		for i := range machines.Items {
			if m := &machines.Items[i]; m.Spec.ClusterRef.Name == cluster.Name && upgrade.ConfigPending(m) {
				return nil
			}
		}

		return errors.New("no machine has a pending change yet")
	}, func(error) bool { return true })

	if err == nil {
		s.Update("rolling out the config (kube-apiserver restarts)")

		err = runConfigOperation(ctx, k, cluster, timeout, deps, s.Rewrite(initDetail))
	}

	if ctx.Err() != nil {
		return false, s.Fail(ctx.Err())
	}

	if err != nil {
		s.Warn(fmt.Sprintf("%v; the cluster is fine, run `ichor rollout config --cluster %s` to finish it", err, cluster.Name))

		return false, nil
	}

	s.Done("kube-apiserver runs the authentication config")

	return true, nil
}

// runConfigOperation creates the config TalosOperation, as the UI backend does, and waits for it to complete.
func runConfigOperation(
	ctx context.Context, k client.Client, cluster *ichorv1alpha1.TalosCluster, timeout time.Duration, deps initDeps, progress io.Writer,
) error {
	op := &ichorv1alpha1.TalosOperation{
		GenerateName: ichorv1alpha1.TalosOperationKindConfig + "-", Namespace: cluster.Namespace,
		Annotations: map[string]string{ichorv1alpha1.OperationStartedByAnnotation: "ichor init"},
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Kind: ichorv1alpha1.TalosOperationKindConfig,
		},
	}

	if err := retryInit(ctx, progress, func() error { return k.Create(ctx, op) }); err != nil {
		return fmt.Errorf("creating the config operation: %w", err)
	}

	return pollWhile(ctx, timeout, deps.HandoverInterval, progress, "operation "+op.Name+" to complete", func(ctx context.Context) error {
		if err := k.Get(ctx, client.ObjectKeyFromObject(op), op); err != nil {
			return err
		}

		if op.Status.FailureReason != "" {
			return fmt.Errorf("operation %s: %w: %s", op.Name, errOperationStopped, op.Status.FailureReason)
		}

		if op.Status.Phase != ichorv1alpha1.TalosOperationPhaseCompleted {
			return fmt.Errorf("operation %s is %s", op.Name, cmp.Or(op.Status.Phase, "pending"))
		}

		return nil
	}, func(err error) bool { return !errors.Is(err, errOperationStopped) })
}

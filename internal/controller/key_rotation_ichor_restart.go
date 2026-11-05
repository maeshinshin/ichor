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

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Ichor's own pods are clients of the cluster they run in. A pod's projected ServiceAccount token is signed
// when it is issued and the kubelet re-requests it only at 80% of its hour, so a pod that started before the new
// key signed still holds a token of the old key, and dropping the old key makes the API server refuse it. On
// hardware the controller running the rotation crashlooped on exactly that.
// Once the new key signs, the controller and ichor-ui Deployments are therefore rolled once, so their pods hold
// tokens of the new key, and the old key is not dropped before that rollout is finished. The restart is a write
// to Ichor's own Deployments (the RBAC rule on deployments in the controller's namespace of ca_rotation_clients.go).

// keyRotationRestartMarker is set on a restarted Deployment's pod template to the operation that restarted it: a
// controller that is replaced by its own rollout finds it and does not restart the Deployment again.
const keyRotationRestartMarker = "ichor.maesh.dev/key-rotation"

// restartIchorWorkloads rolls the controller and ichor-ui Deployments once for this operation. The controller
// restarts itself with it: its state is in the operation's status and the bundle Secret, so the new pod resumes
// where this one stopped. A Deployment that is not installed is skipped.
func (c *keyRun) restartIchorWorkloads(ctx context.Context) error {
	ns := c.r.ControllerNamespace
	if ns == "" {
		return nil
	}

	reader := readerOrClient(c.r.APIReader, c.r.Client)
	token := string(c.op.UID)

	body, err := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{
		"annotations": map[string]string{
			restartedAtAnnotation:    time.Now().UTC().Format(time.RFC3339),
			keyRotationRestartMarker: token,
		},
	}}}})
	if err != nil {
		return err
	}

	for _, w := range ichorDeployments {
		var list appsv1.DeploymentList
		if err := reader.List(ctx, &list, client.InNamespace(ns), w.labels); err != nil {
			return fmt.Errorf("finding the %s Deployment: %w", w.role, err)
		}

		for i := range list.Items {
			d := &list.Items[i]
			if d.Spec.Template.Annotations[keyRotationRestartMarker] == token {
				continue
			}

			if err := c.r.Patch(ctx, d, client.RawPatch(types.StrategicMergePatchType, body)); err != nil {
				return fmt.Errorf("restarting the %s Deployment %s: %w", w.role, d.Name, err)
			}
		}
	}

	return nil
}

// ichorRolloutPending is what the controller and ichor-ui Deployments still wait for before the old key may be
// dropped, or "".
func (c *keyRun) ichorRolloutPending(ctx context.Context) (string, error) {
	if c.r.ControllerNamespace == "" {
		return "", nil
	}

	return ichorRolloutWaiting(ctx, readerOrClient(c.r.APIReader, c.r.Client), c.r.ControllerNamespace)
}

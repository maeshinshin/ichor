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
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// NewNodesReady returns the NodesReadyFunc cmd/main.go wires in: it lists the Nodes through an
// admin kubeconfig the Talos API issues (the way kubeconfigAdmin reaches the cluster), so the
// controller's own ServiceAccount needs no right on Nodes for this. The kubeconfig is fetched
// fresh on every call, so it carries whichever Kubernetes CA the node issues from at that moment.
func NewNodesReady() NodesReadyFunc {
	return func(ctx context.Context, talos TalosClient) ([]string, error) {
		cfg, err := restConfig(ctx, talos)
		if err != nil {
			return nil, err
		}

		cs, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("building a clientset from the admin kubeconfig: %w", err)
		}

		nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("listing Nodes: %w", err)
		}

		if len(nodes.Items) == 0 {
			return nil, errors.New("the Kubernetes API lists no Node")
		}

		var notReady []string

		for i := range nodes.Items {
			ready := false

			for _, cond := range nodes.Items[i].Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
					ready = true
				}
			}

			if !ready {
				notReady = append(notReady, nodes.Items[i].Name)
			}
		}

		return notReady, nil
	}
}

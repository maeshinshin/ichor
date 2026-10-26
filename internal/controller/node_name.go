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
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// resolveNodeName returns the Kubernetes node name (and etcd member name) of m. spec.hostname
// is an optional override and wins when set. `ichor init` leaves it empty on the first control
// plane, so otherwise the node's own report decides (its Nodename resource, through talos),
// and when the node cannot be asked (talos is nil, or it is mid-reboot) the Kubernetes Node
// whose address is m's Talos endpoint.
func resolveNodeName(ctx context.Context, c client.Reader, talos TalosClient, m *ichorv1alpha1.TalosMachine) (string, error) {
	if m.Spec.Hostname != "" {
		return m.Spec.Hostname, nil
	}

	if talos != nil {
		if name, err := talos.NodeName(ctx); err == nil && name != "" {
			return name, nil
		}
	}

	host := m.Spec.Endpoint
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes); err != nil {
		return "", fmt.Errorf("listing Kubernetes nodes to find %q's: %w", m.Name, err)
	}

	for i := range nodes.Items {
		for _, addr := range nodes.Items[i].Status.Addresses {
			if addr.Address == host {
				return nodes.Items[i].Name, nil
			}
		}
	}

	return "", fmt.Errorf("machine %q has no spec.hostname and neither its node's report nor a Kubernetes node at %q names its node", m.Name, host)
}

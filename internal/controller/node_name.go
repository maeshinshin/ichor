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

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// nodeNames resolves the Kubernetes node name of every machine in ms, by machine name, with
// resolveNodeName: spec.hostname, else what the node reports (dialed here), else the Node by address.
// It is what "is this the node the controller runs on" must be asked of (r.NodeName is a Node name, and
// spec.hostname is empty on the first control plane). A machine whose name cannot be told is left out
// and the first such error is returned alongside the rest. bundle may be nil: it is then loaded.
func (r *TalosOperationReconciler) nodeNames(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, bundle *secrets.Bundle, ms []*ichorv1alpha1.TalosMachine,
) (map[string]string, error) {
	names := make(map[string]string, len(ms))

	var first error

	reader := readerOrClient(r.APIReader, r.Client)

	for _, m := range ms {
		if m.Spec.Hostname != "" {
			names[m.Name] = m.Spec.Hostname

			continue
		}

		if bundle == nil && r.TalosClient != nil {
			bundle, _ = loadBundle(ctx, r.Client, r.ControllerNamespace, cluster)
		}

		name, err := r.nodeNameOf(ctx, reader, cluster, bundle, m)
		if err != nil {
			if first == nil {
				first = err
			}

			continue
		}

		names[m.Name] = name
	}

	return names, first
}

// nodeNameOf is resolveNodeName with a dialed client when one can be had.
func (r *TalosOperationReconciler) nodeNameOf(
	ctx context.Context, reader client.Reader, cluster *ichorv1alpha1.TalosCluster, bundle *secrets.Bundle, m *ichorv1alpha1.TalosMachine,
) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	if r.TalosClient != nil && bundle != nil {
		if c, err := r.TalosClient(ctx, cluster, m, bundle); err == nil {
			defer closeTalosClient(c)

			return resolveNodeName(ctx, reader, c, m)
		}
	}

	return resolveNodeName(ctx, reader, nil, m)
}

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

	host := m.DialEndpoint()
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

	return "", fmt.Errorf("node %q has no spec.hostname and neither its node's report nor a Kubernetes node at %q names its node", m.Name, host)
}

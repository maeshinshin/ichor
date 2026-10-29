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
	"net"
	"strconv"

	"github.com/siderolabs/talos/pkg/machinery/constants"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// followNodeAddress is called when a node that has joined the cluster stops answering at the
// address Ichor dials. A joined node has a Kubernetes Node object, and the kubelet keeps its
// InternalIP current, so that address says where the node is now. It is recorded in
// status.observedEndpoint (spec.endpoint is the operator's and is never edited; ADR 0086) and
// dialed from the next round on. moved reports whether a new address was recorded.
//
// Only a machine with a known name is followed: spec.hostname is that name, and resolving it
// any other way means asking the node, which is what just failed.
func (r *TalosMachineReconciler) followNodeAddress(
	ctx context.Context, m *ichorv1alpha1.TalosMachine,
) (moved bool, err error) {
	if m.Spec.Hostname == "" || m.Status.AppliedConfigHash == "" {
		return false, nil
	}

	var node corev1.Node
	if err := r.Get(ctx, types.NamespacedName{Name: m.Spec.Hostname}, &node); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	ip := internalIP(&node)
	if ip == "" {
		return false, nil
	}

	port := strconv.Itoa(constants.ApidPort)

	if _, p, err := net.SplitHostPort(m.DialEndpoint()); err == nil {
		port = p
	}

	address := net.JoinHostPort(ip, port)
	if address == m.DialEndpoint() {
		return false, nil
	}

	if err := recordObservedEndpoint(ctx, readerOrClient(r.APIReader, r.Client), r.Client, m, address); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

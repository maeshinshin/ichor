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
	"strings"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// checkEndpointIdentity compares the machine UUID the node at the dialed address reports with the
// UUID this TalosMachine is named for. An address that answers as another machine (an old IP now
// held by a different node) must never be read as this one. stop=true means the round is over:
// the Kubernetes Node said where the machine really is (recorded for the next round), or
// Ready is now False EndpointIdentityMismatch. An unreadable or empty UUID is not a mismatch.
func (r *TalosMachineReconciler) checkEndpointIdentity(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, talos TalosClient,
) (res ctrl.Result, stop bool, err error) {
	want := m.Spec.Identity.UUID
	if want == "" {
		return res, false, nil
	}

	idCtx, cancel := context.WithTimeout(ctx, talosCallTimeout)
	defer cancel()

	hw, ierr := talos.Identity(idCtx)
	if ierr != nil || hw.UUID == "" || strings.EqualFold(hw.UUID, want) {
		return res, false, nil
	}

	if moved, ferr := r.followNodeAddress(ctx, m); ferr == nil && moved {
		return ctrl.Result{RequeueAfter: time.Second}, true, nil
	}

	res, err = r.retryLater(ctx, m, reasonEndpointIdentityMismatch,
		fmt.Sprintf("the node at %s reports machine UUID %s, but this TalosMachine is %s; "+
			"spec.endpoint may be held by another node and may need updating", m.DialEndpoint(), hw.UUID, want))

	return res, true, err
}

// confirmMachine proves that the node talos is connected to is the machine m is named for, before a
// call that wipes it, changes its etcd membership, upgrades it or applies configuration to it. An
// address recorded for one machine can answer as another that trusts the same CA (a DHCP lease that
// changed hands), and every credential Ichor holds opens both. Unlike checkEndpointIdentity, which
// only reads and so lets an unreadable UUID pass, this refuses unless the node reports exactly the
// UUID in m's spec: an Identity call that fails or a node that reports none cannot show it is the
// right one, and none of the callers is hurt by refusing and being retried.
func confirmMachine(ctx context.Context, talos TalosClient, m *ichorv1alpha1.TalosMachine) error {
	idCtx, cancel := context.WithTimeout(ctx, talosCallTimeout)
	defer cancel()

	want := m.Spec.Identity.UUID

	hw, err := talos.Identity(idCtx)
	if err != nil {
		return fmt.Errorf("cannot tell that %s is %q (UUID %s), so nothing was sent to it: reading its identity failed: %w",
			m.DialEndpoint(), m.Name, want, err)
	}

	if hw.UUID == "" || !strings.EqualFold(hw.UUID, want) {
		return fmt.Errorf("the node at %s reports machine UUID %q, but %q is %s, so nothing was sent to it; "+
			"the address may be held by another node", m.DialEndpoint(), hw.UUID, m.Name, want)
	}

	return nil
}

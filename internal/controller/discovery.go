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
	"io"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Event reasons Discover records on the TalosCluster when it will not
// register a candidate -- the human-visible report
// docs/design/architecture.md's "Nodes" section calls for when a key is weak
// or collides, since no TalosMachine is created to carry a condition for
// either case.
const (
	reasonWeakIdentity      = "WeakIdentity"
	reasonIdentityCollision = "IdentityCollision"
)

// DiscoveryReconciler turns a maintenance-mode node's hardware identity into
// a pending, unapproved TalosMachine (docs/design/architecture.md "Nodes":
// "The controller reads hardware identity ... and creates a pending
// TalosMachine"). It never sets TalosMachineSpec.Approved, so a discovered
// machine gets no configuration applied until a human approves it --
// TalosMachineReconciler's own first line already refuses to touch an
// unapproved machine, so that guarantee lives in one place, not two.
//
// What supplies the candidate endpoint -- registering one by IP, a future
// subnet scan, or anything else "Nodes" lists as an option -- is not decided
// here. Discover takes the endpoint as a plain argument
// so whichever mechanism is chosen later can drive it unchanged.
type DiscoveryReconciler struct {
	client.Client

	// Recorder reports a weak or colliding identity in a form an operator
	// can see (architecture.md "Nodes": "the address, MACs, serial and
	// manufacturer shown to decide on"), as a Warning Event on the
	// TalosCluster, since neither case creates a TalosMachine to hold a
	// condition. nil is valid -- Discover still returns nil, nil, it just
	// has nowhere to report to.
	Recorder record.EventRecorder

	// MaintenanceClient opens the insecure maintenance-mode connection
	// Discover reads identity over (NewMachineryMaintenanceClient is the
	// real implementation cmd/main.go wires in). nil is reported through the
	// returned error, the same "not configured" shape
	// TalosMachineReconciler.TalosClient uses.
	MaintenanceClient func(ctx context.Context, endpoint string) (MaintenanceClient, error)
}

// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Discover reads endpoint's hardware identity and decides, from that alone,
// whether to register it:
//
//   - A weak identity (any of UUID, serial or at least one permanent MAC
//     missing) is never auto-registered (architecture.md "Nodes"): Discover
//     records a WeakIdentity event on cluster and returns (nil, nil).
//   - A strong identity whose UUID already names an existing TalosMachine
//     with a *different* serial number or MAC set is a collision Ichor
//     cannot resolve by itself -- Talos does not guarantee the UUID is
//     unique, so the same value could be two different machines. Discover
//     records an IdentityCollision event and returns (nil, nil), leaving the
//     existing machine untouched.
//   - A strong identity matching an existing TalosMachine exactly is the
//     same machine returning; Discover returns it unchanged.
//   - Otherwise Discover creates a new, unapproved TalosMachine keyed by the
//     UUID and returns it.
//
// A created machine's Spec.Role is left unset: architecture.md's "Nodes"
// assigns the role at approval, not at discovery.
func (r *DiscoveryReconciler) Discover(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, endpoint string,
) (*ichorv1alpha1.TalosMachine, error) {
	log := logf.FromContext(ctx)

	if r.MaintenanceClient == nil {
		return nil, fmt.Errorf("no maintenance Talos API connectivity is configured for this controller")
	}

	mc, err := r.MaintenanceClient(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("connecting to the node's maintenance Talos API: %w", err)
	}

	if closer, ok := mc.(io.Closer); ok {
		defer closer.Close() //nolint:errcheck
	}

	hw, err := mc.Identity(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading hardware identity: %w", err)
	}

	if !strongIdentity(hw) {
		log.Info("discovered node has a weak identity; not auto-registering", "endpoint", endpoint)
		r.reportWeak(cluster, endpoint, hw, reasonWeakIdentity,
			"identity is missing a UUID, serial number or permanent MAC address; an operator has to register it")

		return nil, nil
	}

	key := types.NamespacedName{Namespace: cluster.Namespace, Name: hw.UUID}

	var existing ichorv1alpha1.TalosMachine

	err = r.Get(ctx, key, &existing)

	switch {
	case apierrors.IsNotFound(err):
		m := &ichorv1alpha1.TalosMachine{
			Name: hw.UUID, Namespace: cluster.Namespace,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
				Identity: ichorv1alpha1.TalosMachineIdentity{
					UUID: hw.UUID, Serial: hw.Serial, MACAddresses: hw.MACAddresses,
				},
				Endpoint: endpoint,
			},
		}

		if err := r.Create(ctx, m); err != nil {
			return nil, fmt.Errorf("creating pending TalosMachine: %w", err)
		}

		return m, nil

	case err != nil:
		return nil, fmt.Errorf("looking up existing TalosMachine %q: %w", key.Name, err)

	case sameIdentity(existing.Spec.Identity, hw):
		return &existing, nil

	default:
		log.Info("discovered node's UUID already belongs to a different TalosMachine identity",
			"endpoint", endpoint, "uuid", hw.UUID)
		r.reportWeak(cluster, endpoint, hw, reasonIdentityCollision,
			fmt.Sprintf("UUID %q already belongs to a TalosMachine with a different serial number or MAC "+
				"addresses; an operator has to decide whether this is the same machine returning or a different "+
				"one reusing the UUID", hw.UUID))

		return nil, nil
	}
}

// reportWeak records a Warning event on cluster carrying the discovered
// node's address and whatever of its identity was read, so an operator has
// what architecture.md's "Nodes" section asks for without Ichor creating a
// TalosMachine to hold it.
func (r *DiscoveryReconciler) reportWeak(
	cluster *ichorv1alpha1.TalosCluster, endpoint string, hw HardwareIdentity, reason, why string,
) {
	if r.Recorder == nil {
		return
	}

	r.Recorder.Eventf(cluster, corev1.EventTypeWarning, reason,
		"%s (endpoint=%s uuid=%q serial=%q macAddresses=%v manufacturer=%q)",
		why, endpoint, hw.UUID, hw.Serial, hw.MACAddresses, hw.Manufacturer)
}

// strongIdentity reports whether hw has every element of
// docs/design/architecture.md's "Nodes" identity tuple -- SMBIOS UUID,
// serial number, and at least one permanent MAC address. Any missing
// element is a weak key, and Ichor does not auto-register on one.
func strongIdentity(hw HardwareIdentity) bool {
	return hw.UUID != "" && hw.Serial != "" && len(hw.MACAddresses) > 0
}

// sameIdentity reports whether existing and hw describe the same physical
// machine: equal serial numbers and the same set of permanent MAC
// addresses. UUID is deliberately not compared here -- the caller already
// looked existing up by hw.UUID -- which is exactly how Discover tells "the
// same machine returning" apart from "a different machine whose UUID
// collided": only the rest of the tuple can decide it.
func sameIdentity(existing ichorv1alpha1.TalosMachineIdentity, hw HardwareIdentity) bool {
	if existing.Serial != hw.Serial {
		return false
	}

	a, b := slices.Clone(existing.MACAddresses), slices.Clone(hw.MACAddresses)
	slices.Sort(a)
	slices.Sort(b)

	return slices.Equal(a, b)
}

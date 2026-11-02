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
	"io"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/constants"
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

	// ReasonAlreadyConfigured is the Event reason for a Talos node that already has a configuration; internal/uiapi
	// lists these.
	ReasonAlreadyConfigured = "AlreadyConfigured"
	// AnnotationAlreadyConfiguredEndpoint carries the node's endpoint on that Event.
	AnnotationAlreadyConfiguredEndpoint = "ichor.maesh.dev/endpoint"
)

// ErrAlreadyConfigured is what MaintenanceClient.Identity wraps when the node's API rejects the
// unauthenticated call because the node already has a configuration.
var ErrAlreadyConfigured = errors.New("the node already has a Talos configuration")

// DiscoveryReconciler turns a maintenance-mode node's hardware identity into
// a pending, unapproved TalosMachine (docs/design/architecture.md "Nodes":
// "The controller reads hardware identity ... and creates a pending
// TalosMachine"). It leaves TalosMachineSpec.ApprovalState at its "pending" default, so a discovered
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
	if errors.Is(err, ErrAlreadyConfigured) {
		// A Talos API that rejects an unauthenticated call has a config already.
		// Nothing is created; the Event is how the UI and operators learn of it.
		log.Info("discovered a Talos node that is already configured; not adding it", "endpoint", endpoint)
		r.reportAlreadyConfigured(cluster, endpoint)

		return nil, nil
	}

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

		r.pinMaintenance(ctx, mc, m)

		return m, nil

	case err != nil:
		return nil, fmt.Errorf("looking up existing TalosMachine %q: %w", key.Name, err)

	case sameIdentity(existing.Spec.Identity, hw):
		// The same machine, possibly somewhere new (a fresh DHCP lease or pod IP). spec.endpoint
		// is the operator's and never edited here; the address it answers at is recorded in
		// status and dialed first.
		if existing.DialEndpoint() != endpoint {
			if err := recordObservedEndpoint(ctx, r.Client, r.Client, &existing, endpoint); err != nil {
				return nil, fmt.Errorf("recording %q's new address: %w", existing.Name, err)
			}
		}

		if !existing.Spec.IsApproved() {
			r.pinMaintenance(ctx, mc, &existing)
		}

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

// pinMaintenance records the certificate fingerprint the node presented, so the first configuration is
// only pushed to that node again. Failing to record it is not fatal: provisioning pins at its
// first probe instead.
func (r *DiscoveryReconciler) pinMaintenance(ctx context.Context, mc MaintenanceClient, m *ichorv1alpha1.TalosMachine) {
	pf, ok := mc.(PeerFingerprinter)
	if !ok {
		return
	}

	if err := recordMaintenanceFingerprint(ctx, r.Client, r.Client, m, pf.PeerFingerprint()); err != nil {
		logf.FromContext(ctx).Error(err, "recording the maintenance certificate fingerprint", "machine", m.Name)
	}
}

// reportAlreadyConfigured records the AlreadyConfigured Event on cluster, with the endpoint in
// the AnnotationEndpoint annotation so the UI API can list the addresses without parsing text.
func (r *DiscoveryReconciler) reportAlreadyConfigured(cluster *ichorv1alpha1.TalosCluster, endpoint string) {
	if r.Recorder == nil {
		return
	}

	r.Recorder.AnnotatedEventf(cluster, map[string]string{AnnotationAlreadyConfiguredEndpoint: endpoint},
		corev1.EventTypeWarning, ReasonAlreadyConfigured,
		"%s already runs a Talos configuration, so Ichor did not add it; reclaim it first (ichor machine reclaim)",
		endpoint)
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
// docs/design/architecture.md's "Nodes" identity tuple, via
// ichorv1alpha1.TalosMachineIdentity.IsStrongIdentity -- the one place
// that rule is written, shared with internal/uiapi's own,
// separate call site on the TalosMachine's already-recorded identity.
// HardwareIdentity, not TalosMachineIdentity, is what Discover has in
// hand at this point (the CRD type is only built once a strong identity
// clears this check, a few lines below), so this converts rather than
// calling the method on a type this package doesn't have yet.
func strongIdentity(hw HardwareIdentity) bool {
	return ichorv1alpha1.TalosMachineIdentity{
		UUID: hw.UUID, Serial: hw.Serial, MACAddresses: hw.MACAddresses,
	}.IsStrongIdentity()
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

// discoveryConcurrency and discoveryDialTimeout bound one DiscoverAll call's
// worst case. The CRD caps spec.discovery at 64 addresses plus four /24
// CIDRs (256 addresses each), so at most 64+4*256=1088 candidates exist to
// probe. The per-candidate deadline covers the whole of Discover -- the
// insecure maintenance-mode dial and identity read *and* the Kubernetes API
// calls that follow for a strong identity -- so it has to be generous
// enough that ordinary API latency never gets mistaken for an unreachable
// node; 2s does that while still keeping the worst case (1088 candidates /
// 32 workers * 2s ≈ 68s) well short of "hangs." Neither number is
// configurable -- nobody asked for that knob, and a fixed worst case is the
// property that matters.
const (
	discoveryConcurrency = 32
	discoveryDialTimeout = 2 * time.Second
)

// EnumerateCandidates expands cluster.Spec.Discovery into the maintenance
// endpoints to probe, each address (direct or from a CIDR) paired with
// Talos's fixed apid port -- the only port this ever dials. A
// nil or empty Discovery enumerates to nothing: Ichor never finds a range
// on its own, only ever the one declared.
func EnumerateCandidates(cluster *ichorv1alpha1.TalosCluster) []string {
	d := cluster.Spec.Discovery
	if d == nil {
		return nil
	}

	candidates := make([]string, 0, len(d.Addresses))

	for _, a := range d.Addresses {
		candidates = append(candidates, net.JoinHostPort(a, strconv.Itoa(constants.ApidPort)))
	}

	for _, c := range d.CIDRs {
		prefix, err := netip.ParsePrefix(c)
		if err != nil {
			continue // the CRD's own CEL validation already rejects this; defensive only.
		}

		for addr := prefix.Masked().Addr(); prefix.Contains(addr); addr = addr.Next() {
			candidates = append(candidates, net.JoinHostPort(addr.String(), strconv.Itoa(constants.ApidPort)))
		}
	}

	return candidates
}

// DiscoverAll enumerates cluster.Spec.Discovery's declared candidates and
// calls Discover on each, bounded by discoveryConcurrency and
// discoveryDialTimeout. An address that never answers -- most of a
// provisioning network, by design -- fails silently and is skipped: logging
// or eventing every miss would bury the one candidate that matters in
// noise, which is why neither this function nor Discover's own connect/read
// error paths log anything for that case. Discover's own identity
// classification and events still run normally for whatever does answer.
func (r *DiscoveryReconciler) DiscoverAll(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster,
) ([]*ichorv1alpha1.TalosMachine, error) {
	candidates := EnumerateCandidates(cluster)
	if len(candidates) == 0 {
		return nil, nil
	}

	known, err := r.knownHosts(ctx, cluster)
	if err != nil {
		return nil, err
	}

	candidates = slices.DeleteFunc(candidates, func(c string) bool { return known[hostOf(c)] })

	sem := make(chan struct{}, discoveryConcurrency)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []*ichorv1alpha1.TalosMachine
	)

	for _, endpoint := range candidates {
		wg.Add(1)
		sem <- struct{}{}

		go func(endpoint string) {
			defer wg.Done()
			defer func() { <-sem }()

			cctx, cancel := context.WithTimeout(ctx, discoveryDialTimeout)
			defer cancel()

			m, err := r.Discover(cctx, cluster, endpoint)
			if err != nil || m == nil {
				return
			}

			mu.Lock()
			results = append(results, m)
			mu.Unlock()
		}(endpoint)
	}

	wg.Wait()

	return results, nil
}

// hostOf is endpoint without its port; an endpoint with no port is returned as is.
func hostOf(endpoint string) string {
	if h, _, err := net.SplitHostPort(endpoint); err == nil {
		return h
	}

	return endpoint
}

// knownHosts is the set of hosts that already belong to cluster: each of its TalosMachines'
// spec.endpoint and status.observedEndpoint, and every Node's InternalIP (a member whose machine
// endpoint is stale). They are not probed: a configured member is not a discovery candidate, and
// probing it would only record AlreadyConfigured for it. spec.discovery is left as the user wrote it.
func (r *DiscoveryReconciler) knownHosts(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster,
) (map[string]bool, error) {
	var machines ichorv1alpha1.TalosMachineList
	if err := r.List(ctx, &machines, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, fmt.Errorf("listing TalosMachines: %w", err)
	}

	known := map[string]bool{}

	for i := range machines.Items {
		m := &machines.Items[i]
		if m.Spec.ClusterRef.Name != cluster.Name {
			continue
		}

		known[hostOf(m.Spec.Endpoint)] = true
		known[hostOf(m.Status.ObservedEndpoint)] = true
	}

	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return nil, fmt.Errorf("listing Nodes: %w", err)
	}

	for i := range nodes.Items {
		known[internalIP(&nodes.Items[i])] = true
	}

	delete(known, "")

	return known, nil
}

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
	"strings"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/constants"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/endpointguard"
)

// Event reasons Discover records on the TalosCluster when it will not register a candidate (architecture.md
// "Nodes"): no TalosMachine is created to carry a condition for a weak or colliding key.
const (
	reasonWeakIdentity      = "WeakIdentity"
	reasonIdentityCollision = "IdentityCollision"

	// reasonMaintenanceRefreshed is the Event reason on a pending TalosMachine whose pinned maintenance
	// certificate was replaced because the same node came back with a new one.
	reasonMaintenanceRefreshed = "MaintenanceIdentityRefreshed"

	// ReasonAlreadyConfigured is the Event reason for a Talos node that already has a configuration; internal/uiapi
	// lists these.
	ReasonAlreadyConfigured = "AlreadyConfigured"
	// ReasonOrphan is the Event reason for a configured Talos node at the address of a Kubernetes Node that
	// no TalosMachine of the namespace accounts for: probably an orphan of this cluster, a machine whose
	// TalosMachine was deleted and that was never wiped. Discovery cannot prove the
	// node is this cluster's (the call is unauthenticated); reclaim with the cluster's own credentials
	// does, because only this cluster's certificate authority can dial it. Nothing is wiped by the Event.
	ReasonOrphan = "Orphan"
	// AnnotationAlreadyConfiguredEndpoint carries the node's endpoint on that Event (and on Orphan).
	AnnotationAlreadyConfiguredEndpoint = "ichor.maesh.dev/endpoint"
)

// ErrAlreadyConfigured is what MaintenanceClient.Identity wraps when the node's API rejects the
// unauthenticated call because the node already has a configuration.
var ErrAlreadyConfigured = errors.New("the node already has a Talos configuration")

// DiscoveryReconciler turns a maintenance-mode node's hardware identity into a pending, unapproved
// TalosMachine (architecture.md "Nodes"). It leaves ApprovalState at "pending", so the machine gets no
// configuration until a human approves it (TalosMachineReconciler refuses to touch an unapproved one).
//
// What supplies the candidate endpoint (an IP, a subnet scan, ...) is not decided here:
// Discover takes it as a plain argument.
type DiscoveryReconciler struct {
	client.Client

	// Recorder reports a weak or colliding identity as a Warning Event on the TalosCluster (the address,
	// MACs, serial and manufacturer to decide on; architecture.md "Nodes"). nil is valid: Discover still
	// returns nil, nil.
	Recorder record.EventRecorder

	// MaintenanceClient opens the insecure maintenance-mode connection Discover reads identity over
	// (NewMachineryMaintenanceClient). nil is reported through the returned error.
	MaintenanceClient func(ctx context.Context, endpoint string) (MaintenanceClient, error)
}

// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Discover reads endpoint's hardware identity and decides, from that alone, whether to register it:
//
//   - A weak identity (a missing UUID, serial or permanent MAC) is never auto-registered: Discover records a
//     WeakIdentity event on cluster and returns (nil, nil).
//   - A strong identity whose UUID names an existing TalosMachine with a different serial or MAC set is a
//     collision Ichor cannot resolve (Talos does not guarantee UUIDs are unique): an IdentityCollision
//     event, (nil, nil), the existing machine untouched.
//   - A strong identity matching an existing TalosMachine exactly is the same machine returning; it is
//     returned unchanged.
//   - Otherwise a new, unapproved TalosMachine keyed by the UUID is created and returned.
//
// A created machine's Spec.Role is left unset: the role is assigned at approval.
func (r *DiscoveryReconciler) Discover(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, endpoint string,
) (*ichorv1alpha1.TalosMachine, error) {
	return r.discover(ctx, cluster, endpoint, false)
}

// discover is Discover; orphanCandidate marks an address of a Kubernetes Node that no TalosMachine
// accounts for, where a configured node is reported as Orphan instead of AlreadyConfigured.
func (r *DiscoveryReconciler) discover(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, endpoint string, orphanCandidate bool,
) (*ichorv1alpha1.TalosMachine, error) {
	log := logf.FromContext(ctx)

	if r.MaintenanceClient == nil {
		return nil, errors.New("no maintenance Talos API connectivity is configured for this controller")
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
		log.Info("discovered a Talos node that is already configured; not adding it", "endpoint", endpoint,
			"orphanCandidate", orphanCandidate)
		r.reportAlreadyConfigured(cluster, endpoint, orphanCandidate)

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

		// Written to the object Create returned: the cache may not have the new machine yet.
		if inv, ok := r.readInventory(ctx, mc, m); ok {
			m.Status.DiscoveredDisks, m.Status.DiscoveredLinks = inv.Disks, inv.Links
			if err := r.Status().Update(ctx, m); err != nil {
				logf.FromContext(ctx).Error(err, "recording the disks and NICs of a new TalosMachine", "machine", m.Name)
			}
		}

		return m, nil

	case err != nil:
		return nil, fmt.Errorf("looking up existing TalosMachine %q: %w", key.Name, err)

	case sameIdentity(existing.Spec.Identity, hw):
		// The same machine, possibly somewhere new (a fresh DHCP lease or pod IP). spec.endpoint
		// is the operator's and never edited here; the address it answers at is recorded in
		// status and dialed first, unless the machine is one whose address a
		// self-reported identity must not move (followsDiscoveredAddress).
		if existing.DialEndpoint() != endpoint && followsDiscoveredAddress(&existing) {
			if err := recordObservedEndpoint(ctx, r.Client, r.Client, &existing, endpoint); err != nil {
				return nil, fmt.Errorf("recording %q's new address: %w", existing.Name, err)
			}
		}

		if !existing.Spec.IsApproved() {
			r.refreshUnapproved(ctx, mc, &existing)
		}

		return &existing, nil

	default:
		log.Info("discovered node's UUID already belongs to a different TalosMachine identity",
			"endpoint", endpoint, "uuid", hw.UUID)
		r.reportWeak(cluster, endpoint, hw, reasonIdentityCollision,
			fmt.Sprintf("UUID %q already belongs to a TalosMachine with a different serial number or MAC "+
				"addresses; an operator has to decide whether this is the same node returning or a different "+
				"one reusing the UUID", hw.UUID))

		return nil, nil
	}
}

// followsDiscoveredAddress reports whether a node that answers the insecure maintenance API with m's
// identity at another address is where m is dialled from now on.
// That identity is self-reported, so for an approved machine it must not choose where the cluster's
// configuration (its CA keys) goes: a converged machine's address changes only through its
// authenticated reads, the Kubernetes Node (followNodeAddress) or spec.endpoint. The exception is
// an approved machine that has not been sent its configuration yet and has a pinned maintenance
// certificate: provisioning refuses a node with another certificate, so following a
// redirect cannot hand the configuration to a twin, and a machine that moved before its first
// provisioning is not left dialling a stale address.
func followsDiscoveredAddress(m *ichorv1alpha1.TalosMachine) bool {
	if !m.Spec.IsApproved() {
		return true
	}

	return m.Status.ConfigStoredAt == nil && m.Status.AppliedConfigHash == "" && m.Status.MaintenanceFingerprint != ""
}

// refreshUnapproved re-records what a maintenance-mode probe of m's node showed: the certificate
// fingerprint and the disks and NICs. m must be unapproved, and the node must
// have answered with m's identity. A changed fingerprint is reported on m, because it means the node
// was reset or rebooted since the last probe.
func (r *DiscoveryReconciler) refreshUnapproved(ctx context.Context, mc MaintenanceClient, m *ichorv1alpha1.TalosMachine) {
	old := m.Status.MaintenanceFingerprint

	r.pinMaintenance(ctx, mc, m)

	if inv, ok := r.readInventory(ctx, mc, m); ok {
		if err := recordDiscoveredHardware(ctx, r.Client, r.Client, m, inv); err != nil {
			logf.FromContext(ctx).Error(err, "recording the disks and NICs of a pending TalosMachine", "machine", m.Name)
		}
	}

	if r.Recorder != nil && old != "" && m.Status.MaintenanceFingerprint != old {
		r.Recorder.Eventf(m, corev1.EventTypeNormal, reasonMaintenanceRefreshed,
			"the node answers in maintenance mode with the same identity and a new certificate (%s, was %s), so the "+
				"pinned fingerprint and the disk and NIC lists were refreshed; the node was not approved, so no "+
				"configuration was ever sent against the old one", m.Status.MaintenanceFingerprint, old)
	}
}

// refreshPendingAt probes endpoint, the address of a pending machine of cluster, and refreshes that machine
// (refreshUnapproved) when the node answers in maintenance mode with its recorded identity. Every other
// outcome -- no answer, a configured node, a weak identity, a UUID that is not a pending machine of the
// cluster, or the same UUID with another serial or MACs -- changes nothing and reports nothing, as when
// these addresses were not probed at all; creating a second machine at a known address is Discover's
// decision for unknown addresses, not this path's.
func (r *DiscoveryReconciler) refreshPendingAt(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, endpoint string) {
	if r.MaintenanceClient == nil {
		return
	}

	mc, err := r.MaintenanceClient(ctx, endpoint)
	if err != nil {
		return
	}

	if closer, ok := mc.(io.Closer); ok {
		defer closer.Close() //nolint:errcheck
	}

	hw, err := mc.Identity(ctx)
	if err != nil || !strongIdentity(hw) {
		return
	}

	var m ichorv1alpha1.TalosMachine
	if err := r.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: hw.UUID}, &m); err != nil {
		return
	}

	if m.Spec.ClusterRef.Name != cluster.Name || !isPendingMachine(&m) || !sameIdentity(m.Spec.Identity, hw) {
		return
	}

	r.refreshUnapproved(ctx, mc, &m)
}

// isPendingMachine reports whether m awaits an operator's decision: neither approved nor rejected.
func isPendingMachine(m *ichorv1alpha1.TalosMachine) bool {
	return !m.Spec.IsApproved() && m.Spec.ApprovalState != ichorv1alpha1.MachineApprovalStateRejected
}

// readInventory reads the node's disks and NICs for m's status. A failed read is not a
// failed discovery: the approval form reads the node itself while the lists are missing, and the next
// probe records them.
func (r *DiscoveryReconciler) readInventory(
	ctx context.Context, mc MaintenanceClient, m *ichorv1alpha1.TalosMachine,
) (Inventory, bool) {
	inv, err := mc.Inventory(ctx)
	if err != nil {
		logf.FromContext(ctx).Info("reading the node's disks and NICs; not recorded on the TalosMachine",
			"machine", m.Name, "reason", err.Error())

		return Inventory{}, false
	}

	return inv, true
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
func (r *DiscoveryReconciler) reportAlreadyConfigured(cluster *ichorv1alpha1.TalosCluster, endpoint string, orphan bool) {
	if r.Recorder == nil {
		return
	}

	if orphan {
		r.Recorder.AnnotatedEventf(cluster, map[string]string{AnnotationAlreadyConfiguredEndpoint: endpoint},
			corev1.EventTypeWarning, ReasonOrphan,
			"%s runs a Talos configuration and has a Kubernetes Node, but no TalosMachine accounts for it: it may be "+
				"a node of this cluster that was removed without a wipe; Ichor did not touch it. To start over, "+
				"wipe it with the cluster's own credentials (ichor reclaim node ADDRESS --cluster NAME, or Reset and add)",
			endpoint)

		return
	}

	r.Recorder.AnnotatedEventf(cluster, map[string]string{AnnotationAlreadyConfiguredEndpoint: endpoint},
		corev1.EventTypeWarning, ReasonAlreadyConfigured,
		"%s already runs a Talos configuration, so Ichor did not add it; reclaim it first (ichor reclaim node)",
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

// strongIdentity reports whether hw has every element of architecture.md's "Nodes" identity tuple,
// through TalosMachineIdentity.IsStrongIdentity, the one place that rule is written.
func strongIdentity(hw HardwareIdentity) bool {
	return ichorv1alpha1.TalosMachineIdentity{
		UUID: hw.UUID, Serial: hw.Serial, MACAddresses: hw.MACAddresses,
	}.IsStrongIdentity()
}

// sameIdentity reports whether existing and hw describe the same physical machine: equal serial
// numbers and the same set of permanent MAC addresses. UUID is not compared; the caller looked existing
// up by it, so only the rest of the tuple tells "the same machine returning" from a UUID collision.
func sameIdentity(existing ichorv1alpha1.TalosMachineIdentity, hw HardwareIdentity) bool {
	if existing.Serial != hw.Serial {
		return false
	}

	a, b := slices.Clone(existing.MACAddresses), slices.Clone(hw.MACAddresses)
	slices.Sort(a)
	slices.Sort(b)

	return slices.Equal(a, b)
}

// discoveryConcurrency and discoveryDialTimeout bound one DiscoverAll call's worst case. The CRD caps
// spec.discovery at 64 addresses plus four /24 CIDRs, so at most 64+4*256=1088 candidates exist. The
// per-candidate deadline covers the whole of Discover (the maintenance-mode dial and identity read and
// the Kubernetes API calls after it), so it must be generous enough that API latency is never mistaken
// for an unreachable node; 2s keeps the worst case (1088 / 32 workers * 2s, about 68s) short of a hang.
// Neither is configurable.
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

	known, pending, orphans, err := r.knownHosts(ctx, cluster)
	if err != nil {
		return nil, err
	}

	candidates = slices.DeleteFunc(candidates, func(c string) bool { return known[endpointguard.HostOf(c)] })

	// One slot per candidate, so the goroutines never write the same element and need no lock.
	found := make([]*ichorv1alpha1.TalosMachine, len(candidates))

	var g errgroup.Group
	g.SetLimit(discoveryConcurrency)

	for i, endpoint := range candidates {
		g.Go(func() error {
			cctx, cancel := context.WithTimeout(ctx, discoveryDialTimeout)
			defer cancel()

			// The address of a pending machine is probed only to refresh that machine, never to
			// register another one.
			if pending[endpointguard.HostOf(endpoint)] {
				r.refreshPendingAt(cctx, cluster, endpoint)

				return nil
			}

			// A failed probe is a miss, not an error for the pass (see above).
			if m, err := r.discover(cctx, cluster, endpoint, orphans[endpointguard.HostOf(endpoint)]); err == nil {
				found[i] = m
			}

			return nil
		})
	}

	_ = g.Wait() // every task returns nil; the group only bounds concurrency

	return slices.DeleteFunc(found, func(m *ichorv1alpha1.TalosMachine) bool { return m == nil }), nil
}

// knownHosts is the set of hosts that already belong to cluster: each of its TalosMachines'
// spec.endpoint and status.observedEndpoint, and every Node's InternalIP (a member whose machine
// endpoint is stale). They are not probed: a configured member is not a discovery candidate, and
// probing it would only record AlreadyConfigured for it. spec.discovery is left as the user wrote it.
//
// The hosts of pending machines (neither approved nor rejected) are returned apart, as pending: they
// were never configured, and the node may have been reset since, so DiscoverAll probes them to refresh
// the machine. A host that an approved or rejected machine or a Node also has is only known.
func (r *DiscoveryReconciler) knownHosts(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster,
) (known, pending, orphans map[string]bool, err error) {
	var machines ichorv1alpha1.TalosMachineList
	if err := r.List(ctx, &machines, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, nil, nil, fmt.Errorf("listing TalosMachines: %w", err)
	}

	known, pending, orphans = map[string]bool{}, map[string]bool{}, map[string]bool{}
	uuids := map[string]bool{}

	for i := range machines.Items {
		m := &machines.Items[i]
		uuids[strings.ToLower(m.Name)], uuids[strings.ToLower(m.Spec.Identity.UUID)] = true, true

		if m.Spec.ClusterRef.Name != cluster.Name {
			continue
		}

		set := known
		if isPendingMachine(m) {
			set = pending
		}

		set[endpointguard.HostOf(m.Spec.Endpoint)] = true
		set[endpointguard.HostOf(m.Status.ObservedEndpoint)] = true
	}

	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return nil, nil, nil, fmt.Errorf("listing Nodes: %w", err)
	}

	for i := range nodes.Items {
		// A Node left behind by an Ichor reset that is not Ready again is not a member: the wiped
		// machine behind its address has to be found.
		if _, reset := nodes.Items[i].Annotations[nodeResetByIchorAnnotation]; reset && !nodeIsReady(&nodes.Items[i]) {
			continue
		}

		ip := internalIP(&nodes.Items[i])
		known[ip] = true

		// A Node that no TalosMachine of the namespace accounts for (by UUID) is not a member Ichor
		// manages: its address is probed, and a configured node there is reported as an orphan.
		if u := strings.ToLower(nodes.Items[i].Status.NodeInfo.SystemUUID); u != "" && !uuids[u] {
			orphans[ip] = true
		}
	}

	delete(known, "")
	delete(pending, "")
	delete(orphans, "")

	// A host a machine's own endpoint names is a member, whatever its Node says.
	for i := range machines.Items {
		m := &machines.Items[i]
		delete(orphans, endpointguard.HostOf(m.Spec.Endpoint))
		delete(orphans, endpointguard.HostOf(m.Status.ObservedEndpoint))
	}

	for h := range known {
		delete(pending, h)
	}

	// Orphan candidates are probed, so they are not skipped.
	for h := range orphans {
		delete(known, h)
	}

	return known, pending, orphans, nil
}

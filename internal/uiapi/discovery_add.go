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

package uiapi

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/endpointguard"
)

// AddDiscoveryAddress appends one IPv4 address to spec.discovery.addresses and writes nothing else
// (ADR 0011: Ichor only dials what the cluster declares).
func (s *Server) AddDiscoveryAddress(
	ctx context.Context, req *connect.Request[pb.AddDiscoveryAddressRequest],
) (*connect.Response[pb.AddDiscoveryAddressResponse], error) {
	addr, err := ipv4Address(req.Msg.GetAddress())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	ref := req.Msg.GetCluster()

	out, audit, err := s.editDiscovery(ctx, ref, "add-discovery-address",
		"adding a discovery address to", func(d *ichorv1alpha1.TalosClusterDiscovery) error {
			if slices.Contains(d.Addresses, addr) {
				return fmt.Errorf("%s is already in the discovery list", addr)
			}

			d.Addresses = append(d.Addresses, addr)

			return nil
		})
	if err != nil {
		return nil, err
	}

	c := ichorv1alpha1.TalosCluster{Namespace: ref.GetNamespace(), Name: ref.GetName()}
	s.fillDiscoveryFindings(ctx, out, &c)

	return connect.NewResponse(&pb.AddDiscoveryAddressResponse{Cluster: out, Audit: audit}), nil
}

// The Events internal/controller records when discovery reaches a Talos node that already has a
// configuration (ADR 0103 section 2; Orphan, ADR 0136 amendment). Kept as literals: this package does
// not import the controller.
const (
	reasonAlreadyConfigured             = "AlreadyConfigured"
	reasonOrphan                        = "Orphan"
	annotationAlreadyConfiguredEndpoint = "ichor.maesh.dev/endpoint"
)

// fillDiscoveryFindings sets out's AlreadyConfiguredAddresses and OrphanAddresses from c's current
// state.
func (s *Server) fillDiscoveryFindings(ctx context.Context, out *pb.Cluster, c *ichorv1alpha1.TalosCluster) {
	out.AlreadyConfiguredAddresses, out.OrphanAddresses = s.discoveryFindings(ctx, c)
}

// alreadyConfiguredAddresses is the AlreadyConfigured half of discoveryFindings.
func (s *Server) alreadyConfiguredAddresses(ctx context.Context, c *ichorv1alpha1.TalosCluster) []string {
	configured, _ := s.discoveryFindings(ctx, c)

	return configured
}

// discoveryFindings lists, sorted, the endpoints of c's AlreadyConfigured and Orphan Events. Events
// expire and outlive the state they describe, so the list is derived from current state: only the
// latest Event of an address counts (an address that was AlreadyConfigured and later Orphan, or the
// reverse, is listed once, under the latest), and an address a pending or approved TalosMachine of c
// now has is dropped (the machine came back, or was approved). A failed read yields none: the fields
// are advisory and must not fail the call they ride on.
func (s *Server) discoveryFindings(ctx context.Context, c *ichorv1alpha1.TalosCluster) (configured, orphans []string) {
	var list corev1.EventList
	if err := s.List(ctx, &list, client.InNamespace(c.Namespace)); err != nil {
		return nil, nil
	}

	type latest struct {
		reason string
		at     time.Time
	}

	byAddr := map[string]latest{}

	for i := range list.Items {
		ev := &list.Items[i]
		if (ev.Reason != reasonAlreadyConfigured && ev.Reason != reasonOrphan) ||
			ev.InvolvedObject.Kind != "TalosCluster" || ev.InvolvedObject.Name != c.Name {
			continue
		}

		ep := ev.Annotations[annotationAlreadyConfiguredEndpoint]
		if ep == "" {
			continue
		}

		at := ev.LastTimestamp.Time
		if at.IsZero() {
			at = ev.EventTime.Time
		}

		if at.IsZero() {
			at = ev.CreationTimestamp.Time
		}

		if prev, ok := byAddr[ep]; !ok || !at.Before(prev.at) {
			byAddr[ep] = latest{reason: ev.Reason, at: at}
		}
	}

	if len(byAddr) == 0 {
		return nil, nil
	}

	owned := map[string]bool{}

	var machines ichorv1alpha1.TalosMachineList
	if err := s.List(ctx, &machines, client.InNamespace(c.Namespace)); err == nil {
		for i := range machines.Items {
			m := &machines.Items[i]
			if m.Spec.ClusterRef.Name == c.Name && m.Spec.ApprovalState != ichorv1alpha1.MachineApprovalStateRejected {
				owned[endpointguard.HostOf(m.Spec.Endpoint)] = true
				owned[endpointguard.HostOf(m.Status.ObservedEndpoint)] = true
			}
		}
	}

	for ep, l := range byAddr {
		if owned[endpointguard.HostOf(ep)] {
			continue
		}

		if l.reason == reasonOrphan {
			orphans = append(orphans, ep)
		} else {
			configured = append(configured, ep)
		}
	}

	slices.Sort(configured)
	slices.Sort(orphans)

	return configured, orphans
}

// ipv4Address trims s and accepts it only as a plain IPv4 address, which is all the CRD allows.
func ipv4Address(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("address is required")
	}

	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is4() {
		return "", fmt.Errorf("%q is not an IPv4 address (use a form like 192.168.0.50; ranges belong in spec.discovery.cidrs)", s)
	}

	return a.String(), nil
}

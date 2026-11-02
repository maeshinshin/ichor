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

	"connectrpc.com/connect"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// maxDiscoveryCIDRs mirrors the CRD's MaxItems on spec.discovery.cidrs.
const maxDiscoveryCIDRs = 4

// editDiscovery reads the cluster, lets edit change spec.discovery, writes it as the caller and
// returns the stored cluster with its audit record. Nothing but spec.discovery is touched. The write
// itself wakes the TalosCluster reconcile, which is what starts a scan pass at once.
func (s *Server) editDiscovery(
	ctx context.Context, ref *pb.ObjectRef, action, reason, doing string,
	edit func(d *ichorv1alpha1.TalosClusterDiscovery) error,
) (*pb.Cluster, *pb.AuditRecord, error) {
	var c ichorv1alpha1.TalosCluster

	key := namespacedName(ref)
	if err := s.Get(ctx, key, &c); err != nil {
		return nil, nil, mapGetError("cluster "+key.String(), err)
	}

	if c.Spec.Discovery == nil {
		c.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{}
	}

	if err := edit(c.Spec.Discovery); err != nil {
		return nil, nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := s.Update(ctx, &c, strictFieldValidation); err != nil {
		return nil, nil, mapOperationWriteError(doing, key, err)
	}

	audit := &pb.AuditRecord{
		Actor: s.actorName(ctx), At: timestamppb.New(s.now()), Action: action,
		Target: objectRef(c.Namespace, c.Name), Reason: reason,
	}

	return clusterToPB(&c), audit, nil
}

// AddDiscoveryCidr appends one IPv4 range (/24 or smaller) to spec.discovery.cidrs.
func (s *Server) AddDiscoveryCidr(
	ctx context.Context, req *connect.Request[pb.AddDiscoveryCidrRequest],
) (*connect.Response[pb.AddDiscoveryCidrResponse], error) {
	cidr, err := ipv4Cidr(req.Msg.GetCidr())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	cl, audit, err := s.editDiscovery(ctx, req.Msg.GetCluster(), "add-discovery-cidr", req.Msg.GetReason(),
		"adding a discovery range to", func(d *ichorv1alpha1.TalosClusterDiscovery) error {
			if slices.Contains(d.CIDRs, cidr) {
				return fmt.Errorf("%s is already in the discovery list", cidr)
			}

			if len(d.CIDRs) >= maxDiscoveryCIDRs {
				return fmt.Errorf("at most %d ranges can be listed; remove one first", maxDiscoveryCIDRs)
			}

			d.CIDRs = append(d.CIDRs, cidr)

			return nil
		})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.AddDiscoveryCidrResponse{Cluster: cl, Audit: audit}), nil
}

// RemoveDiscoveryAddress removes one address from spec.discovery.addresses.
func (s *Server) RemoveDiscoveryAddress(
	ctx context.Context, req *connect.Request[pb.RemoveDiscoveryAddressRequest],
) (*connect.Response[pb.RemoveDiscoveryAddressResponse], error) {
	addr, err := ipv4Address(req.Msg.GetAddress())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	cl, audit, err := s.editDiscovery(ctx, req.Msg.GetCluster(), "remove-discovery-address", req.Msg.GetReason(),
		"removing a discovery address from", func(d *ichorv1alpha1.TalosClusterDiscovery) error {
			i := slices.Index(d.Addresses, addr)
			if i < 0 {
				return fmt.Errorf("%s is not in the discovery list", addr)
			}

			d.Addresses = slices.Delete(d.Addresses, i, i+1)

			return nil
		})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.RemoveDiscoveryAddressResponse{Cluster: cl, Audit: audit}), nil
}

// RemoveDiscoveryCidr removes one range from spec.discovery.cidrs.
func (s *Server) RemoveDiscoveryCidr(
	ctx context.Context, req *connect.Request[pb.RemoveDiscoveryCidrRequest],
) (*connect.Response[pb.RemoveDiscoveryCidrResponse], error) {
	cidr, err := ipv4Cidr(req.Msg.GetCidr())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	cl, audit, err := s.editDiscovery(ctx, req.Msg.GetCluster(), "remove-discovery-cidr", req.Msg.GetReason(),
		"removing a discovery range from", func(d *ichorv1alpha1.TalosClusterDiscovery) error {
			i := slices.Index(d.CIDRs, cidr)
			if i < 0 {
				return fmt.Errorf("%s is not in the discovery list", cidr)
			}

			d.CIDRs = slices.Delete(d.CIDRs, i, i+1)

			return nil
		})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.RemoveDiscoveryCidrResponse{Cluster: cl, Audit: audit}), nil
}

// ipv4Cidr trims s and accepts it only as an IPv4 range of /24 or smaller (ADR 0011). It returns the
// masked form, so 192.168.0.5/24 is stored as the range it names, 192.168.0.0/24.
func ipv4Cidr(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("CIDR is required")
	}

	p, err := netip.ParsePrefix(s)
	if err != nil || !p.Addr().Is4() {
		return "", fmt.Errorf("%q is not an IPv4 range (use a form like 192.168.0.0/24)", s)
	}

	if p.Bits() < 24 {
		return "", fmt.Errorf("%s is larger than /24; ranges of up to 256 addresses are allowed (ADR 0011)", s)
	}

	return p.Masked().String(), nil
}

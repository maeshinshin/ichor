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
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/prmode"
)

// AddDiscoveryAddress appends one IPv4 address to spec.discovery.addresses and writes nothing else
// (ADR 0011: Ichor only dials what the cluster declares). It passes the same gate as every other
// write to an existing CR (planWrite): refused when the cluster is tracked and no pull request can
// be opened, a pull request when Argo CD tracks it, break-glass on request (ADR 0065).
func (s *Server) AddDiscoveryAddress(
	ctx context.Context, req *connect.Request[pb.AddDiscoveryAddressRequest],
) (*connect.Response[pb.AddDiscoveryAddressResponse], error) {
	ref := req.Msg.GetCluster()

	var c ichorv1alpha1.TalosCluster

	key := namespacedName(ref)
	if err := s.Get(ctx, key, &c); err != nil {
		return nil, mapGetError("cluster "+key.String(), err)
	}

	addr, err := ipv4Address(req.Msg.GetAddress())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	plan, err := s.planWrite(ctx, &c, c.Name, false, req.Msg.GetBreakGlass())
	if err != nil {
		return nil, err
	}

	if c.Spec.Discovery != nil && slices.Contains(c.Spec.Discovery.Addresses, addr) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s is already in the discovery list", addr))
	}

	now := s.now()
	stored := c.DeepCopy()

	if c.Spec.Discovery == nil {
		c.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{}
	}

	c.Spec.Discovery.Addresses = append(c.Spec.Discovery.Addresses, addr)

	if plan != nil && plan.bg == nil {
		return s.addDiscoveryByPR(ctx, plan, &c, stored, addr, now, req.Msg.GetReason())
	}

	if err := s.Update(ctx, &c, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("adding a discovery address to", key, err)
	}

	audit := &pb.AuditRecord{
		Actor: s.actorName(ctx), At: timestamppb.New(now), Action: "add-discovery-address",
		Target: objectRef(c.Namespace, c.Name), Reason: req.Msg.GetReason(),
	}

	url, notice := s.afterBreakGlass(ctx, plan, &c, audit, func() (string, error) {
		r, err := s.addDiscoveryByPR(ctx, plan, &c, stored, addr, now, req.Msg.GetReason())
		if err != nil {
			return "", err
		}

		return r.Msg.GetPullRequestUrl(), nil
	})

	return connect.NewResponse(&pb.AddDiscoveryAddressResponse{Cluster: clusterToPB(&c), Audit: audit, PullRequestUrl: url, Notice: notice}), nil
}

// addDiscoveryByPR is the pull request branch: it sets spec.discovery as a whole, because the file
// may not have the key yet and a missing parent is an error in PR mode.
func (s *Server) addDiscoveryByPR(
	ctx context.Context, plan *prPlan, c, stored *ichorv1alpha1.TalosCluster, addr string, now time.Time, reason string,
) (*connect.Response[pb.AddDiscoveryAddressResponse], error) {
	discovery, err := toGeneric(c.Spec.Discovery)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	url, err := s.openPR(ctx, plan, prChange{
		verb: "add-discovery-address", name: c.Name, target: clusterTarget(c),
		updates: []prmode.FieldUpdate{{Path: []string{specKey, "discovery"}, Value: discovery}},
		title:   fmt.Sprintf("Add %s to the discovery list of cluster %s", addr, c.Name),
		impact: fmt.Sprintf("- Affected clusters: 1 (%s)\n- The controller starts looking for a node in maintenance mode at %s.\n- Reason: %s",
			c.Name, addr, orNone(reason)),
	})
	if err != nil {
		return nil, err
	}

	audit := &pb.AuditRecord{
		Actor: s.actorName(ctx), At: timestamppb.New(now), Action: "add-discovery-address-pr",
		Target: objectRef(c.Namespace, c.Name), Reason: reason,
	}

	return connect.NewResponse(&pb.AddDiscoveryAddressResponse{Cluster: clusterToPB(stored), Audit: audit, PullRequestUrl: url}), nil
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

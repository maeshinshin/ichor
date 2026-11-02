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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// AddDiscoveryAddress appends one IPv4 address to spec.discovery.addresses and writes nothing else
// (Ichor only dials what the cluster declares).
func (s *Server) AddDiscoveryAddress(
	ctx context.Context, req *connect.Request[pb.AddDiscoveryAddressRequest],
) (*connect.Response[pb.AddDiscoveryAddressResponse], error) {
	addr, err := ipv4Address(req.Msg.GetAddress())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	ref := req.Msg.GetCluster()

	out, audit, err := s.editDiscovery(ctx, ref, "add-discovery-address", req.Msg.GetReason(),
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

	c := ichorv1alpha1.TalosCluster{ObjectMeta: metav1.ObjectMeta{Namespace: ref.GetNamespace(), Name: ref.GetName()}}
	out.AlreadyConfiguredAddresses = s.alreadyConfiguredAddresses(ctx, &c)

	return connect.NewResponse(&pb.AddDiscoveryAddressResponse{Cluster: out, Audit: audit}), nil
}

// The AlreadyConfigured Event internal/controller records when discovery reaches a Talos node that
// already has a configuration. Kept as literals: this package does not import
// the controller.
const (
	reasonAlreadyConfigured             = "AlreadyConfigured"
	annotationAlreadyConfiguredEndpoint = "ichor.maesh.dev/endpoint"
)

// alreadyConfiguredAddresses lists, sorted and without duplicates, the endpoints of c's
// AlreadyConfigured Events. A failed Events read yields none: the field is advisory and must not fail
// the call it rides on.
func (s *Server) alreadyConfiguredAddresses(ctx context.Context, c *ichorv1alpha1.TalosCluster) []string {
	var list corev1.EventList
	if err := s.List(ctx, &list, client.InNamespace(c.Namespace)); err != nil {
		return nil
	}

	var out []string

	for i := range list.Items {
		ev := &list.Items[i]
		if ev.Reason != reasonAlreadyConfigured || ev.InvolvedObject.Kind != "TalosCluster" ||
			ev.InvolvedObject.Name != c.Name {
			continue
		}

		if ep := ev.Annotations[annotationAlreadyConfiguredEndpoint]; ep != "" {
			out = append(out, ep)
		}
	}

	slices.Sort(out)

	return slices.Compact(out)
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

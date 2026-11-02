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

// Package endpointguard decides which addresses the controller may dial as a machine's Talos API
// ("Admission and confused deputy"). The controller pushes a rendered machine
// configuration, which carries the cluster's CA keys, to spec.endpoint, so an address a CR author
// can type must never reach the controller's own network position (loopback, link-local metadata
// services, the cluster's own pods and services) or an arbitrary host.
package endpointguard

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// hostOf drops the port of endpoint, if it has one.
func hostOf(endpoint string) string {
	if h, _, err := net.SplitHostPort(endpoint); err == nil {
		return h
	}

	return endpoint
}

func parseHost(host string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}

	return a.Unmap().WithZone(""), true
}

// Forbidden returns an error when endpoint ("host" or "host:port") is an IP literal the controller
// must never dial: unspecified, loopback, link-local, multicast, or inside the pod or service ranges
// cluster declares in spec.network (Talos's undeclared defaults are not assumed: they collide with
// the ranges of an outer cluster that hosts the machines). A hostname is not judged here: see
// ForbiddenResolved.
func Forbidden(endpoint string, cluster *ichorv1alpha1.TalosCluster) error {
	host := hostOf(endpoint)
	if host == "" {
		return fmt.Errorf("the endpoint %q has no host", endpoint)
	}

	if a, ok := parseHost(host); ok {
		return forbiddenAddr(a, cluster)
	}

	return nil
}

func forbiddenAddr(a netip.Addr, cluster *ichorv1alpha1.TalosCluster) error {
	switch {
	case a.IsUnspecified():
		return fmt.Errorf("the address %s is unspecified", a)
	case a.IsLoopback():
		return fmt.Errorf("the address %s is a loopback address", a)
	case a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast():
		return fmt.Errorf("the address %s is link-local", a)
	case a.IsMulticast(), a.IsInterfaceLocalMulticast():
		return fmt.Errorf("the address %s is a multicast address", a)
	}

	if n := cluster.Spec.Network; n != nil {
		for _, c := range append(append([]string{}, n.PodSubnets...), n.ServiceSubnets...) {
			if p, err := netip.ParsePrefix(c); err == nil && p.Contains(a) {
				return fmt.Errorf("the address %s is inside the cluster's own pod or service range %s", a, c)
			}
		}
	}

	return nil
}

// ForbiddenResolved is Forbidden, and for a hostname also judges every address it resolves to. A
// name that does not resolve passes: the dial fails by itself.
func ForbiddenResolved(ctx context.Context, endpoint string, cluster *ichorv1alpha1.TalosCluster) error {
	if err := Forbidden(endpoint, cluster); err != nil {
		return err
	}

	host := hostOf(endpoint)
	if _, ok := parseHost(host); ok {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil //nolint:nilerr // unresolvable: the dial fails on its own
	}

	for _, a := range addrs {
		if err := forbiddenAddr(a.Unmap().WithZone(""), cluster); err != nil {
			return fmt.Errorf("%s resolves to a forbidden target: %w", host, err)
		}
	}

	return nil
}

// InDiscovery reports whether endpoint's host is one of cluster.spec.discovery's addresses or
// inside one of its CIDRs: the only addresses an operator has declared as machine candidates.
func InDiscovery(endpoint string, cluster *ichorv1alpha1.TalosCluster) bool {
	d := cluster.Spec.Discovery
	if d == nil {
		return false
	}

	a, ok := parseHost(hostOf(endpoint))
	if !ok {
		return false
	}

	for _, s := range d.Addresses {
		if p, ok := parseHost(s); ok && p == a {
			return true
		}
	}

	for _, c := range d.CIDRs {
		if p, err := netip.ParsePrefix(c); err == nil && p.Contains(a) {
			return true
		}
	}

	return false
}

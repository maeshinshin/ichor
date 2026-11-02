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

package v1alpha1

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/render"
)

// Admission rules for the opt-in hardening options.

// SourceResolver returns the local address the controller would connect from to reach host. It
// is a seam for tests; the default asks the kernel's routing table (a UDP "connect" sends nothing).
type SourceResolver func(ctx context.Context, host string) (netip.Addr, error)

// routeSource is the default SourceResolver.
func routeSource(ctx context.Context, host string) (netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var d net.Dialer

	conn, err := d.DialContext(ctx, "udp", net.JoinHostPort(host, "6443"))
	if err != nil {
		return netip.Addr{}, err
	}
	defer func() { _ = conn.Close() }()

	ua, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return netip.Addr{}, fmt.Errorf("unexpected local address %v", conn.LocalAddr())
	}

	a, _ := netip.AddrFromSlice(ua.IP)

	return a.Unmap(), nil
}

// validateSecurityCreate checks spec.security of a new cluster.
func (v *TalosClusterCustomValidator) validateSecurityCreate(ctx context.Context, c *ichorv1alpha1.TalosCluster) (admission.Warnings, error) {
	if err := render.ValidateSecurity(c); err != nil {
		return nil, err
	}

	if err := v.checkControllerReach(ctx, c); err != nil {
		return nil, err
	}

	return firewallWarnings(c), nil
}

// validateSecurityUpdate checks spec.security of an updated cluster. Only what the update
// changes is checked beyond the static rules, so an object stored before a rule existed can
// still be edited for something else.
func (v *TalosClusterCustomValidator) validateSecurityUpdate(ctx context.Context, oldC, newC *ichorv1alpha1.TalosCluster) (admission.Warnings, error) {
	if apiequality.Semantic.DeepEqual(oldC.Spec.Security, newC.Spec.Security) {
		return nil, nil
	}

	if err := render.ValidateSecurity(newC); err != nil {
		return nil, err
	}

	var warns admission.Warnings

	if !apiequality.Semantic.DeepEqual(firewallOf(oldC), firewallOf(newC)) || oldC.Spec.Endpoint != newC.Spec.Endpoint {
		if err := v.checkControllerReach(ctx, newC); err != nil {
			return nil, err
		}

		if err := v.checkFirewallCoversMachines(ctx, newC); err != nil {
			return nil, err
		}

		warns = firewallWarnings(newC)
	}

	return warns, v.checkDiskEncryptionConfirmed(ctx, oldC, newC)
}

// firewallWarnings warns about a firewall with no talosAPICIDRs: nobody but the controller and
// the control planes can then reach apid, so a lock-out has no operator break-in path.
func firewallWarnings(c *ichorv1alpha1.TalosCluster) admission.Warnings {
	if !render.FirewallEnabled(c) || len(c.Spec.Security.Firewall.TalosAPICIDRs) > 0 {
		return nil
	}

	return admission.Warnings{"spec.security.firewall.talosAPICIDRs is empty: only the controller and the control planes can " +
		"reach the Talos API, so if the firewall locks Ichor out there is no talosctl path back (docs/guides/hardening.md); " +
		"list your workstation's network"}
}

// machineAddr is the address of a machine's Talos API endpoint ("10.0.0.1", "10.0.0.1:50000" or
// "[fd00::1]:50000"); false for a hostname, which is not checked.
func machineAddr(endpoint string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(endpoint); err == nil {
		return ap.Addr().Unmap(), true
	}

	a, err := netip.ParseAddr(strings.Trim(endpoint, "[]"))

	return a.Unmap(), err == nil
}

// checkFirewallCoversMachines refuses a firewall whose nodeCIDRs miss an approved machine or whose
// controlPlaneCIDRs miss an approved control plane: the rules built from them would cut the
// controller off that machine, and the controller could not deliver a corrected spec. Needs APIReader; machines
// whose endpoint is a hostname are skipped.
func (v *TalosClusterCustomValidator) checkFirewallCoversMachines(ctx context.Context, c *ichorv1alpha1.TalosCluster) error {
	if !render.FirewallEnabled(c) || v.APIReader == nil {
		return nil
	}

	machines, err := deleteguard.ApprovedMachines(ctx, v.APIReader, c.Namespace, c.Name, deleteguard.MachineFilter{IncludeDeleting: true})
	if err != nil {
		return err
	}

	fw := c.Spec.Security.Firewall

	for i := range machines {
		m := &machines[i]

		addr, ok := machineAddr(m.DialEndpoint())
		if !ok {
			continue
		}

		if !prefixesContain(fw.NodeCIDRs, addr) {
			return fmt.Errorf("spec.security.firewall.nodeCIDRs %v does not contain %s, the address of machine %s: "+
				"the firewall would cut it off from the cluster", fw.NodeCIDRs, addr, m.Name)
		}

		if m.Spec.Role == roleControlPlane && len(fw.ControlPlaneCIDRs) > 0 && !prefixesContain(fw.ControlPlaneCIDRs, addr) {
			return fmt.Errorf("spec.security.firewall.controlPlaneCIDRs %v does not contain %s, the address of control plane %s: "+
				"the other nodes (and Ichor, reaching them) would lose it", fw.ControlPlaneCIDRs, addr, m.Name)
		}
	}

	return nil
}

func prefixesContain(cidrs []string, addr netip.Addr) bool {
	return slices.ContainsFunc(cidrs, func(s string) bool {
		p, err := netip.ParsePrefix(s)

		return err == nil && p.Contains(addr)
	})
}

func firewallOf(c *ichorv1alpha1.TalosCluster) *ichorv1alpha1.TalosClusterFirewall {
	if c.Spec.Security == nil {
		return nil
	}

	return c.Spec.Security.Firewall
}

// checkControllerReach refuses a firewall that would cut the controller off: the address it
// reaches the cluster endpoint from must be inside firewall.controllerCIDRs. If the address
// cannot be found (an endpoint that does not resolve yet), the rule is not guessed at.
func (v *TalosClusterCustomValidator) checkControllerReach(ctx context.Context, c *ichorv1alpha1.TalosCluster) error {
	if !render.FirewallEnabled(c) {
		return nil
	}

	resolve := v.ControllerSource
	if resolve == nil {
		resolve = routeSource
	}

	src, err := resolve(ctx, c.Spec.Endpoint.Address)
	if err != nil {
		talosclusterlog.Info("not checking the firewall against the controller's address", "cluster", c.Name, "error", err.Error())

		return nil
	}

	if err := render.ControllerSourceAllowed(c, src); err != nil {
		return fmt.Errorf("%w; if the controller runs in a pod, list the pod network and its node's address", err)
	}

	return nil
}

// checkDiskEncryptionConfirmed refuses enabling or changing disk encryption on a cluster that
// already has approved machines unless the object carries the confirm annotation. Talos encrypts
// a volume only when it creates it, so existing nodes need the wipe-and-rejoin path
// (docs/guides/hardening.md), which Ichor does not automate.
func (v *TalosClusterCustomValidator) checkDiskEncryptionConfirmed(ctx context.Context, oldC, newC *ichorv1alpha1.TalosCluster) error {
	next := newC.Spec.Security
	if next == nil || next.DiskEncryption == nil {
		return nil
	}

	if prev := oldC.Spec.Security; prev != nil && apiequality.Semantic.DeepEqual(prev.DiskEncryption, next.DiskEncryption) {
		return nil
	}

	machines, err := deleteguard.ApprovedMachines(ctx, v.APIReader, newC.Namespace, newC.Name, deleteguard.MachineFilter{IncludeDeleting: true})
	if err != nil {
		return err
	}

	if len(machines) == 0 || newC.GetAnnotations()[ichorv1alpha1.TalosClusterDiskEncryptionConfirmAnnotation] == "true" {
		return nil
	}

	return fmt.Errorf("spec.security.diskEncryption changes for a cluster with %d approved machine(s): Talos encrypts a volume "+
		"only when it creates it, so each node must be wiped and rejoined (see docs/guides/hardening.md; Ichor does not wipe for you). "+
		"Set the annotation %s=\"true\" to confirm you will do that",
		len(machines), ichorv1alpha1.TalosClusterDiskEncryptionConfirmAnnotation)
}

// Talos API roles that must not be handed to pods (docs: Talos role-based access control).
const roleOSAdmin = "os:admin"

// talosAPIAccessWarnings checks every Config document of a TalosConfigPatch for
// machine.features.kubernetesTalosAPIAccess. os:admin in allowedRoles hands a Talos
// administrator to every pod in the allowed namespaces, so it is refused unless the user holds the
// break-glass verb on the patch; any enabled access is warned about.
func (v *TalosConfigPatchCustomValidator) talosAPIAccessWarnings(ctx context.Context, p *ichorv1alpha1.TalosConfigPatch) (admission.Warnings, error) {
	var warnings admission.Warnings

	for i, d := range p.Spec.Documents {
		if d.Config == nil || d.Config.MachineConfig == nil || d.Config.MachineConfig.MachineFeatures == nil {
			continue
		}

		a := d.Config.MachineConfig.MachineFeatures.KubernetesTalosAPIAccessConfig
		if a == nil || a.AccessEnabled == nil || !*a.AccessEnabled {
			continue
		}

		where := fmt.Sprintf("spec.documents[%d].config.machine.features.kubernetesTalosAPIAccess", i)

		if slices.Contains(a.AccessAllowedRoles, roleOSAdmin) {
			allowed := false

			if v.BreakGlass != nil {
				ok, err := v.BreakGlass(ctx, "talosconfigpatches", p.Name, p.Namespace)
				if err != nil {
					return nil, fmt.Errorf("%s: %s in allowedRoles is refused, and the %s right could not be checked: %w",
						where, roleOSAdmin, BreakGlassVerb, err)
				}

				allowed = ok
			}

			if !allowed {
				return nil, fmt.Errorf("%s: allowedRoles contains %s, which gives every pod in %v a Talos administrator "+
					"(use os:reader, or hold the %q verb on this patch to override)",
					where, roleOSAdmin, a.AccessAllowedKubernetesNamespaces, BreakGlassVerb)
			}

			breakglasslog.Info("os:admin Talos API access admitted under break-glass", "patch", p.Name)
		}

		warnings = append(warnings, fmt.Sprintf("%s grants Talos API access (%v) to pods in namespaces %v: a pod there can act on the nodes",
			where, a.AccessAllowedRoles, a.AccessAllowedKubernetesNamespaces))
	}

	return warnings, nil
}

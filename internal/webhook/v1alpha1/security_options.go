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
	"github.com/maeshinshin/ichor/pkg/secureboot"
)

// Admission rules for the opt-in hardening options.

// annotationTrue is the value of a confirmation annotation.
const annotationTrue = "true"

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

	return append(firewallWarnings(c), secureBootWarnings(c)...), nil
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

		covers, err := v.checkFirewallCoversMachines(ctx, newC)
		if err != nil {
			return nil, err
		}

		warns = append(covers, firewallWarnings(newC)...)
	}

	if err := v.checkDiskEncryptionConfirmed(ctx, oldC, newC); err != nil {
		return nil, err
	}

	sbWarns, err := v.checkSecureBootConfirmed(ctx, oldC, newC)
	if err != nil {
		return nil, err
	}

	if secureBootInputsChanged(oldC.Spec.Security, newC.Spec.Security) {
		warns = append(warns, secureBootWarnings(newC)...)
	}

	return append(warns, sbWarns...), nil
}

// secureBootInputsChanged reports whether an update touches what secureBootWarnings judge, so
// an unrelated security edit is not warned about again.
func secureBootInputsChanged(oldS, newS *ichorv1alpha1.TalosClusterSecurity) bool {
	if oldS == nil {
		oldS = &ichorv1alpha1.TalosClusterSecurity{}
	}

	if newS == nil {
		newS = &ichorv1alpha1.TalosClusterSecurity{}
	}

	return !apiequality.Semantic.DeepEqual(oldS.SecureBoot, newS.SecureBoot) || oldS.KernelLockdown != newS.KernelLockdown ||
		!apiequality.Semantic.DeepEqual(oldS.DiskEncryption, newS.DiskEncryption)
}

// secureBootWarnings are the static warnings about spec.security.secureBoot, kernelLockdown and
// their neighbours. Nothing here refuses: the cluster is the user's.
func secureBootWarnings(c *ichorv1alpha1.TalosCluster) admission.Warnings {
	s := c.Spec.Security
	if s == nil {
		return nil
	}

	mode := secureboot.Mode(s)

	var warns admission.Warnings

	switch {
	case s.KernelLockdown != "" && mode == ichorv1alpha1.SecureBootOff:
		warns = append(warns, fmt.Sprintf("spec.security.kernelLockdown %s is only checked, never rendered, and Talos v1.14 locks the kernel "+
			"down by itself only under SecureBoot: without secureBoot the level has to be built into the boot asset (an Image Factory "+
			"schematic with extraKernelArgs lockdown=%[1]s, set as spec.schematic)", s.KernelLockdown))
	case s.KernelLockdown == ichorv1alpha1.KernelLockdownConfidentiality:
		warns = append(warns, "spec.security.kernelLockdown confidentiality is not Talos's default under SecureBoot (integrity is): "+
			"nodes only reach it from an Image Factory schematic with extraKernelArgs lockdown=confidentiality, set as spec.schematic; "+
			"the KernelLockdown condition says which nodes do not")
	}

	if d := s.DiskEncryption; d != nil && d.Type == ichorv1alpha1.DiskEncryptionTPM && d.RequireSecureBoot != nil && *d.RequireSecureBoot &&
		mode == ichorv1alpha1.SecureBootOff {
		warns = append(warns, "spec.security.diskEncryption.requireSecureBoot is true but spec.security.secureBoot is not set: "+
			"enrolling the TPM key fails on a node booted without SecureBoot; set secureBoot.mode to Preferred or Required so the "+
			"SecureBoot installer is used")
	}

	return warns
}

// checkSecureBootConfirmed handles a change of spec.security.secureBoot.mode to Preferred or
// Required on a cluster whose approved machines do not all run SecureBoot. Talos cannot
// convert an installed node (GRUB to UKI, firmware keys, a TPM key bound to the SecureBoot state), so
// the machines stay as they are and are reported; Required, which says the cluster must not have such
// machines, is refused unless the object carries the confirm annotation. An unchanged mode is not
// asked again. Machines not read yet count as not running it.
func (v *TalosClusterCustomValidator) checkSecureBootConfirmed(
	ctx context.Context, oldC, newC *ichorv1alpha1.TalosCluster,
) (admission.Warnings, error) {
	mode := secureboot.Mode(newC.Spec.Security)
	if mode == ichorv1alpha1.SecureBootOff || mode == secureboot.Mode(oldC.Spec.Security) || v.APIReader == nil {
		return nil, nil
	}

	machines, err := deleteguard.ApprovedMachines(ctx, v.APIReader, newC.Namespace, newC.Name, deleteguard.MachineFilter{IncludeDeleting: true})
	if err != nil {
		return nil, err
	}

	var lacking []string

	for i := range machines {
		if s := machines[i].Status.Security; s == nil || !s.SecureBoot {
			lacking = append(lacking, machines[i].Name)
		}
	}

	if len(lacking) == 0 {
		return nil, nil
	}

	slices.Sort(lacking)

	names := strings.Join(lacking, ", ")

	if mode == ichorv1alpha1.SecureBootRequired && newC.GetAnnotations()[ichorv1alpha1.TalosClusterSecureBootConfirmAnnotation] != annotationTrue {
		return nil, fmt.Errorf("spec.security.secureBoot.mode Required for a cluster whose machine(s) %s do not run SecureBoot: Talos cannot "+
			"convert an installed node, so each must be reset and added again from the SecureBoot ISO, one at a time (see "+
			"docs/guides/hardening.md; Ichor does not do it for you). Set the annotation %s=\"true\" to confirm you will do that, or use Preferred",
			names, ichorv1alpha1.TalosClusterSecureBootConfirmAnnotation)
	}

	return admission.Warnings{fmt.Sprintf("spec.security.secureBoot.mode %s: machine(s) %s do not run SecureBoot and stay as they are until "+
		"they are reset and added again; their SecureBoot condition says so", mode, names)}, nil
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

// checkFirewallCoversMachines looks at the machines the firewall's CIDRs must cover. A
// controlPlaneCIDRs that misses an approved control plane is refused: the controller reaches the
// other nodes from a control plane's address, so Ichor would lose them and could not deliver a
// corrected spec. A nodeCIDRs that misses a machine only costs that
// machine its Cilium and trustd rules, so it is a warning. Needs APIReader; machines whose
// endpoint is a hostname are skipped.
func (v *TalosClusterCustomValidator) checkFirewallCoversMachines(ctx context.Context, c *ichorv1alpha1.TalosCluster) (admission.Warnings, error) {
	if !render.FirewallEnabled(c) || v.APIReader == nil {
		return nil, nil
	}

	machines, err := deleteguard.ApprovedMachines(ctx, v.APIReader, c.Namespace, c.Name, deleteguard.MachineFilter{IncludeDeleting: true})
	if err != nil {
		return nil, err
	}

	fw := c.Spec.Security.Firewall

	var warns admission.Warnings

	for i := range machines {
		m := &machines[i]

		addr, ok := machineAddr(m.DialEndpoint())
		if !ok {
			continue
		}

		if m.Spec.Role == roleControlPlane && len(fw.ControlPlaneCIDRs) > 0 && !prefixesContain(fw.ControlPlaneCIDRs, addr) {
			return nil, fmt.Errorf("spec.security.firewall.controlPlaneCIDRs %v does not contain %s, the address of control plane %s: "+
				"the other nodes (and Ichor, reaching them) would lose it", fw.ControlPlaneCIDRs, addr, m.Name)
		}

		if !prefixesContain(fw.NodeCIDRs, addr) {
			warns = append(warns, fmt.Sprintf("spec.security.firewall.nodeCIDRs %v does not contain %s, the address of machine %s: "+
				"the nodes' Cilium and trustd rules will not admit it", fw.NodeCIDRs, addr, m.Name))
		}
	}

	return warns, nil
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

	if len(machines) == 0 || newC.GetAnnotations()[ichorv1alpha1.TalosClusterDiskEncryptionConfirmAnnotation] == annotationTrue {
		return nil
	}

	return fmt.Errorf("spec.security.diskEncryption changes for a cluster with %d approved machine(s): Talos encrypts a volume "+
		"only when it creates it, so each node must be wiped and rejoined (see docs/guides/hardening.md; Ichor does not wipe for you). "+
		"Set the annotation %s=\"true\" to confirm you will do that",
		len(machines), ichorv1alpha1.TalosClusterDiskEncryptionConfirmAnnotation)
}

// Talos API roles that must not be handed to pods (docs: Talos role-based access control).
const roleOSAdmin = "os:admin"

// talosAPIAccessWarnings warns about every Config document of a TalosConfigPatch that enables
// machine.features.kubernetesTalosAPIAccess. It is the user's choice for their
// own cluster, so nothing is refused: os:admin in allowedRoles gets the strongest wording, as it
// hands a Talos administrator to every pod in the allowed namespaces. The feature renders on
// control planes only (pkg/render leaves it out of workers).
func talosAPIAccessWarnings(p *ichorv1alpha1.TalosConfigPatch) admission.Warnings {
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
			warnings = append(warnings, fmt.Sprintf("%s: allowedRoles contains %s, which gives every pod in %v a Talos administrator "+
				"(os:reader is enough for most uses)", where, roleOSAdmin, a.AccessAllowedKubernetesNamespaces))
		}

		warnings = append(warnings, fmt.Sprintf("%s grants Talos API access (%v) to pods in namespaces %v: a pod there can act on the nodes; "+
			"it applies to control planes only", where, a.AccessAllowedRoles, a.AccessAllowedKubernetesNamespaces))
	}

	return warnings
}

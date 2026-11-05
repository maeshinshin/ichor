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
	"slices"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	runtimeres "github.com/siderolabs/talos/pkg/machinery/resources/runtime"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/secureboot"
)

// SecurityState is what a node reports about its boot security (ADR 0123): the fields of Talos's
// SecurityState resource Ichor uses, and the kernel command line, from which the lockdown level
// is derived.
type SecurityState struct {
	SecureBoot, BootedWithUKI, ModuleSignatureEnforced bool
	UKISigningKeyFingerprint, PCRSigningKeyFingerprint string
	Cmdline                                            string
}

// ReadSecurityState reads the SecurityState and KernelCmdline resources (the runtime namespace;
// `talosctl get securitystate`, with --insecure in maintenance mode). A node without a
// SecurityState is an error, not a node without SecureBoot; a missing command line is empty.
func ReadSecurityState(ctx context.Context, cosi state.State) (SecurityState, error) {
	ss, err := safe.StateGetByID[*runtimeres.SecurityState](ctx, cosi, runtimeres.SecurityStateID)
	if err != nil {
		return SecurityState{}, fmt.Errorf("reading the security state: %w", err)
	}

	spec := ss.TypedSpec()
	out := SecurityState{
		SecureBoot: spec.SecureBoot, BootedWithUKI: spec.BootedWithUKI, ModuleSignatureEnforced: spec.ModuleSignatureEnforced,
		UKISigningKeyFingerprint: spec.UKISigningKeyFingerprint, PCRSigningKeyFingerprint: spec.PCRSigningKeyFingerprint,
	}

	cmd, err := safe.StateGetByID[*runtimeres.KernelCmdline](ctx, cosi, runtimeres.KernelCmdlineID)

	switch {
	case err == nil:
		out.Cmdline = cmd.TypedSpec().Cmdline
	case !state.IsNotFoundError(err):
		return SecurityState{}, fmt.Errorf("reading the kernel command line: %w", err)
	}

	return out, nil
}

// recordSecurity reads the node's boot security into m.Status.Security and sets the SecureBoot and
// KernelLockdown conditions for the options cluster sets (ADR 0123). It mutates m only; the caller's
// status write persists it. A node that cannot be asked keeps its last recorded state.
func recordSecurity(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, talos TalosClient,
) {
	callCtx, cancel := context.WithTimeout(ctx, talosCallTimeout)
	defer cancel()

	if ss, err := talos.SecurityState(callCtx); err == nil {
		m.Status.Security = ss.status()
	}

	want := secureboot.MachineConditions(cluster.Spec.Security, m.Status.Security)

	for _, typ := range []string{ichorv1alpha1.ConditionSecureBoot, ichorv1alpha1.ConditionKernelLockdown} {
		i := slices.IndexFunc(want, func(c metav1.Condition) bool { return c.Type == typ })
		if i < 0 {
			apimeta.RemoveStatusCondition(&m.Status.Conditions, typ)

			continue
		}

		c := want[i]
		c.ObservedGeneration = m.Generation
		apimeta.SetStatusCondition(&m.Status.Conditions, c)
	}
}

// requireSecureBoot is the first-configuration gate of spec.security.secureBoot.mode Required: it
// reads the maintenance-mode node's SecurityState and holds the machine (Ready=False,
// SecureBootRequired, nothing sent) unless it booted with SecureBoot. A state that cannot be read
// holds it too: Required is not guessed at. stop=false means carry on.
func (r *TalosMachineReconciler) requireSecureBoot(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, mc MaintenanceClient,
) (res ctrl.Result, stop bool, err error) {
	if secureboot.Mode(cluster.Spec.Security) != ichorv1alpha1.SecureBootRequired {
		return ctrl.Result{}, false, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, talosCallTimeout)
	defer cancel()

	ss, rerr := mc.SecurityState(callCtx)
	if rerr != nil {
		res, err = r.retryLater(ctx, m, reasonSecureBootRequired, fmt.Sprintf(
			"spec.security.secureBoot.mode is Required and the node's SecureBoot state could not be read (%v), so the "+
				"configuration was not sent", rerr))

		return res, true, err
	}

	m.Status.Security = ss.status()

	if ss.SecureBoot {
		return ctrl.Result{}, false, nil
	}

	res, err = r.retryLater(ctx, m, reasonSecureBootRequired,
		"spec.security.secureBoot.mode is Required and this node booted without SecureBoot, so the configuration was not sent: "+
			"boot it from the SecureBoot ISO of this cluster's schematic and Talos version with the firmware in setup mode "+
			"(docs/guides/hardening.md), or set the mode to Preferred")

	return res, true, err
}

// status is the form kept in TalosMachine.status.security.
func (s SecurityState) status() *ichorv1alpha1.TalosMachineSecurity {
	return &ichorv1alpha1.TalosMachineSecurity{
		SecureBoot: s.SecureBoot, BootedWithUKI: s.BootedWithUKI, ModuleSignatureEnforced: s.ModuleSignatureEnforced,
		UKISigningKeyFingerprint: s.UKISigningKeyFingerprint, PCRSigningKeyFingerprint: s.PCRSigningKeyFingerprint,
		KernelLockdown: secureboot.LockdownLevel(s.Cmdline, s.SecureBoot),
	}
}

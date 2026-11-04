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

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	runtimeres "github.com/siderolabs/talos/pkg/machinery/resources/runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/secureboot"
)

// SecurityState is what a node reports about its boot security (ADR 0123): the fields of Talos's
// SecurityState resource Ichor uses, and the kernel command line, from which the lockdown level
// is derived.
type SecurityState struct {
	SecureBoot, BootedWithUKI, ModuleSignatureEnforced bool
	UKISigningKeyFingerprint                           string
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
		UKISigningKeyFingerprint: spec.UKISigningKeyFingerprint,
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

// status is the form kept in TalosMachine.status.security.
func (s SecurityState) status() *ichorv1alpha1.TalosMachineSecurity {
	return &ichorv1alpha1.TalosMachineSecurity{
		SecureBoot: s.SecureBoot, BootedWithUKI: s.BootedWithUKI, ModuleSignatureEnforced: s.ModuleSignatureEnforced,
		UKISigningKeyFingerprint: s.UKISigningKeyFingerprint,
		KernelLockdown:           secureboot.LockdownLevel(s.Cmdline, s.SecureBoot),
	}
}

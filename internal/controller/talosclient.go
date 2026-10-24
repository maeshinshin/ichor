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

import "context"

// TalosClient is the Talos API surface the reconciler needs for one
// machine (architecture.md's "Configuration reconcile" steps 2 and 4). It
// is the one interface rule 0 allows: a real implementation dials machined
// over gRPC; tests use a fake, since no Talos call may ever reach a real
// node from a test (unit rule 2).
//
// Deliberately not on this interface: anything that decides reboot vs.
// no-reboot. ADR 0009 found that Talos's own ApplyConfiguration response
// cannot answer that (AUTO is a hardcoded alias for NO_REBOOT and never
// inspects the diff), so the reconciler decides from pkg/fieldmeta before
// it ever calls DryRunApply or ApplyNoReboot, and this interface has no
// method that could tempt it to ask Talos instead.
type TalosClient interface {
	// CurrentConfig returns the node's current machine configuration as
	// canonical YAML bytes (design step 2: "read the node's current
	// configuration over the Talos API"), the same encoding pkg/render
	// produces so the two hash comparably.
	CurrentConfig(ctx context.Context) ([]byte, error)

	// DryRunApply validates cfg against the node without applying it and
	// returns ValidateAtRuntime's warnings: deprecated-field migration
	// hints, missing cross-document configuration, label mismatches (ADR
	// 0009). It is always called before an apply, and is genuinely
	// side-effect free.
	DryRunApply(ctx context.Context, cfg []byte) (warnings []string, err error)

	// ApplyNoReboot applies cfg with ApplyConfigurationRequest_NO_REBOOT.
	// The reconciler only ever calls this once pkg/fieldmeta has said the
	// change needs neither a reboot nor a reinstall.
	ApplyNoReboot(ctx context.Context, cfg []byte) error
}

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

import ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"

// strongIdentity reports whether identity has every element of
// docs/design/architecture.md's "Nodes" identity tuple -- SMBIOS UUID,
// serial number, and at least one permanent MAC address -- the same test
// internal/controller.strongIdentity applies to a freshly discovered
// HardwareIdentity. ApproveMachine applies it a second time,
// here, against the TalosMachine's already-recorded
// TalosMachineSpec.identity: discovery refusing to auto-register a weak
// identity keeps most weak machines from ever reaching this point, but a
// machine created any other way (a future registration path, a test
// fixture, a hand-written manifest) is not guaranteed to have gone through
// discovery, so approval is the backstop that must not depend on it having
// happened.
//
// shortcut: duplicated rather than imported -- internal/controller's
// version is unexported and operates on its own HardwareIdentity, not the
// CRD's TalosMachineIdentity -- but it is the same three-line rule
// states once; if that rule ever changes, both copies need the same edit.
func strongIdentity(identity ichorv1alpha1.TalosMachineIdentity) bool {
	return identity.UUID != "" && identity.Serial != "" && len(identity.MACAddresses) > 0
}

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

// TalosMachineSecurity is what a node reported about its boot security (ADR 0123): Talos's
// SecurityState resource and its kernel command line.
type TalosMachineSecurity struct {
	// secureBoot is true when the node booted with UEFI SecureBoot enabled.
	// +optional
	SecureBoot bool `json:"secureBoot,omitempty"`

	// bootedWithUKI is true when the node booted a Unified Kernel Image. A node that did not (a
	// GRUB install) cannot get SecureBoot without a reinstall.
	// +optional
	BootedWithUKI bool `json:"bootedWithUKI,omitempty"`

	// moduleSignatureEnforced is true when the kernel only loads signed modules.
	// +optional
	ModuleSignatureEnforced bool `json:"moduleSignatureEnforced,omitempty"`

	// ukiSigningKeyFingerprint is the fingerprint of the key that signed the booted UKI; empty
	// without SecureBoot.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	UKISigningKeyFingerprint string `json:"ukiSigningKeyFingerprint,omitempty"`

	// kernelLockdown is the lockdown level in effect: the last lockdown= word of the kernel
	// command line, else integrity under SecureBoot (Talos v1.14's implicit level), else none.
	// +kubebuilder:validation:Enum=none;integrity;confidentiality
	// +optional
	KernelLockdown string `json:"kernelLockdown,omitempty"`
}

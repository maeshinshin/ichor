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

// TalosClusterContainerd is spec.containerd (ADR 0172). It is typed on purpose: Ichor renders the
// containerd configuration itself and takes no TOML.
type TalosClusterContainerd struct {
	// deviceOwnershipFromSecurityContext sets containerd's device_ownership_from_security_context, so
	// a container's block and character devices are owned by the pod's runAsUser and fsGroup instead
	// of root. Non-root workloads such as KubeVirt VMs and CDI importers need it to open a
	// volumeMode: Block volume. A containerd restart on every node, no node reboot; pods already
	// running keep their device ownership until they are recreated. Needs versionContract v1.14 or
	// later. False (the default) renders nothing.
	// +optional
	DeviceOwnershipFromSecurityContext bool `json:"deviceOwnershipFromSecurityContext,omitempty"`
}

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

// The RBAC rights the TalosMachine admission webhook asks (ADR 0108, "Admission and confused
// deputy"). Both are checked by SubjectAccessReview as the requesting user, so a hand-written CR
// obeys them like the CLI and the UI do.
const (
	// MachineApproveVerb on MachineApprovalSubresource is needed to move spec.approvalState to
	// approved (also at creation): the controller then pushes the cluster's rendered configuration,
	// which carries its CA keys, to spec.endpoint.
	MachineApproveVerb         = "approve"
	MachineApprovalSubresource = "approval"

	// MachineEndpointVerb on MachineEndpointSubresource is needed to set spec.endpoint to an
	// address that is not one the cluster's spec.discovery declares.
	MachineEndpointVerb        = "update"
	MachineEndpointSubresource = "endpoint"
)

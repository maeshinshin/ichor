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

// CiliumValuesVerb and CiliumValuesSubresource are the RBAC right that gates a change to
// TalosCluster.spec.cni.cilium.values: the verb update on talosclusters/cilium-values.
// The chart behind the values can run privileged host-path pods on every node, so only ichor-admin
// holds it. The validating webhook asks it for a hand-written CR; the UI backend and the CLI ask it first
// to refuse before anything is written.
const (
	CiliumValuesVerb        = "update"
	CiliumValuesSubresource = "cilium-values"
)

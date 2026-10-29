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

// OperationStartVerb is the RBAC verb that lets a user start a destructive TalosOperation. It is
// asked on the subresource StartSubresource names, because `create` on
// talosoperations cannot say which kind is being created.
const OperationStartVerb = "start"

// StartSubresource returns the talosoperations subresource whose `start` verb starting this
// operation needs, or "" for a kind that needs none. The destructive starts are a shutdown, a CA
// rotation, an etcd restore, and the node actions reset and shutdown, named shutdown,
// ca-rotation, etcd-restore, node-reset and node-shutdown.
func (s *TalosOperationSpec) StartSubresource() string {
	switch s.Kind {
	case TalosOperationKindShutdown, TalosOperationKindCARotation, TalosOperationKindEtcdRestore:
		return s.Kind
	case TalosOperationKindNode:
		if s.Node != nil && (s.Node.Action == NodeActionReset || s.Node.Action == NodeActionShutdown) {
			return TalosOperationKindNode + "-" + s.Node.Action
		}
	}

	return ""
}

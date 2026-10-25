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

// Package uiapi implements phase 5's read and approval surface
// (docs/design/architecture.md, "CLI and UI"; docs/adr/0016;
// docs/adr/0018): the server side of ClusterService, MachineService and
// NodeService. cmd/ichor-ui is the only importer.
//
// Every write this package makes is a Kubernetes API write to a custom
// resource -- it never calls a mutating Talos API ("UI and CLI change the
// world by writing CRs"; "the UI backend holds a read-only [Talos]
// identity"). ApproveMachine and RejectMachine are the only two methods
// that write anything, and both only ever update a TalosMachine.
package uiapi

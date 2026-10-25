//go:build ichorui_rbac

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

// This file carries every +kubebuilder:rbac marker for the UI backend's own
// ClusterRole (docs/adr/0018). It is never compiled into the real binary or
// any test (the ichorui_rbac tag above is never set by `go build`/`go
// test`/`go vet`) -- the markers are pure comments, with nothing here but a
// package clause, so excluding this file changes nothing about what the
// package does or how it is tested.
//
// It carries the //go:build tag instead of living next to the code the
// rules describe so that `make manifests` (paths="./...", no special
// tags) never discovers them and never merges them into the controller's
// manager-role ClusterRole. Only `make manifests-ichor-ui` passes
// --load-build-tags=ichorui_rbac, so this file -- and the ClusterRole it
// produces -- stays entirely separate from the controller's.
//
// Kept minimal (design principle: "the UI backend holds a read-only
// [Talos] identity"; this is its Kubernetes-API counterpart, not a claim
// that every verb here is read-only): get/list on talosclusters, get/
// list/update on talosmachines (ApproveMachine, RejectMachine), and now
// get/list/create/update/delete on talosconfigpatches (PatchService, ADR
// 0035, roadmap phase 6 -- the one CRD this package actually creates and
// deletes, not just updates).
package uiapi

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosconfigpatches,verbs=get;list;create;update;delete
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines,verbs=get;list;update
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,verbs=get

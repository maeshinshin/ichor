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
// ClusterRole. It is never compiled into the real binary or
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
// list/update on talosmachines (ApproveMachine, RejectMachine), get/list
// on the Kubernetes Node findKubernetesNode looks up (node_service.go),
// and get/list/create/update/delete on talosconfigpatches (PatchService, roadmap phase 6 -- the one CRD this package
// actually creates
// and deletes, not just updates).
package uiapi

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list;update
// OperationService: starting an upgrade changes the spec it names and creates the
// TalosOperation; pausing/deleting writes it. With OIDC on these run as the user, so
// the ServiceAccount needs them only for the OIDC-off case.
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosoperations,verbs=get;list;create;update;delete
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorinstallations,verbs=get;update
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosconfigpatches,verbs=get;list;create;update;delete
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines,verbs=get;list;update;delete
// GetEffectiveConfig renders through render.ResolveSystemInputs, which lists TalosStorages with
// the ServiceAccount; the caller's own list follows. List only, no informer.
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosstorages,verbs=list
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,verbs=get

// corev1.Node is itself a cluster-scoped Kubernetes type, unlike the secrets
// rule above: that one only needs the namespace= qualifier (and so only
// becomes a Role instead of this package's ClusterRole) because
// TalosCluster.spec.secretsRef happens to resolve to a fixed namespace --
// a design choice. A Node has
// no namespace to qualify into; namespace= on this marker would not narrow
// the grant, controller-gen would simply ignore it. So, unlike every other
// line here, this one has no narrower form to prefer -- ClusterRole is the
// only thing a Node read can ever be, not a convenience taken. get;list
// only (no watch: findKubernetesNode issues one Get or one List per GetNode
// call, never an informer) backs that lookup of the Node matching a
// TalosMachine's hostname or endpoint address.
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list

// GetNodeTalosRuntime lists the pods bound to one node: one List per call, filtered
// by spec.nodeName, read as the caller when OIDC is on. A Pod is namespaced, but the pods of a
// node live in every namespace, so the grant is cluster-wide get;list (no watch, no
// pods/log, pods/exec or secrets).
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list

// The two reads behind the "is this CR tracked by Argo CD or Flux" gate on
// every write. Both stay in this ClusterRole: Argo CD's
// namespace is configurable (Server.ArgoCDNamespace), and a Role/RoleBinding
// into a namespace that may not exist would fail the chart install. Narrowed instead
// by resourceNames, so this is get on ConfigMaps named argocd-cm and nothing
// else -- no cluster-wide ConfigMap or Secret read.
// +kubebuilder:rbac:groups="",resources=configmaps,resourceNames=argocd-cm,verbs=get
// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations,verbs=list

// PR mode reads the one Argo CD Application that tracks the CR
// being changed, to find the repository path its manifests live in. get only:
// the name comes from the CR's own tracking marker, never from a list. Cluster
// scoped for the same reason as the rules above (Argo CD's namespace is
// configurable). The repository token Secret needs no new rule: it is read in
// the backend's own namespace through the namespaced secrets rule at the top.
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get

// OIDC login: the backend calls the Kubernetes API as the
// signed-in user. impersonate on users/groups is the strongest verb in this
// file, so the chart only renders it when ichorUI.oidc.enabled and can
// narrow it with resourceNames (ichorUI.oidc.impersonate.users/groups); the
// backend itself refuses to impersonate a system: user. Never on
// serviceaccounts or userextras.
// +kubebuilder:rbac:groups="",resources=users;groups,verbs=impersonate

// Break-glass records a Warning Event on the resource it
// wrote. create only, on Events: no get/list/update/delete. The break-glass
// permission itself is not here: it is the user's, checked with a
// SelfSubjectAccessReview made as the user (every authenticated user may ask
// that), so the ServiceAccount gains nothing for it.
// +kubebuilder:rbac:groups="",resources=events,verbs=create

// Diagnostic downloads add nothing to this role on purpose. The download verb on
// talosmachines/support-bundle, talosmachines/etcd-snapshot and talosmachines/packet-capture is checked
// with a SelfSubjectAccessReview as the caller, and is granted to nobody here: the chart grants it to
// groups (or, only if the operator says so, to this ServiceAccount) from its values. The Event that
// audits each download needs the events create rule above.

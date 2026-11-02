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
// list/update on talosmachines (ApproveMachine, RejectMachine), get/list
// on the Kubernetes Node findKubernetesNode looks up (node_service.go),
// and get/list/create/update/delete on talosconfigpatches (PatchService,
// ADR 0035, roadmap phase 6 -- the one CRD this package actually creates
// and deletes, not just updates).
package uiapi

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list;update
// OperationService (docs/adr/0068): starting an upgrade changes the spec it names and creates the
// TalosOperation; pausing/deleting writes it. With OIDC on these run as the user (ADR 0058), so
// the ServiceAccount needs them only for the OIDC-off case.
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosoperations,verbs=get;list;create;update;delete
// The destructive starts (ADR 0094, Decision 4) need the verb start on talosoperations/<kind>. With
// OIDC on the backend asks it as the user; with OIDC off the backend is the caller and the validating
// webhook judges its ServiceAccount, so it holds the verb exactly as it holds create above.
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosoperations/shutdown;talosoperations/ca-rotation;talosoperations/etcd-restore;talosoperations/node-reset;talosoperations/node-shutdown,verbs=start
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorinstallations,verbs=get;update
// SettingsService (ADR 0105): with OIDC off the backend is the caller and the settings webhook judges its
// ServiceAccount, so it holds the settings subresource like it holds update above (never .../version).
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorinstallations/settings,verbs=update
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosconfigpatches,verbs=get;list;create;update;delete
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines,verbs=get;list;update;delete
// An etcd restore (ADR 0089) is planned and chosen from the backups' status: get/list, nothing written.
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosetcdbackups,verbs=get;list;update
// GetEffectiveConfig renders through render.ResolveSystemInputs, which lists TalosStorages with
// the ServiceAccount (ADR 0050); the caller's own list follows. List only, no informer.
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosstorages,verbs=list
// The secrets bundle and every other Secret are out of reach of this role (ADR 0108): whatever needs a CA
// key is the controller's. The backend asks it for a certificate or a cluster's public facts by creating a
// TalosCredentialRequest, in the cluster's namespace, and reads the answer back (get). With Ichor users on
// it creates them as the signed-in user, so the validating webhook judges that user; it is the
// ServiceAccount itself only for the purposes `public` and `node-reader`, for which the chart grants the
// verb `issue` on talosclusters/public and talosclusters/node-reader to this ServiceAccount alone.
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters/public;talosclusters/node-reader,verbs=issue
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=taloscredentialrequests,verbs=create;get

// corev1.Node is itself a cluster-scoped Kubernetes type, unlike the secrets
// rule above: that one only needs the namespace= qualifier (and so only
// becomes a Role instead of this package's ClusterRole) because
// TalosCluster.spec.secretsRef happens to resolve to a fixed namespace --
// a design choice, and this file is exactly where ADR 0011 records that
// choice getting undone and landing back in a ClusterRole twice. A Node has
// no namespace to qualify into; namespace= on this marker would not narrow
// the grant, controller-gen would simply ignore it. So, unlike every other
// line here, this one has no narrower form to prefer -- ClusterRole is the
// only thing a Node read can ever be, not a convenience taken. get;list
// only (no watch: findKubernetesNode issues one Get or one List per GetNode
// call, never an informer) backs that lookup of the Node matching a
// TalosMachine's hostname or endpoint address.
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list

// GetNodeTalosRuntime lists the pods bound to one node (ADR 0082): one List per call, filtered
// by spec.nodeName, read as the caller when OIDC is on. A Pod is namespaced, but the pods of a
// node live in every namespace, so the grant is cluster-wide get;list (no watch, no
// pods/log, pods/exec or secrets). It is the only widening of this role in that ADR.
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list

// CniService (docs/adr/0099) asks whether the Gateway API CRDs exist before it accepts
// gatewayAPI.enabled: one metadata get of that one CRD, as the ServiceAccount.
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,resourceNames=gateways.gateway.networking.k8s.io,verbs=get

// OIDC login (docs/adr/0058) and Ichor users (docs/adr/0094): the backend calls the Kubernetes API as the
// signed-in user. impersonate is the strongest verb in this file, so (ADR 0108) it never covers a name it
// has not been given: the built-in groups below are fixed here; the users, the Ichor role groups beyond
// the built-in ones and anything an external IdP names are listed by name elsewhere (the ClusterRole
// ichor-ui-impersonate that the controller keeps equal to the IchorUsers, and the chart values
// ichorUI.oidc.impersonate.*). Never serviceaccounts, userextras, or a system: name.
// +kubebuilder:rbac:groups="",resources=groups,resourceNames="ichor:viewer";"ichor:operator";"ichor:admin",verbs=impersonate

// Break-glass (docs/adr/0065) records a Warning Event on the resource it
// acted on. create only, on Events: no get/list/update/delete. The break-glass
// permission itself is not here: it is the user's, checked with a
// SelfSubjectAccessReview made as the user (every authenticated user may ask
// that), so the ServiceAccount gains nothing for it.
// The operation page lists the Events about an operation (docs/adr/0068); with OIDC on that list is made as the
// signed-in user, so this rule only serves the no-OIDC mode.
// +kubebuilder:rbac:groups="",resources=events,verbs=create;list

// Diagnostic downloads (docs/adr/0084) add nothing to this role on purpose. The download verb on
// talosmachines/support-bundle, talosmachines/etcd-snapshot and talosmachines/packet-capture is checked
// with a SelfSubjectAccessReview as the caller, and is granted to nobody here: the chart grants it to
// groups (or, only if the operator says so, to this ServiceAccount) from its values. The Event that
// audits each download needs the events create rule above.

// Ichor users (docs/adr/0094), in ichor-system only. The per-RPC re-read of the signed-in IchorUser
// is a get with the ServiceAccount. The password hashes live in one Secret that the backend patches
// after a SelfSubjectAccessReview as the caller: get, update, patch on that name and no other, never
// create (the chart makes the Secret) and never list. The user CRs themselves are written as the
// caller, so the ServiceAccount needs nothing more on ichorusers.
// +kubebuilder:rbac:groups=ichor.maesh.dev,namespace=ichor-system,resources=ichorusers,verbs=get
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,resourceNames=ichor-user-credentials,verbs=get;update;patch

// "Reset and add" (MachineReclaim; docs/adr/0103, 2.2, docs/adr/0107), in ichor-system only. The backend creates the credential
// Secret, with the owner reference set at creation (the MachineReclaim is written first); it can never read it back or patch it.
// The MachineReclaim itself is written as the caller, so the ServiceAccount only reads and lists it
// (when OIDC is off it also creates and updates it; the webhook then judges the ServiceAccount, which
// holds no reclaim verb until an operator binds ichor-machine-reclaimer to it: ADR 0107).
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,verbs=create
// +kubebuilder:rbac:groups=ichor.maesh.dev,namespace=ichor-system,resources=machinereclaims,verbs=get;list;create;update

// /metrics (docs/adr/0108) is served only to a caller that proves a Kubernetes token and may `get` the
// non-resource URL /metrics: the same TokenReview and SubjectAccessReview the controller's secure metrics
// endpoint makes. create only, on those two review kinds.
// +kubebuilder:rbac:groups=authentication.k8s.io,resources=tokenreviews,verbs=create
// +kubebuilder:rbac:groups=authorization.k8s.io,resources=subjectaccessreviews,verbs=create

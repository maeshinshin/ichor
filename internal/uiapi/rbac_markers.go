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
// Every RPC carries the signed-in user (AuthInterceptor), and cl(ctx) makes each write under that user, so
// the writes on the user-requested CRs are the user's own RBAC and the validating webhook's. This role
// holds no write verb on those CRs (ADR 0108, amendments of 2026-11-16). What it writes itself is named
// below where it is granted: the Events of an override or download, the TalosCredentialRequests it asks the
// controller for, the credentials Secret's patch and the reclaim Secret's create. Reads are granted as
// before. The point is that the UI backend holds a read-only Talos identity and no Kubernetes write on the
// CRs a user changes.
package uiapi

// OperationService and ClusterService read TalosClusters as the ServiceAccount, and write them as the
// signed-in user (cl(ctx)): the write verbs are the user's, not this role's.
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list
// The operations are created, paused, acknowledged and deleted as the signed-in user. The ServiceAccount
// reads them only.
// There is no start verb here, on purpose: the destructive starts (talosoperations/<kind>) are the
// signed-in user's own right, asked as the user, and a ServiceAccount that held them would let the
// validating webhook, which asks whoever wrote the TalosOperation, pass a start nobody signed in for.
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosoperations,verbs=get;list
// SettingsService writes the installation as the signed-in user, and checks the settings subresource
// as that user too (never .../version): this role holds no update on it.
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorinstallations,verbs=get
// PatchService creates, updates and deletes TalosConfigPatches as the signed-in user; the reads are unchanged.
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosconfigpatches,verbs=get;list
// Machine writes (RejectMachine, SetMachineInstall, RemoveMachine) are made as the signed-in user. The
// ServiceAccount cannot approve: the webhook asks for approve on talosmachines/approval and fails closed.
// The only delete of a TalosMachine is RemoveMachine, which asks for the override verb, so the ServiceAccount
// never reaches it, and it is the user who deletes.
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines,verbs=get;list
// An etcd restore is planned and chosen from the backups' status: get/list, nothing written. The backup
// request is written as the signed-in user.
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosetcdbackups,verbs=get;list
// GetEffectiveConfig renders through render.ResolveSystemInputs, which lists TalosStorages with
// the ServiceAccount; the caller's own list follows. List only, no informer.
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosstorages,verbs=list
// The secrets bundle and every other Secret are out of reach of this role: whatever needs a CA
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

// CniService asks whether the Gateway API CRDs exist before it accepts
// gatewayAPI.enabled: one metadata get of that one CRD, as the ServiceAccount.
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,resourceNames=gateways.gateway.networking.k8s.io,verbs=get

// OIDC login and Ichor users: the backend calls the Kubernetes API as the
// signed-in user. impersonate is the strongest verb in this file, so it never covers a name it
// has not been given: the built-in groups below are fixed here; the users, the Ichor role groups beyond
// the built-in ones and anything an external IdP names are listed by name elsewhere (the ClusterRole
// ichor-ui-impersonate that the controller keeps equal to the IchorUsers, and the chart values
// ichorUI.oidc.impersonate.*). Never serviceaccounts, userextras, or a system: name.
// +kubebuilder:rbac:groups="",resources=groups,resourceNames="ichor:viewer";"ichor:operator";"ichor:admin",verbs=impersonate

// Override records a Warning Event on the resource it
// acted on. create only, on Events: no get/list/update/delete. The override
// permission itself is not here: it is the user's, checked with a
// SelfSubjectAccessReview made as the user (every authenticated user may ask
// that), so the ServiceAccount gains nothing for it.
// The operation page lists the Events about an operation as the signed-in user, so the list is not one the
// backend makes as itself; it is still granted here, unchanged.
// +kubebuilder:rbac:groups="",resources=events,verbs=create;list

// Diagnostic downloads add nothing to this role on purpose. The download verb on
// talosmachines/support-bundle, talosmachines/etcd-snapshot and talosmachines/packet-capture is checked
// with a SelfSubjectAccessReview as the caller, and is granted to nobody here: the chart grants it to
// groups (or, only if the operator says so, to this ServiceAccount) from its values. The Event that
// audits each download needs the events create rule above.

// Ichor users, in ichor-system only. The per-RPC re-read of the signed-in IchorUser
// is a get with the ServiceAccount. The password hashes live in one Secret that the backend patches
// (a merge patch, never an update) after a SelfSubjectAccessReview as the caller:
// get and patch on that name and no other, never update, create (the chart makes the Secret) or list.
// The user CRs themselves are written as the caller, so the ServiceAccount needs nothing more on ichorusers.
// +kubebuilder:rbac:groups=ichor.maesh.dev,namespace=ichor-system,resources=ichorusers,verbs=get
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,resourceNames=ichor-user-credentials,verbs=get;patch

// GetEffectiveConfig renders the kube-apiserver's authentication layer, which reads the
// trust ConfigMap the controller publishes: get on that one name in ichor-system, never list or watch.
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=configmaps,resourceNames=ichor-oidc-trust,verbs=get

// "Reset and add" (MachineReclaim), in ichor-system only. The backend creates the credential
// Secret, with the owner reference set at creation (the MachineReclaim is written first); it can never read it back or patch it.
// The MachineReclaim itself is created, confirmed and cancelled as the signed-in user, so the ServiceAccount
// only reads and lists it.
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,verbs=create
// +kubebuilder:rbac:groups=ichor.maesh.dev,namespace=ichor-system,resources=machinereclaims,verbs=get;list

// /metrics is served only to a caller that proves a Kubernetes token and may `get` the
// non-resource URL /metrics: the same TokenReview and SubjectAccessReview the controller's secure metrics
// endpoint makes. create only, on those two review kinds.
// +kubebuilder:rbac:groups=authentication.k8s.io,resources=tokenreviews,verbs=create
// +kubebuilder:rbac:groups=authorization.k8s.io,resources=subjectaccessreviews,verbs=create

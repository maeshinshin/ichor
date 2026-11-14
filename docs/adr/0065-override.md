# 0065. Override: a direct write that also opens the matching PR

Status: Accepted (UI and webhook added 2026-10-26); superseded in part by ADR 0101 (the tracked-write part); amended by ADR 0110 (the reason is optional) · Date: 2026-10-26 · Design section: GitOps and Argo CD; Validation
Decided: override is a request flag (`Override{reason}` on approve, reject, update-patch and delete-patch) that, only where the answer would otherwise be a pull request or a refusal because Argo CD tracks the resource, writes the CR directly and then opens the same pull request ADR 0056 would have opened; it is allowed only when Kubernetes RBAC says so, asked with a `SelfSubjectAccessReview` made as the user (verb `override`, group `ichor.maesh.dev`, resource `talosmachines` or `talosconfigpatches`, the object's name and namespace), so Ichor keeps no list of who may; with OIDC off (the backend on its own ServiceAccount) it is refused, and the CLI's `--override` always runs in-process on the caller's kubeconfig; the reason is optional (ADR 0110) and goes to `AuditRecord` (action `<verb>-override`), a Warning Event `Override` on the resource, and the PR title and description; Ichor never touches an Application's sync policy, so the response carries a `notice` that says the write will be reverted if the Application has `selfHeal` on, and to pause it in Argo CD until the PR merges; a validating webhook (`/validate-ichor-maesh-dev-v1alpha1-override`, UPDATE and DELETE of `talosmachines` and `talosconfigpatches`, fail-closed) refuses, unless the requesting user holds the same verb (a `SubjectAccessReview` on the admission request's `userInfo`), a direct update or delete of a CR that `gitopstracking.IsTracked` reports as tracked, and the removal of an `ichor.maesh.dev/*` finalizer from an object that is being deleted; Ichor's own controller, Argo CD's application controller, Flux's kustomize and helm controllers and the two kube-system controllers that cascade deletions are exempt by exact username; when tracking cannot be determined the write is refused; the UI offers the path after a pull request or a refusal, asks for a required reason and shows the response `notice`

## Context

architecture.md, "GitOps and Argo CD": "Override exists for incidents: a permission-gated direct write that also
opens the matching PR, so Git catches up. Ichor never touches an Application's sync policy." "Validation":
"direct writes to GitOps-managed CRs are rejected except through override." "Nodes": "Refusing finalizer
removal except through override belongs with the other dangerous-operation checks." The design does not say
how permission is expressed, what happens with OIDC off, how the write and the PR relate, what is recorded, or
how Argo CD's selfHeal fits. ADR 0056 made `planWrite` the one place that chooses direct write, PR or refusal;
ADR 0058 made the backend call Kubernetes as the signed-in user.

## Decisions

**1. Permission is a Kubernetes RBAC verb.** `override` on `talosmachines` and `talosconfigpatches` (group
`ichor.maesh.dev`). The backend creates a `SelfSubjectAccessReview` through the same impersonated client it uses
for everything else (`Server.cl`), so the API server answers for exactly the user. A `SelfSubjectAccessReview`
may be created by any authenticated user (the built-in `system:basic-user` role), so the ServiceAccount gains
no `subjectaccessreviews` grant and nothing is added to the impersonation scope. Cluster admins grant the verb
with `ichor-override-role` (shipped, bound to nobody; `config/rbac/override_role.yaml` and
`dist/chart/templates/rbac/ichor-override-role.yaml`), bound as narrowly as they like (a RoleBinding in one
namespace limits it to that namespace). Roles with verbs `*` on these resources, such as the generated `*-admin-role`
helpers, include it; the editor roles do not. The user needs their ordinary `update`/`delete` right too, because
the write is made as them.

*OIDC off.* With no signed-in user the request would be answered by the ServiceAccount, which holds
`update` on every machine: asking it "may I?" proves nothing about the person at the keyboard. Override is
refused with a message that says to enable OIDC or use the CLI. Refusal is the safe side; nobody loses a
capability they had (override did not exist). *CLI:* `Server.CallerCredentials` is true in the in-process
fallback, whose `Client` is the caller's own kubeconfig (ADR 0049), so the same review is made with it.
`--override <reason>` implies `--local`: the flag's value is the reason, and a reason-less flag is refused
by the Server. The CLI audit actor stays `ichor-cli` (ADR 0058, decision 5); the API server's own audit log names
the user.

**2. Where it applies, and what is refused.** `planWrite` takes the request's `Override` and decides in one
place. If the write would be direct anyway (untracked, relaxed) the flag is ignored: nothing is checked,
recorded or labelled, because no gate was crossed. If the answer is a PR plan (tracked and connected, or a
strict approval), the plan gains `bg` after the reason is non-empty and the review passes; handlers then take the
direct-write path they always had and, once it succeeded, open the plan's PR. If the CR is tracked and no PR can
be opened (no `gitops.repository`, Flux, a source that is not a plain path, a different repository) override
is refused with `FailedPrecondition`: a direct write Git can never catch up with is exactly the divergence the
design forbids. Order inside a handler: authorization happens in `planWrite`, before anything is written or
validated; the PR is opened after the write, because in an incident the write is the point and a validation
failure should not leave a stale PR. If the write succeeds and the PR then fails (Git host down), the call does
not fail: the write cannot be undone, so the response says in `notice` that the PR could not be opened and must
be opened by hand, and that Git does not match the cluster until it merges.

**3. The PR.** The same `prChange` ADR 0056 builds, with the title prefixed `[override]` and the impact
section saying who wrote the CR already and why. Branch `ichor/<verb>-<name>` and the open-PR dedup of ADR 0056
decision 6 apply unchanged. The PR is not merged by Ichor.

**4. selfHeal.** Ichor does not change an Application's sync policy (decided). The write is a live-state change
that Git does not yet have; with `spec.syncPolicy.automated.selfHeal: true` Argo CD reverts it quickly. The
backend already reads the Application to find the file (`get` only), so it also reads `selfHeal` and the response
`notice` says one of: the Application has selfHeal on, so the write will be reverted unless the PR merges first,
and to pause selfHeal on that Application in Argo CD until it does; or it does not, so the change stays (the
Application shows OutOfSync) and will be reverted if selfHeal is turned on; or (strict approval) nothing tracks
the resource yet. Pausing selfHeal and merging stay human actions. Rejected alternative: Ichor patching the
Application to pause selfHeal and restoring it later. It contradicts the decided rule, needs write access to
Argo CD, and a crash between the two steps would leave self-heal off.

**5. Audit.** Three records, all carrying who and why. (a) `AuditRecord` in the response: `actor` is the OIDC
user, `action` is `approve-override`, `reject-override`, `update-override` or `delete-override`,
`reason` is the override reason. (b) A Warning Event, reason `Override`, on the resource, message
`<verb> <name> by <user> under override: <reason>`, created with the backend's own client (`events` `create`
is the only new ServiceAccount permission, marker in `rbac_markers.go`; users are not given Event rights). If the
Event cannot be created the write already happened: the `notice` says so. (c) The PR title and description.
There is no `AuditRecord` custom resource; the response, the Event and the PR are the durable trail, with the API
server's own audit log as the authoritative one. The reason is required (`InvalidArgument` when empty or blank).

**6. Finalizer removal and tracked writes: the webhook.** The only operation in this design that is safe only through
override is removing a finalizer from a resource the controller is still working on (a mid-sequence
`TalosMachine`, or the deletion guards): it orphans a cordoned node or an etcd voter. Neither the UI nor the CLI
removes finalizers, so the rule lives in the admission webhook (`internal/webhook/v1alpha1/override_webhook.go`),
which also enforces the design's "direct writes to GitOps-managed CRs are rejected except through override"
for anyone who bypasses the UI and CLI (`kubectl edit`). It is a separate webhook entry from the existing
validators (path `/validate-ichor-maesh-dev-v1alpha1-override`, `failurePolicy: Fail`, UPDATE and DELETE,
`talosmachines` and `talosconfigpatches`), so the one rule covers both kinds and the per-kind validators stay as
they are. In order, for each request:

1. Not UPDATE or DELETE: allowed. CREATE is not checked: a new object is not yet "a GitOps-managed CR", and a
   check there would also stop Helm or a user creating an object that merely carries `app.kubernetes.io/instance`.
2. The username is exempt (decision 8): allowed.
3. A `SubjectAccessReview` is created for `userInfo` (username, UID, groups, extra), verb `override`, the
   resource and the object's name and namespace. Allowed means the request is allowed and nothing else is read.
   Holders therefore still work when Argo CD or Flux cannot be read, which is when an incident is likely. The webhook
   server never impersonates anyone and gains only `subjectaccessreviews` `create` (cluster scoped; `create` cannot be
   narrowed by `resourceNames`).
4. UPDATE that removes an `ichor.maesh.dev/*` finalizer the stored object has, while the stored object has a
   `deletionTimestamp`: denied, tracked or not.
5. Otherwise `gitopstracking.IsTracked` on the stored object (not the new one, so an update cannot strip the marker
   to get past it): tracked is denied, with the message naming the manager and the verb to hold.

*Cost.* An ordinary user's write to an untracked CR now costs one `SubjectAccessReview` and the reads `IsTracked`
makes (`argocd-cm` by name, one list of Flux `Kustomization`s). All of it is uncached (`client.New`, no informer, so no
list or watch grant on ConfigMaps) and uses RBAC the controller already has for the same check (`configmaps/argocd-cm`
`get`, `kustomizations` `list`, ADR 0044); nothing else is added to the controller's role.

**7. UI.** The dialog is offered in two situations only: after the write came back as a pull request (the PR is
already open; the direct write then reuses it through ADR 0056's dedup) and after a refusal (`FailedPrecondition`:
tracked and no PR possible; `PermissionDenied`). It is not asked beforehand whether the user may (no new RPC): the
button is always offered there, and Kubernetes' refusal, whose message states the missing verb, is shown inside the
dialog. The dialog (`OverrideDialog`) says what the call does (writes now, also opens the pull request) and that
Argo CD may revert it while selfHeal is on, requires a non-blank reason (the submit button stays disabled, and
the server refuses it as well), repeats the same call with `override{reason}`, and on success shows the response
`notice` (the selfHeal warning, or a failed PR) and the pull request link with wording that says the cluster already
changed. It is wired into `approve node`, `reject node`, `apply` and `delete patch`. The helper logic is in
`ui/src/lib/override.ts` (reason check, request shape, when to offer).

**8. Who is exempt, and how.** Exact usernames, never groups or namespaces: `system:serviceaccount:<ns>:<name>` for
(a) Ichor's own controller (`POD_NAMESPACE` and `POD_SERVICE_ACCOUNT`, the latter from the downward API field
`spec.serviceAccountName`, so a renamed ServiceAccount stays right), (b) Argo CD's `argocd-application-controller`
in the Argo CD namespace (`ICHOR_ARGOCD_NAMESPACE`, default `argocd`), the identity that applies a sync, (c) Flux's
`kustomize-controller` and `helm-controller` in `flux-system`, and (d) kube-system's `namespace-controller` and
`generic-garbage-collector`, because a namespace or owner deletion cascades into tracked CRs and would be refused and hang. `ICHOR_OVERRIDE_EXEMPT_USERS` (comma separated) adds exact names for an Argo CD or Flux
that lives elsewhere or syncs through an impersonated account. Why not a group such as `system:serviceaccounts:argocd`:
it would also exempt `argocd-server`, which writes on behalf of people clicking in its UI, and so turn the exemption
into a way round the rule. Why exemption at all: a refused sync breaks GitOps for everything, and Ichor's controller
removes its own finalizers and must never wait on a human. The cost of exact names is that an Argo CD that is
configured to impersonate per-Application accounts needs those names added; the symptom is a sync that fails with
the webhook's message, which names the verb and the object. The controller's own writes arrive as that identity
only when it has one: `make run` against a kubeconfig user is not exempt, but a cluster admin
(`system:masters`) always holds every verb.

**9. When tracking cannot be decided.** `IsTracked` can fail (the API is unavailable, `argocd-cm` is forbidden, the
Flux list fails; a missing `argocd-cm` or a missing Flux CRD is "not tracked", not a failure). Then the write is
refused with 503 and the cause in the message (ADR 0044: when it cannot be decided, do not write). That is safe
because the usual victims are exempt before any lookup: Argo CD's sync and Ichor's controller never reach
`IsTracked`, so an outage there cannot block GitOps or the controller, and holders of the verb skip it too. A person
without the verb who edits while Argo CD is unreadable is refused until it is back, or until a holder writes. A
failing `SubjectAccessReview` is refused the same way. The webhook server being down refuses all writes to these
kinds (ADR 0006's fail-closed), unchanged, but a lookup that hangs now also holds each non-exempt admission
request until the API server's webhook timeout (10 s default).

## Consequences

- Override in the UI needs OIDC. Without OIDC only the CLI can use it.
- An override write is a normal write to the API server as the user, so the webhook (decision 6) sees that user and recognises the verb.
- Two requests for the same change return the same PR (ADR 0056) but the second one still writes directly.
- Amended: the webhook now also refuses a direct UPDATE of a GitOps-tracked `TalosCluster` unless the user holds `override` on `talosclusters` (same exempt users, same 503 when tracking cannot be decided); DELETE is covered by the amendment below, and the `override` role lists `talosclusters`. `CreatePatch` stays ungated on purpose (ADR 0051, decision 1 above: a new object is not yet GitOps-managed), so it has no `override` field.
- Amended: the cluster webhook rule now also matches DELETE, and deleting a GitOps-tracked `TalosCluster` needs `override` on `talosclusters` like an update (same exempt users, same 503 when tracking cannot be decided; the allow-delete guard is unchanged). The `override` role and `ichor-admin` both already list `talosclusters`.
- Amended: override also overrides the last-worker guard, at all three places it applies, with the same verb and no new type. (1) The TalosMachine webhook (deleting or un-approving the last approved worker of a cluster whose `schedulable` is `"false"`) and (2) the TalosCluster webhook (turning `schedulable` to `"false"` with no approved worker) ask the same SubjectAccessReview as the override handler, as the requesting user, for `override` on `talosmachines/<name>` or `talosclusters/<name>`; they ask only when the guard would refuse, a failed check refuses, and an override is logged like other override writes. (3) The controller's reboot and shutdown guard (ADR 0085) cannot see the user, so a node `TalosOperation` carries the optional `spec.node.overrideReason` (an append-only field). The operation webhook admits it only when the creator holds `override` on the machine, as it does for a reset; the CLI and UI fill it from the existing `--override <reason>` flag and `Override` request field, check the verb, record the audit action with the `-override` suffix and the reason, and emit the Warning Event on the machine, as for a reset; the controller records a Warning Event on the operation when it goes ahead under the reason. All three refusals now say how to override. A plain delete with `kubectl` by a holder of the verb works with no flag. This waives only the last-worker rule: quorum, the control-plane count and the allow-delete annotation are unchanged.
- Amended by ADR 0110: the reason is optional. An override is requested by a flag (`--override`, `spec.node.override`, or the presence of the `Override` message), and the same verb gates it with or without a reason. Where this record says a reason is required, it is recorded instead as "no reason given" (the audit record, the Event, and the etcd restore's stored `reason`). A reason still counts as an override, so operations that carry only `overrideReason` or the deprecated `breakGlassReason` keep their gate.

Amendment 2026-11-05 (ADR 0130): no reason is asked for, required or accepted any more. `Override{reason}` is deprecated and never read (the message's presence is the override), `--override` is a boolean with no `--override-reason`, `spec.node.overrideReason` is deprecated and unused, and the Warning Event and the audit record say who overrode what, with no reason clause. The verb is still the only gate.

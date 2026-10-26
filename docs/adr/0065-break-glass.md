# 0065. Break-glass: a direct write that also opens the matching PR

Status: Accepted · Date: 2026-10-26 · Design section: GitOps and Argo CD; Validation
Decided: break-glass is a request flag (`BreakGlass{reason}` on approve, reject, update-patch and delete-patch) that, only where the answer would otherwise be a pull request or a refusal because Argo CD tracks the resource, writes the CR directly and then opens the same pull request ADR 0056 would have opened; it is allowed only when Kubernetes RBAC says so, asked with a `SelfSubjectAccessReview` made as the user (verb `break-glass`, group `ichor.maesh.dev`, resource `talosmachines` or `talosconfigpatches`, the object's name and namespace), so Ichor keeps no list of who may; with OIDC off (the backend on its own ServiceAccount) it is refused, and the CLI's `--break-glass <reason>` always runs in-process on the caller's kubeconfig; a reason is required and goes to `AuditRecord` (action `<verb>-break-glass`), a Warning Event `BreakGlass` on the resource, and the PR title and description; Ichor never touches an Application's sync policy, so the response carries a `notice` that says the write will be reverted if the Application has `selfHeal` on, and to pause it in Argo CD until the PR merges; refusing finalizer removal except through break-glass is a webhook check of the same verb, left to the webhook owner

## Context

architecture.md, "GitOps and Argo CD": "Break-glass exists for incidents: a permission-gated direct write that also
opens the matching PR, so Git catches up. Ichor never touches an Application's sync policy." "Validation":
"direct writes to GitOps-managed CRs are rejected except through break-glass." "Nodes": "Refusing finalizer
removal except through break-glass belongs with the other dangerous-operation checks." The design does not say
how permission is expressed, what happens with OIDC off, how the write and the PR relate, what is recorded, or
how Argo CD's selfHeal fits. ADR 0056 made `planWrite` the one place that chooses direct write, PR or refusal;
ADR 0058 made the backend call Kubernetes as the signed-in user.

## Decisions

**1. Permission is a Kubernetes RBAC verb.** `break-glass` on `talosmachines` and `talosconfigpatches` (group
`ichor.maesh.dev`). The backend creates a `SelfSubjectAccessReview` through the same impersonated client it uses
for everything else (`Server.cl`), so the API server answers for exactly the user. A `SelfSubjectAccessReview`
may be created by any authenticated user (the built-in `system:basic-user` role), so the ServiceAccount gains
no `subjectaccessreviews` grant and nothing is added to the impersonation scope. Cluster admins grant the verb
with `ichor-break-glass-role` (shipped, bound to nobody; `config/rbac/break_glass_role.yaml` and
`dist/chart/templates/rbac/ichor-break-glass-role.yaml`), bound as narrowly as they like (a RoleBinding in one
namespace limits it to that namespace). Roles with verbs `*` on these resources, such as the generated `*-admin-role`
helpers, include it; the editor roles do not. The user needs their ordinary `update`/`delete` right too, because
the write is made as them.

*OIDC off.* With no signed-in user the request would be answered by the ServiceAccount, which holds
`update` on every machine: asking it "may I?" proves nothing about the person at the keyboard. Break-glass is
refused with a message that says to enable OIDC or use the CLI. Refusal is the safe side; nobody loses a
capability they had (break-glass did not exist). *CLI:* `Server.CallerCredentials` is true in the in-process
fallback, whose `Client` is the caller's own kubeconfig (ADR 0049), so the same review is made with it.
`--break-glass <reason>` implies `--local`: the flag's value is the reason, and a reason-less flag is refused
by the Server. The CLI audit actor stays `ichor-cli` (ADR 0058, decision 5); the API server's own audit log names
the user.

**2. Where it applies, and what is refused.** `planWrite` takes the request's `BreakGlass` and decides in one
place. If the write would be direct anyway (untracked, relaxed) the flag is ignored: nothing is checked,
recorded or labelled, because no gate was crossed. If the answer is a PR plan (tracked and connected, or a
strict approval), the plan gains `bg` after the reason is non-empty and the review passes; handlers then take the
direct-write path they always had and, once it succeeded, open the plan's PR. If the CR is tracked and no PR can
be opened (no `gitops.repository`, Flux, a source that is not a plain path, a different repository) break-glass
is refused with `FailedPrecondition`: a direct write Git can never catch up with is exactly the divergence the
design forbids. Order inside a handler: authorization happens in `planWrite`, before anything is written or
validated; the PR is opened after the write, because in an incident the write is the point and a validation
failure should not leave a stale PR. If the write succeeds and the PR then fails (Git host down), the call does
not fail: the write cannot be undone, so the response says in `notice` that the PR could not be opened and must
be opened by hand, and that Git does not match the cluster until it merges.

**3. The PR.** The same `prChange` ADR 0056 builds, with the title prefixed `[break-glass]` and the impact
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
user, `action` is `approve-break-glass`, `reject-break-glass`, `update-break-glass` or `delete-break-glass`,
`reason` is the break-glass reason. (b) A Warning Event, reason `BreakGlass`, on the resource, message
`<verb> <name> by <user> under break-glass: <reason>`, created with the backend's own client (`events` `create`
is the only new ServiceAccount permission, marker in `rbac_markers.go`; users are not given Event rights). If the
Event cannot be created the write already happened: the `notice` says so. (c) The PR title and description.
There is no `AuditRecord` custom resource; the response, the Event and the PR are the durable trail, with the API
server's own audit log as the authoritative one. The reason is required (`InvalidArgument` when empty or blank).

**6. Finalizer removal.** The only operation in this design that is safe only through break-glass is removing a
finalizer from a resource the controller is still working on (a mid-sequence `TalosMachine`, or
`TalosCluster`/`TalosMachine` deletion guards): it orphans a cordoned node or an etcd voter. Neither the UI nor the
CLI removes finalizers, so there is nothing to gate in `internal/uiapi`. The rule belongs to the admission
webhook (`internal/controller` / `internal/webhook`, another owner): reject an update that removes
`ichor.maesh.dev/*` finalizers from an object whose `deletionTimestamp` is set, unless the requesting user holds
the same `break-glass` verb on that resource (a `SubjectAccessReview` on `request.userInfo`). The same check is
how the webhook's "direct writes to GitOps-managed CRs are rejected except through break-glass" can recognise a
break-glass write, since the write reaches the API server as the user. Not done here; the verb and role are
ready for it.

**7. UI.** Nothing in `ui/` changes in this PR (another change is working there). The responses already carry
`pull_request_url`, so the existing notice shows the PR; a follow-up should send `break_glass` from a confirm
dialog that asks for the reason and show `notice` (selfHeal warning) next to the PR link. Until then break-glass
is CLI-only, which is also the safer default.

## Consequences

- Break-glass in the UI needs the follow-up above and OIDC. Without OIDC only the CLI can use it.
- A break-glass write is a normal write to the API server as the user, so any admission webhook sees that user.
- Two requests for the same change return the same PR (ADR 0056) but the second one still writes directly.
- Shortcut (`ponytail:`): no break-glass for `TalosCluster` (the UI/CLI write none) and for creating a patch
  (`CreatePatch` writes a new object, which is not tracked yet).

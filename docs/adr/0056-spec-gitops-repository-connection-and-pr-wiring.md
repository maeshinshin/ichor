# 0056. `TalosCluster.spec.gitops`: a repository connection, strict mode, and the UI/CLI opening PRs

Status: Accepted · Date: 2026-10-26 · Design section: GitOps and Argo CD
Decided: `TalosCluster.spec.gitops` carries `mode` (`relaxed` default, `strict`) and an optional `repository` connection (host, https API URL, owner/repo, https clone URL, base branch, a `tokenSecretRef` read in the UI backend's own namespace, `machinesPath` required for strict); `Server.requireUntracked` becomes `planWrite`, which sends a CR Argo CD tracks to a pull request when a connection exists and refuses otherwise, and sends a strict approval to a pull request that adds the `TalosMachine` file; changing `mode` rewrites nothing; the response carries `pull_request_url` and the audit action gets a `-pr` suffix

## Context

ADR 0051 made every UI/CLI write refuse a tracked CR because no repository connection could be expressed.
`pkg/prmode` (ADR 0037, 0038) could already clone, edit and open a PR, and `Decide` already named
`ModeOpenPR`, but nothing fed it a connection, and nothing handled strict mode (architecture.md: "approving a
node *is* a PR that adds the `TalosMachine`").

## Decisions

**1. The shape.** `spec.gitops.mode` is `relaxed` (default) or `strict`. `spec.gitops.repository` is the
connection: `host` (`github|gitlab|gitea`), `apiURL`, `owner`, `repo`, `cloneURL`, `baseBranch` (default
`main`), `tokenSecretRef` (a `SecretKeySelector`) and `machinesPath`. CEL and patterns reject what can be
rejected at admission: both URLs must be `https://` and carry no `@` (so no credentials in a CR that lives in
Git, and the token is never sent in clear text); `owner`/`repo`/`machinesPath` are patterns; `machinesPath` may
not contain `.` or `..` segments; `strict` needs `repository` with `machinesPath`. The Go side re-checks the
new-file path anyway (`prmode.SubmitPR` refuses an absolute path, `..`, `.git/` and an existing file).

The token is a reference, never a value. The Secret is read in the UI backend's own namespace
(`Server.ControllerNamespace`), never `cluster.Namespace` (ADR 0011), through the namespaced
`ichor-ui-secrets-role` that already exists for the secrets bundle; an empty namespace is refused rather than
read as "all". No RBAC was added for the token. `get` on `applications.argoproj.io` was added (cluster scoped,
like ADR 0051's rules, because Argo CD's namespace is configurable): PR mode reads the one Application named by
the CR's own tracking marker (`gitopstracking.ArgoApplicationOf`), never lists.

**2. Changing `mode`.** It is a mutable field and changing it rewrites nothing. `relaxed` -> `strict`: the
`TalosMachine`s already written directly stay in the cluster and are not in Git; nothing prunes or reverts them.
From then on a new approval is a PR. `ichor gitops export` is how existing ones reach Git. `strict` -> `relaxed`:
a PR already open stays open; new approvals write directly. Mode is read on each call, so there is no state to
migrate. The design says nothing more, and a controller that "fixes" the difference would break "the controller
never mutates `spec`".

**3. Strict approval.** `ApproveMachine` runs every check it always ran (already approved, weak identity,
role), then, instead of `Update`, calls `SubmitPR` with a new `prmode.NewFile`: the approved `TalosMachine`
(`apiVersion`, `kind`, `name`, `namespace`, `spec`; no status, no server metadata) at
`<machinesPath>/<name>.yaml`. `Locate` has nothing to find for a CR that is not in Git, so `SubmitPRRequest` got
a `NewFile` branch that skips `Locate`/`Patch` and refuses an existing file. The pending `TalosMachine` in the
cluster is not written: it stays pending until the PR merges and Argo CD applies the file over it. A
`TalosMachine` Argo CD already tracks takes the edit-in-place path of decision 4 in either mode. Rejecting an
untracked pending machine is not an "add": a rejection records a discovery decision and is written directly, in
both modes. Strict applies to approval only.

**4. Relaxed, tracked CR.** `planWrite` runs `IsTracked`, then `Decide(by, repository != nil)`:
`ModeDirectWrite` writes as before; `ModeOpenPR` finds the Application, requires `ClassifySource` to return
`SourceFile`, requires its `repoURL` to be the connection's `cloneURL` (a different repository would be pushed
with the wrong token), uses the Application's `targetRevision` as the base branch (`HEAD`/empty falls back to
`baseBranch`), and opens the PR; `ModeReadOnly` refuses with ADR 0051's message. Edits: approve/reject set
`spec.approvalState`, `approvalDecision`, and `role`/`hostname`/`nodeLabels` when set (`prmode.Patch` now adds a
missing leaf key; a missing parent is still an error); `UpdatePatch` replaces the file's `spec` node whole,
matching its full-replace semantics, so comments inside that node are lost (shortcut). `DeletePatch` of a tracked
patch stays refused even with a connection: removing a file is a different edit and Argo CD prune semantics
decide the rest. Flux stays read-only (ADR 0037). A cluster that cannot be read (not found) is "relaxed, no
repository" so an untracked write is never newly blocked; any other read error refuses.

The PR description carries the requester and an impact section: for a patch, its layer/target and
`fieldmeta.Assess`'s reboot / install-time-only / unsupported (the `PreviewImpact` rules); for a machine, the
affected node and what approving starts. The response carries `pull_request_url`
(`ApproveMachineResponse`, `RejectMachineResponse`, `UpdatePatchResponse`); `machine`/`patch` in it are the
unchanged stored object. The audit record's action becomes `approve-pr`, `reject-pr` or `update-pr`. The CLI
already prints the whole response, so it shows the URL; the UI shows a notice with a link (http(s) only) and no
screen follows the PR.

**5. Requester.** `AuditRecord.actor` is still the fixed `ichor-ui` / `ichor-cli` string (OIDC is phase 9). The
PR says "Requested by: ichor-ui". When OIDC lands the same value will be the user, with no change here.

## Token handling

The token goes to git only through `GIT_CONFIG_*` `http.extraHeader` in the environment (`prmode.gitAuthEnv`) and
to the host as a request header. Errors from `SubmitPR` are scrubbed of the token and its base64 Basic form
before they are returned; the Secret read errors name the Secret, never its data. Tests drive a host that echoes
the `Authorization` header on failure, a failing clone, and the success path, and look for the token in the
error, the PR body, the response and the pushed file.

## Consequences

- A PR is opened per click: branches are `ichor/<verb>-<name>-<unix time>`, so two clicks open two PRs. Nothing
  tracks them or de-duplicates; following a PR to merge is a separate unit. Until it merges the machine stays
  pending and the UI would offer approval again.
- `machinesPath` is one directory for every cluster's machines in that connection; a layout that needs more is
  later work.
- The new-file manifest carries no labels or annotations from the live `TalosMachine`.
- A merged strict PR relies on Argo CD applying over the existing pending `TalosMachine`; if the Application
  prunes or uses `Replace`, that is the Application's setting, not Ichor's.

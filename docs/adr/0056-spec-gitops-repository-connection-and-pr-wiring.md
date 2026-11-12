# 0056. `TalosCluster.spec.gitops`: a repository connection, strict mode, and the UI/CLI opening PRs

Status: Superseded by ADR 0101 · Date: 2026-10-26 · Design section: GitOps and Argo CD
Decided: `TalosCluster.spec.gitops` carries `mode` (`relaxed` default, `strict`) and an optional `repository` connection (host, https API URL, owner/repo, https clone URL, base branch, a `tokenSecretRef` read in the UI backend's own namespace, `machinesPath` required for strict); `Server.requireUntracked` becomes `planWrite`, which sends a CR Argo CD tracks to a pull request when a connection exists and refuses otherwise, and sends a strict approval to a pull request that adds the `TalosMachine` file; changing `mode` rewrites nothing; the response carries `pull_request_url` and the audit action gets a `-pr` suffix; amended 2026-10-26: the PR branch is `ichor/<verb>-<name>` (since 2026-10-27 `ichor/<verb>-<namespace>-<name>`) and a request whose head branch already has an open PR returns that PR instead of opening another, and deleting a tracked `TalosConfigPatch` becomes a PR that removes its document (or its file)

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
matching its full-replace semantics, merged into the existing node (`FieldUpdate.Merge`) so comments and formatting on unchanged fields are kept; a comment on a removed key or item goes with it. `DeletePatch` of a tracked
patch is a PR too (decision 6). Flux stays read-only (ADR 0037). A cluster that cannot be read (not found) is "relaxed, no
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

**6. One open PR per change, and deleting by PR (amendment).** The first version named branches
`ichor/<verb>-<name>-<unix time>`, so every click on a still-pending machine opened another PR that conflicted
with the first on merge.

- *Identity.* The branch is now `ichor/<verb>-<name>` (`approve`, `reject`, `update`, `delete`). Before cloning,
  `prmode.SubmitPR` asks the host for an open PR with that head and the same base (`FindOpenPullRequest`); if
  there is one it returns its URL and pushes nothing. The UI needs no change for this: the response already
  carries `pull_request_url`, so the existing notice shows the URL of the PR that is already open. No screen
  lists open PRs.
- *A second request with different content* (approving again with another role) gets the open PR back; it is
  not amended or force-pushed under a reviewer. To change it, close that PR (or edit its branch) and ask again.
  The PR body keeps the request it was opened with.
- *A lookup that fails is an error*, never "none", since "none" would open a duplicate. Its error text is
  scrubbed of the token like any other.
- *A branch left behind* (the earlier PR was closed or merged and the branch kept) has no open PR, so the push
  that follows replaces it with `--force`. The `ichor/` branches are Ichor's own; a person's work does not
  belong on one.
- *Host calls* (per each host's docs). GitHub: `GET /repos/{owner}/{repo}/pulls?state=open&head={owner}:{branch}&base={base}`
  (docs.github.com/en/rest/pulls/pulls). GitLab: `GET /api/v4/projects/:id/merge_requests?state=opened&source_branch=&target_branch=`
  (docs.gitlab.com/api/merge_requests). Gitea/Forgejo: the list endpoint has no head filter, so `GET
  /api/v1/repos/{owner}/{repo}/pulls/{base}/{head}` ("get a pull request by base and head", 404 when none;
  docs.gitea.com/api/1.22). Anything not `open` counts as none. Same `net/http` shape as the create calls.
- *Deleting.* `SubmitPRRequest.Delete` locates the file as an edit does and `prmode.RemoveDocument` drops the
  one matching document with its `---`; every other byte, comments and spacing included, stays as it was. When
  nothing but comments is left, the file is removed (`git rm`). `DeletePatch` returns `pull_request_url`
  (`DeletePatchResponse` field 2, audit action `delete-pr`) and writes nothing to the cluster. Whether Argo CD
  prunes the live object after the merge is the Application's setting. The UI's delete dialog shows the notice
  in place of navigating away.

## Token handling

The token goes to git only through `GIT_CONFIG_*` `http.extraHeader` in the environment (`prmode.gitAuthEnv`) and
to the host as a request header. Errors from `SubmitPR` are scrubbed of the token and its base64 Basic form
before they are returned; the Secret read errors name the Secret, never its data.

## Consequences

- Until a strict-approval PR merges the machine stays pending and the UI still offers approval; a click now
  returns the open PR's URL (decision 6). Nothing follows a PR to merge, and the UI does not mark a machine
  as having one open.
- Amended 2026-10-27: the branch is `ichor/<verb>-<namespace>-<name>` (`prBranchName`), so same-named objects in
  different namespaces no longer share one; a name with characters outside `[a-z0-9-]` or over 100 bytes gets an
  8-hex hash of the full name appended, which keeps it a valid, bounded ref. PRs opened under the old name are not found.
- `machinesPath` is one directory for every cluster's machines in that connection; a layout that needs more is
  not built.
- The new-file manifest carries no labels or annotations from the live `TalosMachine`.
- A merged strict PR relies on Argo CD applying over the existing pending `TalosMachine`; if the Application
  prunes or uses `Replace`, that is the Application's setting, not Ichor's.

(Amended 2026-10-26, ADR 0065: a request may carry `Override{reason}` to write a tracked or strict-mode CR directly and still open this PR, if Kubernetes RBAC grants the `override` verb.)

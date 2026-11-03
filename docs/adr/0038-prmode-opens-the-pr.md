# 0038. PR mode: clone, push and open a PR with `net/http` and the `git` binary, never a host SDK

Status: Superseded by ADR 0101 · Date: 2026-10-26 · Design section: GitOps and Argo CD (phase 8, "PR mode")

Decided: `pkg/prmode` now clones a repository, pushes a branch via the `git` CLI (never `go-git`), and opens a PR/MR via one `net/http` POST per host (never a GitHub/GitLab/Gitea SDK) -- with the token carried only through `git`'s own `GIT_CONFIG_*` environment-variable mechanism and an HTTP request header, never argv, a URL, or a log line

## Context

ADR 0037 built `ClassifySource`/`Locate`/`Patch`/`Diff` and deliberately stopped
before "open a PR" -- a host-specific API call this unit now builds, finishing
the half of phase 8's "PR mode" paragraph that does not include merging it or
the UI following one.

## Decision 1: no host SDK

`go-github`, `go-gitlab` and `code.gitea.io/sdk` were not evaluated further
than noting that each would be the *only* reason to depend on it: creating a
pull/merge request is one `POST` per host, with three small, host-specific
JSON body shapes (`pr_hosts.go`). `postJSON` (same file) is the one shared
`net/http`/`encoding/json` helper all three call. No SDK was added, and this
was not escalated back, since the task was explicit that adding one would be.

## Decision 2: push options were checked and rejected, not skipped

Checked against each host's own documentation (2026-10-26), not assumed:

- **Gitea** has no push option for pull-request creation at all -- only
  `repo.private` and `repo.template` (`docs.gitea.com/usage/push-options`).
  Nothing to use there; the REST API is the only path regardless.
- **GitLab** does have `merge_request.create`/`.title`/`.description`/
  `.target` push options. But two things make the REST API still the better
  choice even where this exists: (a) a push-option-created MR's URL comes
  back only as human-readable text in the push's own remote output (e.g.
  `remote: View merge request for ... at <url>`), not a structured field --
  parsing that reliably is a worse bet than the one JSON response field
  (`web_url`) the REST call already returns for free; (b) GitLab's own docs
  do not document newline or formatting behavior for
  `merge_request.description` passed as a single `-o` argument value, and
  this unit's description needs a multi-line diff, which the REST API's JSON
  body transport has no such ambiguity about.

So this unit uses the REST API uniformly across all three hosts, not a mix --
investigated, not guessed, and the reasoning above is why "two hosts' worth
of code disappears" did not happen: only one host even has the option, and
using it there would trade a reliable structured response for an unreliable
text-parsed one.

## Decision 3: the `git` binary via `os/exec`, not `go-git`

This project already depends on `git` being present (`flake.nix`, CI); adding
`go-git` would be a second, large way to do the same clone/commit/push this
unit needs and nothing else. The decisive factor, as asked: **how the token
is passed in**. `os/exec` lets the token travel only through the child
process's environment (invisible to `ps`, unlike argv) via `git`'s own
`GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_n`/`GIT_CONFIG_VALUE_n` mechanism
(`git-config(1)`, "ENVIRONMENT") setting `http.extraHeader` to an HTTP Basic
header -- no config file, no `-c` argument, no URL-embedded credential.
`go-git`'s own `http.BasicAuth` struct would also keep the token out of argv
(it is in-process, not a subprocess argument at all) and was not rejected for
leaking it; it was rejected for being a second git implementation this
project would then depend on for no behavior the `git` binary does not
already provide via a well-documented, scriptable mechanism.

## Decision 4: the token's path, and how "it never leaks" is tested

`gitAuthEnv` (`git.go`) is the one place the token is read: it returns
environment-variable key/value pairs, never touching `exec.Cmd.Args`. The
remote URL passed to `git clone`/`git push` carries no credential at all --
auth is 100% header-based. `GIT_TRACE`/`GIT_TRACE_PACKET`/`GIT_TRACE_CURL`/
`GIT_CURL_VERBOSE` are explicitly cleared (set to the empty string, not just
left unset) in every subprocess's environment, because any of them would
print header values -- including this one -- into output this package does
capture and can return in an error. `openGitHubPR`/`openGitLabPR`/
`openGiteaPR` send the token only as a request header, never in a URL or a
JSON body field, so nothing server-echoed into a response or error body can
repeat it either.

Tested two ways, both without touching a real host (as required):

- `TestGitAuthEnv_CarriesTokenOnlyInEnvNotArgv` asserts the token appears in
  exactly the one expected `GIT_CONFIG_VALUE_0` entry and nowhere else in the
  returned environment slice.
- `TestCloneAndCommitAndPush_TokenNeverLeaks`/`TestClone_ErrorNeverLeaksToken`/
  `TestOpenPullRequest_ErrorNeverLeaksToken`/`TestSubmitPR_TargetNotFoundFailsLoudlyAndNeverOpensAPR`
  force real failures (a bad branch name, a nonexistent remote, a 422
  response, a CR absent from the repository) against a local bare repository
  (`git init --bare`, no network) and a `net/http/httptest` server, and grep
  every returned error string for the fake token, finding it nowhere.

This does not prove any real GitHub/GitLab/Gitea instance accepts this exact
Basic-auth-with-arbitrary-username convention -- that would need a live host,
which this unit's own tests are not allowed to touch. The username
(`x-access-token`) follows GitHub's own documented convention for a
token-as-password login and is used uniformly for all three hosts, since HTTP
Basic auth with a token as the password is the one git-smart-HTTP mechanism
all three document.

## Decision 5: `Diff` moved from `go-difflib` to `git diff --no-index`

ADR 0037's `Diff` used `github.com/pmezard/go-difflib`, promoted to a direct
dependency with zero non-test callers at the time -- caught by the
coordinator's own `go mod why` check before this unit's `SubmitPR` gave it
one. Since this unit already depends on the `git` binary for Clone/
CommitAndPush, `Diff` now shells out to `git diff --no-index` (which needs no
repository at all -- it compares two plain files) instead, removing the
library dependency entirely with no change to `Diff`'s own signature or any
caller.

## Decision 6: `SubmitPR` is the one orchestrator; `AuditRecord.actor` is a plain string parameter

`SubmitPR` (`submit.go`) is the single entry point stringing Clone -> Locate
-> Patch -> CommitAndPush -> Diff -> `OpenPullRequest` together -- the whole
"find the file via the Application's source ... open a PR" path, short of
ever merging it (no code path here calls a merge/accept endpoint on any
host). It only applies to `SourceFile` Applications; `SourceHelm`/
`SourcePlugin` have no single file to locate, so a caller falls back to
`Diff` alone for those (architecture.md's "diff to apply by hand"), not
`SubmitPR`.

"Who asked" (architecture.md: "a PR whose description includes ... who
asked") is `SubmitPRRequest.Requester`, a plain string -- this package never
reads `AuditRecord` or imports `internal/uiapi`. Today's actual value a
caller passes is the fixed `actor` constant `internal/uiapi/machine_service.go`
already defines (`"ichor-ui"`): there is no per-caller identity yet (OIDC
login is phase 9), so "who asked" is honestly the backend's own identity, not
a person, until that lands -- exactly as `actor`'s own existing doc comment
already says. `SubmitPRRequest.ImpactPreview` is likewise an opaque string
slot, included verbatim in the PR body when non-empty and never computed
here: the impact-preview backend does not exist yet (`rg -i impactpreview`
finds nothing), and is a different agent's unit.

## Consequences

- Not built here (explicitly out of scope): anything in `internal/cli` (this
  unit stops at `pkg/prmode`'s own package API, per instruction, to avoid
  colliding with concurrent `internal/cli` work); strict mode; override;
  the UI following a PR through merge/sync/apply; computing an impact
  preview.
- `pkg/prmode` now depends on the `git` binary being on `$PATH` at runtime
  (already true for `make`/CI via `flake.nix`), in addition to the Go
  dependencies ADR 0037 already introduced.
- `SubmitPR`'s shallow (`--depth 1 --single-branch`), temp-directory clone is
  deleted (`os.RemoveAll`) once the function returns, success or failure;
  nothing it creates persists past one call.

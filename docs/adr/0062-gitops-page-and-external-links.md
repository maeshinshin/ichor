# 0062. GitOps page and external tool links

Status: Superseded by ADR 0101 · Date: 2026-10-26 · Design section: CLI and UI
Decided: a new `GitOpsService` backs the GitOps page with three read-only RPCs: `ListGitOps` (every TalosCluster, TalosMachine and TalosConfigPatch the caller can list, with its tracking Application's `status.sync.status` and `status.health.status`, read with `get` only, no new RBAC), `FindPullRequests` (open pull requests of one CR, asked of the Git host only when a row's button is pressed, answered from a 30 s per-replica cache, one host call per verb the kind can propose) and `GetLinks`; external tool URLs are registered in the chart's values (`ichorUI.links.{argocd,grafana,hubble,longhorn}`, env `ICHOR_LINK_*`), not in a CRD spec, and a tool with no URL gets no link; links are http(s) only, checked in the backend and again in the UI with the function pull request URLs already use; Argo CD links go to the CR's Application, Grafana to the bundled dashboard's fixed uid, Hubble and Longhorn to their front page

## Context

architecture.md lists, among the UI screens, "a GitOps page showing each CR's Application, sync state and pending
PRs" and "deep links into Grafana, Hubble, Longhorn and Argo CD when their URLs are registered". What exists:
`gitopstracking.IsTracked`/`ArgoApplicationOf` (ADR 0044, 0056), `prmode.FindOpenPullRequest` (ADR 0056), and a
backend that reads as the signed-in user when OIDC is on (ADR 0058).

## Decisions

**Data.**

- *Application state.* `ListGitOps` lists the three CR kinds as the caller (so the page shows what the caller may
  see), asks `IsTracked` for each, and for those Argo CD tracks reads the one Application `ArgoApplicationOf`
  names with `get`. It never lists Applications, so ADR 0056's `get` rule is enough and no RBAC is added. One
  Application is read once per call however many CRs it tracks. Flux-tracked and untracked CRs are listed with
  who manages them and no state.
- *Whose permission reads the Application.* The caller's (strict: permissions are managed
  strictly; see Amendment below). Reading it with the ServiceAccount would show a user the sync and
  health of an Application they may not read.
- *Open pull requests.* Not on page load. Every open pull request lookup is a call to an external host
  (rate-limited, token-bearing), and a page open would make two per machine or patch. The row's "Check" button
  calls `FindPullRequests`, which reads the CR as the caller first, gets the plan from `planWrite` (the same
  decision that opens a PR, so the repository and base branch match), and looks for the heads Ichor would use
  (`ichor/<verb>-<name>`: `approve`/`reject` for a machine, `update`/`delete` for a patch). Answers are cached 30
  s per replica, keyed by host, repository and branches; an error is never cached and never read as "none"
  (ADR 0056). A CR that is written directly, or read-only because of Flux, answers with a note instead of calling
  the host. The token is read from the same Secret as PR creation and scrubbed from errors.
- *Not done.* A single "list all open `ichor/` pull requests of a repository" call would be cheaper than N
  lookups, but needs a new list function on all three hosts; it can replace the per-verb lookups later without
  changing the RPC. TalosStorage is not listed because no screen edits it yet. "Where Git, the cluster and the
  nodes disagree" is covered here only as far as Argo CD's `OutOfSync` goes; the node side is the drift state
  already on the node page.

**Where links are registered.** In the chart's values, as environment for ichor-ui. They are install facts (the
URL a browser reaches Grafana at), change rarely, belong to the deployment rather than to any one cluster, and a
GitOps-managed chart already carries them. A `TalosCluster.spec` field would be per cluster although the tools are
per installation, would put a UI preference in the API, and would touch the API types. Unset means no link, so the
default install shows none.

**Link shapes (per each tool's documentation).**

- *Argo CD*: the Argo CD docs show `<base>/applications/<name>` and say an Application in Argo CD's own namespace
  may omit the namespace, and that elsewhere applications are named `<namespace>/<name>`. The page writes the
  short form for namespace `argocd` and `/applications/<namespace>/<name>` otherwise. The latter path is how the
  UI addresses a namespaced Application but is not spelled out as a URL in the docs.
- *Grafana*: `<base>/d/<uid>` (Grafana serves the dashboard by uid; the slug is optional). The bundled dashboard's
  uid is `ichor-overview` (ADR 0040). It has no cluster or node variable (its series are the controller's and the
  backend's own), so there is nothing to pass; `var-<name>=` would only matter if a variable existed.
- *Hubble* and *Longhorn*: the front page only. Neither project's documentation gives a deep-link form, so none is invented.

**Safety.** A registered URL is shown to every browser, so `GetLinks` drops anything that is not an absolute
http(s) URL with a host and no `user:pass@`, and the UI applies `pullRequestHref` (the pull request notice's
rule) again. Links open with `rel="noreferrer"`. Nothing credential-bearing is in values or responses.

## Consequences

- The page costs a handful of API-server reads per load and no host calls.
- A user sees an Application's sync and health only if their own RBAC allows `get` on it (see Amendment).
- A Git host failure shows in the row that asked, as "Backend unavailable" (the status code OpenPR failures
  already use), and other rows are unaffected.

## Amendment (2026-10-26): reads are the caller's unless the result stays inside the backend

The rule: **a read whose result, or anything derived from it, is returned to a user is made as that user.** The
ServiceAccount (`s.Client`) is used only for a read that drives an internal decision, or whose content leaves only
masked or as a property of an object the caller already reads. With OIDC off there is no identity, `s.Get` falls
back to the ServiceAccount, and "anyone who reaches the UI may do anything" already holds (ADR 0058, point 4); that
stays, so nobody is locked out on upgrade.

A caller who may not `get` the tracking Application gets a `permission_denied` row (`GitOpsItem.permission_denied`):
no sync, no health, no error, and the UI shows "No permission" / "権限がありません" (`common.noPermission`). The other
rows render. `tracked_by` and the Application's name stay: both are read off the CR the caller already listed (its
tracking annotation or label); the ServiceAccount's `argocd-cm` and Flux inventory reads only decide how to read
those markers, and a Flux/Argo answer is the same gate that makes a write read-only, which the caller must be told.
Users need `get` on `applications.argoproj.io` in Argo CD's namespace through their own RBAC; the chart grants
nothing new, because the grant is the site's decision.

`FindPullRequests` already read the CR as the caller. The Git host is asked with the repository token, which is
the ServiceAccount's side and cannot be impersonated; the result is PR URLs of branches `ichor/<verb>-<name>` of a
CR the caller can read, in a repository that is the cluster's own. That alone is looser than the page: a caller
refused the Application's status would still see what is pending against it. So it also requires `get` on the
tracking Application as the caller (`requireApplicationRead`) and answers PermissionDenied otherwise, before any
host call. Untracked and Flux-tracked CRs have no Application and are unchanged.

### Audit of every `s.Client` read in `internal/uiapi`

| Read | Call site | Returned to the user? | Reader |
| --- | --- | --- | --- |
| Tracking Application `get` | `ListGitOps` | sync, health | **caller** (changed) |
| Tracking Application `get` | `FindPullRequests`, new `requireApplicationRead` | only allow or refuse | **caller** (new) |
| Kubernetes Node `get`/`list` | `findKubernetesNode`, `GetNode` | ready, kubelet version, schedulable | **caller** (changed); forbidden gives `kubernetes_permission_denied`, not "not joined" |
| `TalosStorage` list | `GetEffectiveConfig` (`ResolveSystemInputs` listed it as the ServiceAccount) | presets appear unmasked in the rendering | **caller** (changed); the ServiceAccount list inside `ResolveSystemInputs` is overwritten |
| Application `get` | `planEdit` (in `planWrite`) | no: decides whether a PR can be opened | ServiceAccount |
| `argocd-cm`, Flux inventory (`IsTracked`, `ArgoApplicationOf`) | `planWrite`, `ListGitOps`, `FindPullRequests` | `ListGitOps` returns only the tracker's name for a CR the caller listed | ServiceAccount |
| Secrets bundle (`loadBundle`) | `GetEffectiveConfig`, `NodeTalosClient` dialing | only as masked values (`RedactSecrets`); the client certificate never leaves | ServiceAccount |
| Registry credentials (`ResolveRegistryAuth`) | `GetEffectiveConfig` | only masked | ServiceAccount |
| Git token Secret | `gitToken` | no: used for the host call | ServiceAccount |

Also changed: `mapGetError` now maps a forbidden read to PermissionDenied instead of Internal, and a forbidden
`TalosStorage` list does the same. Not changed and noted: the Flux inventory scan inside `IsTracked` can tell a
caller "Flux" for a CR they list without their own right to read Flux inventories; it is the same fact the write
gate acts on (ADR 0044), so it is kept on the ServiceAccount.

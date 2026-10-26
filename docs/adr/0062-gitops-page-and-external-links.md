# 0062. GitOps page and external tool links

Status: Accepted · Date: 2026-10-26 · Design section: CLI and UI
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
- *Whose permission reads the Application.* The ServiceAccount's, and only the two status words and the
  Application's name leave the backend (not its source, repository or conditions). Reading it as the user would
  make the page empty for everyone not also granted `get` on Applications in Argo CD's namespace, which is a
  different grant from "may see this CR"; the CR itself is already gated by the caller's RBAC, and its
  Application's name is in the CR's own tracking annotation. If an Application cannot be read, that row carries
  the error and the rest of the page still renders.
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

**Link shapes (verified against each tool's documentation, 2026-10-26).**

- *Argo CD*: the Argo CD docs show `<base>/applications/<name>` and say an Application in Argo CD's own namespace
  may omit the namespace, and that elsewhere applications are named `<namespace>/<name>`. The page writes the
  short form for namespace `argocd` and `/applications/<namespace>/<name>` otherwise. The latter path is how the
  UI addresses a namespaced Application but is not spelled out as a URL in the docs.
- *Grafana*: `<base>/d/<uid>` (Grafana serves the dashboard by uid; the slug is optional). The bundled dashboard's
  uid is `ichor-overview` (ADR 0040). It has no cluster or node variable (its series are the controller's and the
  backend's own), so there is nothing to pass; `var-<name>=` would only matter if a variable existed.
- *Hubble* and *Longhorn*: the front page only. Neither project's documentation gives a deep-link form we could
  verify, so none is invented.

**Safety.** A registered URL is shown to every browser, so `GetLinks` drops anything that is not an absolute
http(s) URL with a host and no `user:pass@`, and the UI applies `pullRequestHref` (the pull request notice's
rule) again. Links open with `rel="noreferrer"`. Nothing credential-bearing is in values or responses.

## Consequences

- The page costs a handful of API-server reads per load and no host calls.
- Argo CD's Application list is readable through this page by anyone who may list the CR; that is the intended
  scope.
- A Git host failure shows in the row that asked, as "Backend unavailable" (the status code OpenPR failures
  already use), and other rows are unaffected.

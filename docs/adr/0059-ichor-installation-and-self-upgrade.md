# 0059. IchorInstallation holds the target version; a TalosOperation of kind ichor runs an installer Job that carries its own manifests, snapshots, and rolls back

Status: Accepted · Date: 2026-10-26 · Design section: Upgrades (Ichor), API
Decided: Add the cluster-scoped singleton `IchorInstallation` (managementMode, target version, installer repository; installed version reported by the controller), execute `kind: ichor` by creating a one-shot installer Job that runs the target version's own image and embedded manifest (snapshot in ConfigMaps, additive-only CRD check, CRDs then controller then UI with post-checks, rollback from the snapshot), refuse CLI writes more than one minor from the server, and never launch the Job in `external` mode

## Context

The design (Upgrades, Ichor) says: in `ichor` mode the controller only launches the installer Job for the
target version and never upgrades itself; the installer snapshots the current manifests, updates CRDs
(additive, never downgraded), the controller, then the UI, runs post-checks, and rolls back from the snapshot on
failure; in `external` mode it is a PR; the CLI refuses writes more than one minor from the server; the UI
backend sends its version in a header. ADR 0004 deferred `IchorInstallation` to this phase. ADR 0026 left
`kind: ichor` without an executor, ADR 0019 made `ichor init` apply a manifest embedded in the binary, and ADR
0056 wired PR mode into the UI backend.

## Decisions

**1. Shape, and who owns what.** `IchorInstallation` (cluster-scoped, must be named `ichor`, enforced by CEL):
`spec.managementMode` (`ichor` default, `external`), `spec.version` (semver with a leading `v`, required),
`spec.installerRepository` (default `ghcr.io/maeshinshin/ichor-installer`; point it at a mirror when air-gapped).
`status.installedVersion` is written by the controller from its own build version (`internal/version`, linked
with `-ldflags`) and nothing else writes it; conditions `UpToDate` and `ExternalSuggested` (the latter when
the mode is `ichor` but `pkg/gitopstracking.IsTracked` finds the controller's own Deployment tracked).
The `TalosOperation` of kind `ichor` carries **no version**: the target is `IchorInstallation.spec.version`,
read when the Job is created and recorded on the Job in an annotation. The CR says what should be installed and
who may change it (it is in Git, per the design); the operation is the record of one execution. A target
version in both places would be two places to disagree. Consequence: changing `spec.version` while an
operation runs is detected (the annotation differs) and pauses the operation instead of silently finishing the
old target.

**The backbone is not rebuilt.** ADR 0026 calls `AdvanceMachineFunc` once per machine and an Ichor upgrade has
no machines. The executor is therefore idempotent per operation: the Job is named from a hash of the
operation's namespace/name, the first call creates it, every later call (whichever machine, whichever
reconcile, including the new controller after the upgrade restarted this one) looks at the same Job. Machines
complete serially as soon as the Job has. `acquireDisruptiveLock` remains the only `Acquire` caller. Known
limit: a cluster with no approved machine completes the operation vacuously without launching anything; Ichor
manages Talos clusters, so this is the empty case, and the operation's cluster lock is held for the Job's whole
run, which is the point.

**2. Where the target's manifest comes from.** From the target version's own installer image. `ichor installer
run` (a hidden subcommand) applies `installmanifest.Manifest`, the same embedded bytes `ichor init` applies
(ADR 0019), so the image is built with `-tags installembed` (`Dockerfile.installer`, `make
docker-build-installer`) and **fetches nothing at run time**, which is what an air-gapped cluster needs. The
Job runs `<installerRepository>:<version>`. The controller never reads the new manifest; it cannot even tell
what it contains. The cost: one more image per release, and the manifest's own image references (controller,
UI) must be mirrored along with it (`ichor images list`, phase 10).

**3. Snapshot and rollback.** Before any write, the installer saves the live state of every object the
manifest names (and a marker for those that do not exist yet) as gzipped JSON, split into 512 KiB parts, in
ConfigMaps `<run id>-snapshot-N` in the Job's namespace, labelled with the run id. ConfigMaps, not Secrets:
the snapshot holds no Secret by construction (a Secret in the manifest is neither applied nor snapshotted; the
installer's RBAC has no `secrets` rule at all). 512 KiB leaves wide margin under the 1 MiB object limit;
the seven CRDs gzip to a few hundred KiB, so normally it is one part, and an incomplete set of parts is refused
at load rather than trusted. A snapshot that already exists for the run id is reused, never retaken: a re-run
after a crash would otherwise snapshot the half-upgraded cluster over the good one. Rollback runs in reverse
order with `Update` (an exact restore, unlike re-applying, which would leave behind fields the new version
added), deletes objects the upgrade created, and **never deletes a CRD** (that deletes every custom resource of
the kind); a CRD that was new stays, and a CRD that existed is restored to its old schema. Snapshots are kept
after success or failure (evidence, and the manual way back when the rollback itself failed); garbage
collection of old snapshots is not built.

Order and checks: CRDs (each must be `Established`), then every non-UI object (Deployments must finish a
rollout: generation observed, all replicas updated and available), then the UI objects (same). UI objects are
recognised by `ichor-ui` in the name, because kustomize's prefix makes them `ichor-ichor-ui-*`.

**4. CRDs are additive.** Before the snapshot, for each CRD in the manifest that exists, the installer compares
the schemas and refuses if the target would drop a served version, drop a property, change a property's type
(recursively through properties, items and additionalProperties), or change the scope. This judges the schemas, not
a version string, so it also catches a target built from an older tree and a hand-edited live CRD. The refusal
comes before any write and leaves no snapshot. The controller-side `semver` comparison in the executor
(`spec.version` older than the running controller is refused, creating no Job) is a second, earlier line.

**5. `external` mode.** The executor does not launch the Job: it returns an error naming the pull request path,
which pauses the operation (ADR 0026). **Not built: opening that PR.** PR submission (repository connection,
token, the Argo CD Application's source path) is wired inside `internal/uiapi` (ADR 0056), which another unit
was editing; reaching it from the CLI means either a UI service for `IchorInstallation` or an export from
`uiapi`, to be done after that unit lands. Until then `external` mode means "bump the chart version in Git by
hand". The chart's installer RBAC is also off in that mode (below).

**6. CLI refuses writes beyond one minor.** The CLI compares its own `internal/version` with
`IchorInstallation.status.installedVersion` (the controller's report, so it works with or without ichor-ui,
including the `--local` fallback) before a command marked as a write: `machine approve`, `machine reject`,
`patch create/update/delete`. Reads are never refused. It refuses only on a positive finding: no
`IchorInstallation`, no reported version, an unreadable cluster, a `dev` build or different majors mean "cannot
judge" and the command proceeds. Not marked yet: `init`, `adopt`, `restore`, `gitops` (they run before the
server exists or against its absence; revisit when `ichor self-upgrade` lands). `ichor init` now creates the
`IchorInstallation` with its own version (skipped for a `dev` build).

**7. UI response header: not started.** `internal/uiapi` and `ui/` were being changed by the OIDC unit. After it
lands: the backend sends `X-Ichor-Version` on every response from `internal/version`; the SPA compares it with
the version baked into its bundle and prompts a reload when they differ.

## The installer's RBAC, and how far it narrows

A separate ServiceAccount `ichor-installer` (`config/rbac/ichor-installer`, hand-written, compared with the
chart's copy by `dist/chart/chart_test.go`). The controller holds only `jobs: get, create` in its namespace; it
never holds the installer's permissions. The chart creates the identity only with `installer.enabled`
(default false: a chart-installed Ichor is normally Argo CD's, and nothing there should carry it).

Narrowed to exactly the kinds `dist/install.yaml` contains: CRDs `create`/`get` and `patch`/`update` limited by
`resourceNames` to Ichor's own seven; the `ichor-system` namespace `get`/`patch`/`update` by name;
Deployments, Services, ServiceAccounts, Roles and RoleBindings in the namespace only; ConfigMaps `create`/`get`/`list`
(the snapshots); ValidatingWebhookConfigurations and admission policies; no Secrets, no wildcards, no custom
resources, no CRD delete (a test fails the build if a `secrets` or `*` grant appears).

**Not narrowable: `clusterroles`/`clusterrolebindings` with `escalate` and `bind`** (and `roles` with the same in
the namespace). Applying a role that grants more than the applier holds is refused without `escalate`, and a
new version may add roles with names nobody can list in advance, so `resourceNames` cannot do it. This part is
as strong as cluster-admin. What contains it: the identity exists for one Job, only the controller can start
that Job, its image is the one `spec.installerRepository` names, and in `external` mode it does not exist.
Mirroring the installer image into a registry you control is what makes that last point hold. The installer
rewrites its own role during an upgrade (it is part of the manifest); a new version that narrows it takes effect
mid-run, which the rollback uses the old snapshot to undo.

## Built, and not

1. `IchorInstallation` CRD and reconciler: built. 2. `kind: ichor` executor and the Job: built. 3. The
installer (snapshot, additive check, ordered apply, post-checks, rollback), its image and RBAC: built, tested
against a real API server. 4. CLI skew refusal: built; UI header: **not started** (above). 5. `external`: no Job
launched, built and tested; opening the PR: **not started** (above).

Also not built: `ichor self-upgrade` (creates the operation and, in external mode, the PR) -- today the
operation is written by hand like any TalosOperation; `ichor self-upgrade --local` as the way out of a broken
controller; snapshot garbage collection; `ttlSecondsAfterFinished` on the Job (a finished Job and its snapshot
stay until deleted). Not verified: a real upgrade between two released versions, because there is no second
released version yet and no cluster to run it on.

## Amendment (real hardware)

The executor never created the installer Job: it reads Jobs through the manager's cached client, whose cluster-wide
informer needs `list`/`watch` on `jobs.batch` at cluster scope, while the Role grants `jobs` in `ichor-system` only
(`jobs.batch is forbidden ... at the cluster scope`). The Job cache is now confined to the controller's namespace like
the Secret cache, and the Role grants `get;list;watch;create` on `jobs`. A new envtest spec (ADR 0081, item 6) builds
the manager's cache as the controller's identity bound to the generated RBAC and requires an informer to sync for every
kind read through the cached client, so this class of mismatch fails in CI.

Addendum (ADR 0088): an Ichor upgrade whose renderer emits different output is applied to nodes like any desired-state change (render.Version is part of the input hash); reboot-requiring changes wait for a config rollout.

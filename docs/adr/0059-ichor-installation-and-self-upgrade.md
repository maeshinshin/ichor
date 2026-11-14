# 0059. IchorInstallation holds the target version; a TalosOperation of kind ichor runs an installer Job that carries its own manifests, snapshots, and rolls back

Status: Accepted; superseded in part by ADR 0090 (no air-gap requirement) and in part by ADR 0101 (decision 5 and the external mode) · Date: 2026-10-26 · Design section: Upgrades (Ichor), API
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
`spec.installerRepository` (default `ghcr.io/maeshinshin/ichor-installer`; point it at a mirror if needed).
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
docker-build-installer`) and **fetches nothing at run time**. The
Job runs `<installerRepository>:<version>`. The controller never reads the new manifest; it cannot even tell
what it contains. The cost: one more image per release, and the manifest's own image references (controller,
UI) are pulled as usual.

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
token, the Argo CD Application's source path) is wired inside `internal/uiapi` (ADR 0056); reaching it from the
CLI means either a UI service for `IchorInstallation` or an export from `uiapi`. Until then `external` mode means "bump the chart version in Git by
hand". The chart's installer RBAC is also off in that mode (below).

**6. CLI refuses writes beyond one minor.** The CLI compares its own `internal/version` with
`IchorInstallation.status.installedVersion` (the controller's report, so it works with or without ichor-ui,
including the `--local` fallback) before a command marked as a write: `approve node`, `reject node`,
`apply/delete patch`. Reads are never refused. It refuses only on a positive finding: no
`IchorInstallation`, no reported version, an unreadable cluster, a `dev` build or different majors mean "cannot
judge" and the command proceeds. Not marked yet: `init`, `adopt`, `restore`, `gitops` (they run before the
server exists or against its absence; revisit when `ichor self-upgrade` lands). `ichor init` now creates the
`IchorInstallation` with its own version (skipped for a `dev` build).

**7. UI response header: not built.** Nothing sends or reads it; the version badge (`ui/src/lib/ichorVersion.ts`)
is what exists. It was to work like this: the backend sends `X-Ichor-Version` on every response from `internal/version`; the SPA compares it with
the version baked into its bundle and prompts a reload when they differ.

## The installer's RBAC, and how far it narrows

A separate ServiceAccount `ichor-installer` (`config/rbac/ichor-installer`, hand-written, with an identical
chart copy). The controller holds only `jobs: get, create` in its namespace; it
never holds the installer's permissions. The chart creates the identity only with `installer.enabled`
(default false: a chart-installed Ichor is normally Argo CD's, and nothing there should carry it).

Narrowed to exactly the kinds `dist/install.yaml` contains: CRDs `create`/`get` and `patch`/`update` limited by
`resourceNames` to Ichor's own seven; the `ichor-system` namespace `get`/`patch`/`update` by name;
Deployments, Services, ServiceAccounts, Roles and RoleBindings in the namespace only; ConfigMaps `create`/`get`/`list`
(the snapshots); ValidatingWebhookConfigurations and admission policies; no Secrets, no wildcards, no custom
resources, no CRD delete.

**Not narrowable: `clusterroles`/`clusterrolebindings` with `escalate` and `bind`** (and `roles` with the same in
the namespace). Applying a role that grants more than the applier holds is refused without `escalate`, and a
new version may add roles with names nobody can list in advance, so `resourceNames` cannot do it. This part is
as strong as cluster-admin. What contains it: the identity exists for one Job, only the controller can start
that Job, its image is the one `spec.installerRepository` names, and in `external` mode it does not exist.
Mirroring the installer image into a registry you control is what makes that last point hold. The installer
rewrites its own role during an upgrade (it is part of the manifest); a new version that narrows it takes effect
mid-run, which the rollback uses the old snapshot to undo.

## Built, and not

(The `external` pull request, the version label, snapshot garbage collection and the Job TTL listed as not built
below were built later; see "Amendment (the remaining items, built)".)

1. `IchorInstallation` CRD and reconciler: built. 2. `kind: ichor` executor and the Job: built. 3. The
installer (snapshot, additive check, ordered apply, post-checks, rollback), its image and RBAC: built. 4. CLI skew refusal: built; UI header: **not started** (above). 5. `external`: no Job
launched, built; opening the PR: **not started** (above).

Also not built: `ichor self-upgrade` (creates the operation and, in external mode, the PR) -- today the
operation is written by hand like any TalosOperation; `ichor self-upgrade --local` as the way out of a broken
controller; snapshot garbage collection; `ttlSecondsAfterFinished` on the Job (a finished Job and its snapshot
stay until deleted).

## Amendment (the remaining items, built)

1. **Installer Job cleanup.** The Job carries `ttlSecondsAfterFinished: 86400` (one day: long enough to read the
   pod log of a failed upgrade, short enough that Jobs do not pile up). The operation does not need the Job once
   it has finished: after a successful upgrade the new controller's version equals `spec.version`, which the
   executor checks before it looks for a Job (decision 1, "Done must be decided before the mode"). An expired
   *failed* Job that is resumed after more than a day starts a fresh attempt under the same run id, which reuses
   the run's snapshot (decision 3), so it is a retry and never a second snapshot of a half-upgraded cluster.
2. **Snapshot garbage collection.** Each snapshot ConfigMap carries a `...-taken` annotation (RFC 3339, nanoseconds;
   `creationTimestamp` has one-second resolution and runs can share a second). After the installer has saved
   (or reused) its snapshot, and before it writes anything, it deletes every snapshot except the newest
   `SnapshotsKept` (3, the current one included) and never the current run's. Failing to delete is logged and does
   not fail the upgrade. The installer's Role gains `delete` on ConfigMaps (still namespace-scoped). The decision above that snapshots are kept after success or
   failure still holds, now for the last three.
3. **Version in the UI.** `OperationService.GetIchorInstallation` returns the installed version
   (`status.installedVersion`), `spec.version`, the mode, whether an `ichor` operation is unfinished, and
   `update_available`: `spec.version` is semver-newer than the installed version and no Ichor upgrade is running. A
   cluster without an `IchorInstallation` answers with empty fields, not an error. The app shell shows the
   installed version as a badge in the top bar, plus an "Update available" badge only when the server says so; a
   failed read shows nothing. The `X-Ichor-Version` response header (decision 7, a reload prompt when the bundle
   and backend differ) is still not built (decision 7 says so too); this label (`ui/src/lib/ichorVersion.ts`) is
   separate from it.
4. **`external` mode and Git: a pull request.** What the PR changes is the one thing prmode can edit safely:
   the `IchorInstallation` document in the file the tracking Argo CD Application's plain path source holds,
   field `spec.version` (target `IchorInstallation/ichor`, branch `ichor/upgrade-ichor-<version>`, so asking
   twice finds the first PR). It goes through the same gate as every other write (`planWriteIn`: Argo CD tracks
   it, the cluster's `gitops.repository` is the Application's repository, the source is a plain path). When the
   `CreateOperation`/`PlanOperation` target would change a tracked `IchorInstallation` and a plan exists, the
   backend opens the PR and returns `pull_request_url` with **no operation** (`opens_pull_request` in the plan, so
   the CLI and UI say so before they act); nothing is written to the cluster. When no PR can be opened the old
   refusal stands. In `ichor` mode the next step after merging is to start the upgrade again without a target (the
   installer Job runs). In `external` mode no Job ever runs, so the PR records the target only: the chart or
   manifests Argo CD applies for Ichor (a Helm source, which prmode cannot edit) must be bumped by hand, and the PR
   description says so. That is the conservative reading of "bump the chart version in Git"; editing a Helm
   `targetRevision` or values file is not attempted. An `external` installation that Argo CD does not track is
   refused by the plan (it can neither be written nor proposed). The executor in `external` mode still
   launches no Job and pauses, unchanged.

## Amendment (the Job cache)

The executor reads Jobs through the manager's cached client, whose cluster-wide informer needs `list`/`watch` on
`jobs.batch` at cluster scope, while the Role grants `jobs` in `ichor-system` only. The Job cache is therefore
confined to the controller's namespace like the Secret cache, and the Role grants `get;list;watch;create` on
`jobs`. Every kind read through the cached client must have an informer that can sync under the controller's
generated RBAC (ADR 0081, item 6).

Addendum (ADR 0088): an Ichor upgrade whose renderer emits different output is applied to nodes like any desired-state change (render.Version is part of the input hash); reboot-requiring changes wait for a config rollout.

## Amendment (self-upgrade from an older release)

An upgrade runs the installer Job with the RBAC of the release being replaced, so a rule the new release adds helps only the release after next. A right the new release needs, such as patching a CRD it adds (missing from the ClusterRole resourceNames) or deleting snapshot ConfigMaps, would otherwise fail the upgrade and roll it back.

- The installer applies its own RBAC first. The ClusterRole, ClusterRoleBinding, Role and RoleBinding named `ichor-installer-*` form a step before the CRDs, in the same run. It may already `escalate`/`bind` and patch roles, so it grants itself the new rights, then applies everything else. The snapshot covers these objects too (first entry, so a rollback restores them last and still holds the rights it needs). Kubernetes evaluates RBAC per request and the installer client caches no authorization state, so no restart is needed; applies retry briefly on Forbidden (default 30s) because the authorizer cache can lag the write.
- Snapshot garbage collection now runs after the RBAC step and stays non-fatal. Its `delete` on ConfigMaps is namespace-wide in the release namespace: snapshot names derive from the generated Job name, so `resourceNames` cannot list them.
- Every CRD in `config/crd/bases` must be in the installer ClusterRole `patch`/`update` resourceNames. This holds in both the kustomize role and the chart template.

Amendment: a rolled-back upgrade leaves `spec.version` at the failed target, so "current" in plans and the CLI is `status.installedVersion` (the running controller's own version), never `spec.version`; `UpToDate=False` states running versus target.

## Amendment (an upgrade keeps what `ichor init` customized)

An upgrade must keep what `ichor init` customized (for example `--ui-url` and `--ui-tls-cert/key`). `init` edits the manifest's Deployments and Service after installing them (users-enabled and issuer flags, the internal port and OIDC Secret volumes, the TLS Secret mount and flags, HTTPS probes, the controller's `--user-issuer`). The installer applied the bare manifest by server-side apply under its own field manager, so: container `args` (an atomic list) were replaced, silently dropping every init flag (users and TLS off, and the controller's issuer off); `ports`, `volumes`, `volumeMounts` and Service `ports` (merge lists owned by init's manager) survived by luck; probes merged field by field, leaving init's `httpGet` beside the manifest's `tcpSocket`, which the API server rejects (`may not specify more than 1 handler type`).

- The customizations are one set of pure functions in `internal/installer` (`CustomizeUI`, `CustomizeController`, `CustomizeService`, over `UISettings`). `ichor init` calls them on the live objects; the installer calls them on the new manifest's objects before it classifies, snapshots or applies anything. They replace whole probes and are idempotent, so there is nothing to drift between the two.
- The settings are not stored anywhere new: the installer re-derives them on every run from the live Deployments (`ReadUISettings`): `--external-url`, `--kube-api-server-url`, `--kube-api-tls-server-name` and the `tls` volume's Secret on ichor-ui, `--oidc-refresh-lifetime` on the controller. Those are what init wrote and what an operator would see. A first install by the installer reads as a plain install.
- Deployments are written by a full `Update` (with the live resourceVersion and annotations carried over, field manager `ichor-installer`) instead of server-side apply, so a field another manager owns can no longer merge into the new spec. The Service and everything else keep server-side apply. The rollback snapshot is untouched: it records the live object before the run and writes it back whole.
- A rolled-back Ichor upgrade leaves a Paused `TalosOperation` with a failure reason and `spec.version` at the failed target. A new `ichor upgrade ichor` is refused while it exists (one unfinished operation per cluster); resuming it only meets the failed installer Job again, which is kept for a day. The refusal now says to run `ichor delete operation NAME` and then `ichor upgrade ichor <version>`; after that, any version at or above the running one is accepted, the failed one included.

## Amendment (the installer snapshots its own RBAC first, then everything else)

- Problem: the snapshot read every object of the new manifest before the installer applied its own RBAC. An older release's `ichor-installer-role` has no rule for a kind that is new in the release (a CiliumNetworkPolicy), so the snapshot failed Forbidden before anything was applied and every new kind blocked every upgrade from an older release.
- Order now: (1) snapshot stage 0, only the `ichor-installer-*` ClusterRole, ClusterRoleBinding, Role and RoleBinding; (2) apply them; (3) poll a SelfSubjectAccessReview for the first rule of each applied role (verb `get` when the rule has it) until allowed, bounded by `RBACSettle` (default 30s), because RBAC is eventually consistent; (4) snapshot stage 1, everything else (plus the Cilium policies); (5) CRDs, controller, UI, network policies as before. Both stages carry one run id (a stage annotation on the ConfigMaps); a rollback reads them as one list and restores stage 1 first, the RBAC last, so it still holds the rights it needs. A re-run keeps a stage that already exists.
- (Corrected by "Amendment (installer rights are cluster-scoped only)" below: this holds for the installer's own ClusterRole and the Role in the release namespace, not for a new namespaced Role elsewhere.) No bridge is needed for those. Every release since the installer role was introduced (ffb1413) grants the role get/create/patch/update/delete on `clusterroles`, `clusterrolebindings`, `roles` and `rolebindings` plus `escalate` and `bind` on `clusterroles` and `roles`, so the oldest role can rewrite itself without the user's rights. The CLI does not pre-apply anything.
- The snapshot treats NotFound, and a kind whose CRD is not installed (no REST mapping), as "did not exist": nothing to restore, and the rollback skips it. Forbidden or any other error fails the run before it writes, never ignored, because the rollback depends on the snapshot being complete.

## Amendment (installer rights are cluster-scoped only)

- Problem: an upgrade from an older release fails at the stage-0 snapshot with `roles.rbac.authorization.k8s.io "ichor-installer-kube-system-role" is forbidden ... in the namespace "kube-system"`. Commit 8a7199b moved the Cilium Job to kube-system and gave the installer a namespaced Role there. The old installer role holds no rights in kube-system, so it can neither read nor create that Role, and "RBAC first" cannot bootstrap a namespaced Role in a namespace the old role never reached. The "no bridge is needed" claim above is wrong for any new namespaced RBAC outside the release namespace.
- Rule: installer rights are cluster-scoped only. The installer holds no namespaced Role outside the release namespace. What `installer-kube-system-role` granted (serviceaccounts, roles and rolebindings: create/delete/get/patch/update, and bind/escalate on roles) is folded into `installer-role`'s ClusterRole, which every release's installer can rewrite: since ffb1413 the role holds get/create/patch/update on clusterroles and clusterrolebindings plus bind and escalate on clusterroles. `installer-kube-system-role`, its RoleBinding, their chart copies and the kustomize patches are removed. The controller's `cni-job-role` in kube-system stays; the installer creates it, and the folded rules (with escalate and bind on roles) let it.
- Order: stage 0 is now the installer's ClusterRoles and ClusterRoleBindings only (snapshot, apply, wait until effective); stage 1 is its namespaced Roles and RoleBindings (snapshot, apply, wait); stage 2 is everything else. A future namespaced installer Role elsewhere is read under the already-widened ClusterRole. Rollback restores stage 2, then 1, then 0.
- Cost: the installer's serviceaccount, role and rolebinding rights now hold in every namespace, not only kube-system. The ClusterRole already held escalate and bind on clusterroles, so it was already as strong as cluster-admin (see "The installer's RBAC").
- Amendment (ADR 0113): an Ichor upgrade does not move the cluster's Talos, Kubernetes or Cilium versions. Each Ichor build embeds its own release set, and the cluster follows it through `ichor upgrade cluster`, one step at a time, after the Ichor upgrade has finished.

## Amendment (the operation completes only when the rollout is over)

- Problem: `ichor upgrade ichor --follow` reported the operation Completed while the installer Job was still Running and ichor-ui rolled out 10 to 40 seconds later. The new controller resumes the operation as soon as it runs, and the executor returned done as soon as `spec.version` equalled the running version, without looking at the Job.
- Rule: an `ichor` operation is Completed only when (1) the installer Job has succeeded (or is gone: it is removed after a day), (2) the controller Deployment (`control-plane=controller-manager`) and (3) the ichor-ui Deployment (`app.kubernetes.io/component=ui`) have finished rolling out: `observedGeneration` is current and the updated, ready and available replica counts equal the desired count with no old replica left. A failed Job fails the operation with the Job's message, even when the new controller already runs. A Deployment that is not installed is not waited for. The check is the same whichever controller runs it (the old one after the Job succeeds, the new one on resume).
- The Deployments are read through the uncached API reader (`get` and `list` in the release namespace, already granted), not the cached client: a cached read would start a cluster-wide informer (ADR 0081). The CLI `--follow` only polls the operation phase, so it needs no change.

## Amendment (an operation shows what it is waiting for)

- Gap: the executor's waiting text ("the installer Job is running", "waiting for the ichor-ui Deployment to finish rolling out (n of m updated replicas are available)") was built on every reconcile and thrown away, so nobody could see what a running operation waited for.
- Rule: `TalosOperation.status.message` (optional, appended) holds the message the controller last passed to `setPhase` while the phase is InProgress. It is replaced when it changes (an unchanged value is a no-op status update) and cleared in every other phase: a pause says why in `failureReason` or the Paused condition, a finished operation in its Events. It applies to every kind that goes through `setPhase`, not only `ichor`.
- Shown by `Operation.message` (proto field 21, `GetOperation` and the list), by `ichor ... --follow` and `describe operation --watch` (the progress line ends in `: <message>`, printed whenever the line changes), and by the "Currently" field on the operation detail page. The operations list is a table and does not show it.

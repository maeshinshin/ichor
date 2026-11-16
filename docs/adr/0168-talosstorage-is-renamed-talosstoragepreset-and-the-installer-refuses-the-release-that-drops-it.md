# 0168. `TalosStorage` is renamed `TalosStoragePreset`, and the installer refuses the release that drops the old kind while any object of it remains

Status: Accepted · Date: 2026-11-17 · Design section: Storage · Supersedes in part: 0023

Decided: `TalosStorage` is renamed `TalosStoragePreset` in one release, with no migration command and no dual read; `internal/installer` refuses that release while the old CRD still holds any object, and the operator recreates such objects by hand as `TalosStoragePreset`

## Context

ADR 0023 named the layer-1 kind `TalosStorage`, and ADR 0025 and 0050 built on that name. The name says more than the kind holds: `TalosStorage` reads as the storage system, while the object only selects a Talos disk preset for a set of nodes. The storage system itself is the operator's (ADR 0023, ADR 0167), so the name invites the wrong expectation. The maintainer has decided to rename the kind.

The repository has shipped this kind. `TalosStorage` is in the tagged releases `v0.1.2`, `v0.2.3` and `v0.2.4` (`api/v1alpha1/talosstorage_types.go` exists at each tag), so an installation may hold objects of it. The only mechanism for retiring a shipped thing is the one ADR 0101 built for fields, and it does not cover a kind. This ADR decides the mechanism as well as the name.

### How the installer treats a kind that leaves the bundle

- `internal/installer/install.go` `checkCRDs` loops over the CRDs of the target bundle only: `for _, t := range target {`, with `target` the `p.crds` passed in from `install.go:123`. A CRD absent from the bundle is never compared and is never refused.
- `internal/installer/prune.go` deletes only kinds in `prunableKinds`. CustomResourceDefinitions are left out with this comment: `Left out on purpose: CustomResourceDefinitions (deleting one deletes every custom resource of its kind)`.
- The installer therefore never removes an old CRD. Dropping a kind from the bundle leaves the old CRD and its objects on the cluster, and nothing reconciles them.
- `internal/installer/crdcheck.go` `retiredFields` is a list of `{crd, path}` entries: fields, not kinds. ADR 0101 calls it `a deliberate exception to the append-only rule`, with one ADR per removal.

### What a stranded object does

The layer-1 render reads the kind. `pkg/render/system.go` `StorageFor` lists `TalosStorage` objects and `injectStorage` returns the configuration unchanged when none governs the machine: `if err != nil || chosen == nil {` then `return cfg, err`. The machine controller fingerprints the storage input (`desiredInputsHash fingerprints the generation of every object that fed this reconcile's render`). An object that stops being read therefore changes the render, and the volume document and kubelet mount it produced are dropped from a node's config. That is a node-affecting change, not an orphaned record. A release that drops the kind must not run on a cluster where such an object exists.

### What Helm does (CRD in `templates/`)

This chart ships its CRDs as templates: `dist/chart/templates/crd/talosstorages.ichor.maesh.dev.yaml`. `dist/chart/values.yaml` sets:

```yaml
crd:
  # Install CRDs with the chart
  enabled: true
  # Keep CRDs when uninstalling
  keep: true
```

and the CRD template adds `"helm.sh/resource-policy": keep` under `{{- if .Values.crd.keep }}`.

Helm v3 (`helm.sh/helm/v3 v3.21.4` in `go.mod`) documents the annotation as follows, from the Helm website (`docs/howto/charts_tips_and_tricks.md`): `The annotation helm.sh/resource-policy: keep instructs Helm to skip deleting this resource when a helm operation (such as helm uninstall, helm upgrade or helm rollback) would result in its deletion. However, this resource becomes orphaned. Helm will no longer manage it in any way.`

Helm's `CRDs are never deleted` rule (`docs/topics/charts.mdx`, "Limitations on CRDs") is stated for CRDs in the `crds/` directory. This chart does not use that directory.

Helm's documentation for hooks says of a Job hook: `If they are Job or Pod kind, Helm will wait until it successfully runs to completion. And if the hook fails, the release will fail.`

**Unestablished:** what Helm does on `helm upgrade` when a CRD template is removed from the chart while its previous manifest carried `keep`. The keep sentence above says the resource is skipped and becomes orphaned, but the documentation retrieved does not state that case.

**Inferred, not established:** that a `pre-upgrade` hook runs before the chart's resources are loaded on upgrade. This is the install lifecycle's order carried over to upgrade; it is not separately established here. The installer's refusal does not depend on it (Decision, step 3). The chart's guard does.

## Decision

### 1. The name

The kind is `TalosStoragePreset`. Its CRD is `talosstoragepresets.ichor.maesh.dev`, short name to be chosen when the kind is written, and not `tsto`, which the old kind holds until it leaves.

The existing kinds, from the ten `config/crd/bases/ichor.maesh.dev_*.yaml` files: `IchorInstallation`, `IchorUser`, `MachineReclaim`, `TalosCluster`, `TalosMachine`, `TalosConfigPatch`, `TalosOperation`, `TalosEtcdBackup`, `TalosCredentialRequest`, `TalosStorage`. Seven of the ten are `Talos` followed by a noun. The three without the prefix (`IchorInstallation`, `IchorUser`, `MachineReclaim`) are Ichor's own objects, not Talos ones. So the name fits the convention for Talos-side kinds. A bare `Preset` would not: none of the ten is a bare noun, and `Preset` is already a Go type name in `pkg/storage` and `pkg/cni` (ADR 0013).

The object's central field is `spec.diskPreset`. The other fields (`nodes`, `diskSelector`, `evacuationTimeout`) are parameters of that preset for one set of nodes. The object therefore selects a preset for a set of nodes; it does not define one. The name says what the object is about without naming more than it holds. `TalosNodeStorage` was the alternative considered: it says which nodes and not which storage preparation, so it was not chosen.

### 2. The mechanism: one release, enforced by the installer

**The release.**

- `TalosStorage` is removed from `api/`, the CRD bases, the chart's CRD and RBAC templates, the controller, the render, the UI backend and the docs. The render, `storage.Select`, the status controller and the UI read `TalosStoragePreset` only.
- `TalosStoragePreset` takes the same spec and the same validation as `TalosStorage` had, including the enum and object-level rule of ADR 0167 once that lands (Decision 5).
- `internal/installer/crdcheck.go` gets a `retiredKinds` list beside `retiredFields`, one entry per removal, each citing this ADR. The entry for this removal is `{crd: talosstorages.ichor.maesh.dev}`.

**The refusal.** `internal/installer/install.go` gets a check beside `checkCRDs`, run before any object is applied. For each entry of `retiredKinds` that the target bundle does not ship, the installer lists the objects of that CRD in every namespace. If the count is at least one, the install stops with an error that says why: the count, the namespaces, that this release does not read them and would drop their layer-1 configuration from the nodes they select, and the steps to take. It refuses on one object. It does not warn.

**The steps the refusal names.** The operator recreates each object as a `TalosStoragePreset` with the same name, namespace and spec, confirms the node configuration is what they expect, deletes the `TalosStorage`, and reruns the install. The installer does not do this itself. Nothing in this release copies or reads the old kind.

**After the release.**

- With no objects, the upgrade proceeds normally.
- The installer never deletes the CRD (the prune exclusion quoted above). The empty CRD is left in place, and the operator removes it with `kubectl delete crd talosstorages.ichor.maesh.dev`.
- The installer role needs a rule to list `talosstorages` objects. `config/rbac/ichor-installer/role.yaml` names `talosstorages.ichor.maesh.dev` only as a CRD resource name (line 36). The list rule is new in this release.
- The three ClusterRoles `ichor-talosstorage-{admin,editor,viewer}-role` (named in `config/rbac/ichor-installer/role.yaml` lines 91 to 93) are removed from the bundle. The installer prunes them only if they are in the inventory the last run wrote. A build from before the inventory has no such record, so each needs an entry in `renamedObjects` (`internal/installer/prune.go`, which today names only the break-glass ClusterRole).

**Helm-installed clusters.** The installer's refusal does not run on a `helm upgrade`. For those clusters the release adds a `pre-upgrade` hook Job to the chart, using the controller image, that lists `talosstorages` and exits non-zero when any object exists. Its ServiceAccount needs the same list rule. The hook's ordering on upgrade is inferred (Context). If the inference is wrong, this guard is the one that fails, and nothing else in the release depends on it. It is the only guard for Helm installs.

### 3. Alternatives and what each costs

| Option | Cost | Outcome |
|---|---|---|
| a. Remove the kind and record it in `retiredKinds` as a warning | Small Go change | Rejected. An unread object changes the render, and the next render drops layer 1 for the nodes it selected. A warning does not stop that. |
| b. Keep the old CRD in the bundle, deprecated, and refuse create and update with a ValidatingAdmissionPolicy | Permanent second kind in every bundle; the admission policy is new | Rejected. Ownership is kept, but the old objects still drive layer 1 unless the render keeps reading them. The cost never ends. |
| c. Remove the kind with nothing and document that the CRD and objects are left behind | None | Rejected. The same stranded objects and silent drop as (a), with no warning at all. |
| d. This record: remove the kind in one release, and refuse that release while any object exists | One installer check and one list rule; a `pre-upgrade` hook for Helm installs; the operator recreates objects by hand if any exist | Chosen. Nothing is lost and no node's config changes without the operator's action. |
| e. Migration over two releases: a dual read in render, `storage.Select` and the UI for one release, and `ichor migrate storage-preset` to copy the objects | Two releases; a CLI command; dual-read code in three places; the old Go types live a release longer | Considered and not taken. See below. |

**Why option e was considered and not taken.** Option e protects a population this repository has no evidence of. The maintainer's cluster held no `TalosStorage` objects when this record was written. The tagged releases mean an outside installation could hold some, but none has been seen. The cost of e is paid on every installation, to insure against those that may not exist. Option d keeps the same safety on the installer path: an installation that holds objects is stopped and told before its nodes change, not after. If an installation ever hits the refusal, the manual recreate is the path, or a migration is written then, against a real case. Re-open this decision if a real installation with objects appears.

Variant of e, also not taken: a controller that refuses to render a machine whose old object has no copy. It keeps the data but stalls the node's reconcile until someone runs a command, which is worse than either e or d.

### 4. The rename lands after the in-flight work

The rename lands **after** the `rook-v1` preset and the UI page for storage presets. Both are in flight against the current kind name (Decision 6), and the rename would conflict with both. The implementation is dispatched by the maintainer's lead once those land.

### 5. The `diskPreset` enum and the object-level rule from ADR 0167

ADR 0167 is on branch `docs/rook-ceph-is-the-storage-ichor-prepares-for`, file `docs/adr/0167-rook-ceph-is-a-second-preset-that-renders-no-volume-and-has-no-evacuation-hook.md`, and has not yet landed on `origin/main`. Its decisions 3 and 6 add `rook-v1` to the `diskPreset` enum and replace the field-level `self != ''` on `diskSelector` with an object-level rule on the spec: `self.diskPreset == 'rook-v1' || self.diskSelector != ''`, message `diskSelector is required for diskPreset longhorn-v1`.

Both carry over unchanged to `TalosStoragePreset`. The enum is a property of the field, not of the kind. The rule reads `self.diskPreset` and `self.diskSelector` on the spec, not the kind's name. The new kind's schema is generated from the same types, so the rule and the enum are in the new CRD as written.

### 6. Order against the in-flight work

The rename lands after both of these, because each would conflict with it:

- `feat/the-rook-ceph-storage-preset` (origin head `4407bae06`, the head at the time this record was written), which touches `api/v1alpha1/talosstorage_types.go`, `api/v1alpha1/validation_test.go`, `config/crd/bases/ichor.maesh.dev_talosstorages.yaml`, `dist/chart/templates/crd/talosstorages.ichor.maesh.dev.yaml`, `pkg/storage/rookv1.go` and its test, `pkg/storage/storage.go`, `pkg/storage/storage_test.go`, `pkg/storage/evacuation_test.go`.
- `feat/the-ui-shows-the-storage-presets` (origin head `ac0110677`), which adds `proto/ichor/v1alpha1/storage.proto`, `internal/uiapi/storage_service.go`, `ui/src/pages/StorageList.tsx` and routes, and names the current kind in its backend.

### 7. ADR number

The next free number on `origin/main` at the time this record was written is `0167`: the highest number on origin/main is `0166`, and `0160` is absent there (it exists only on the local branch `docs/adr-storage-kernel-modules`). `0167` is taken on `docs/rook-ceph-is-the-storage-ichor-prepares-for`, which is landing. This record takes `0168`. Recheck the number at land time, after rebasing onto `origin/main`.

### 8. Supersession

This record supersedes part of ADR 0023. Its header records that as `Supersedes in part: 0023`, as ADR 0099 records its supersessions. ADR 0023's Decision line is not edited. ADR 0023's `Status:` line carries the reverse marker, as ADR 0013 and ADR 0099 carry theirs. Once ADR 0167 lands, that line reads `superseded in part by ADR 0167 and ADR 0168`.

## Consequences

- One release removes the kind. Nothing reads `TalosStorage` after it, and no migration code is written.
- An installation that holds `TalosStorage` objects is stopped before any node changes, and told what to do. The operator recreates the objects by hand. This is slower for that installation than a command would be.
- The old CRD is left in place after the release, and the operator removes it by hand. The installer does not delete CRDs, and this record does not change that.
- The installer gains a `retiredKinds` list, a refusal check, a list rule for the old kind's objects, and `renamedObjects` entries for the three old ClusterRoles. Each is new installer code, written with its own tests in its own change.
- The Helm guard depends on the `pre-upgrade` hook behaving as the install lifecycle describes. That ordering is inferred, not established (Context). The installer path does not depend on it.

## Open

- What Helm does on upgrade when a keep-annotated CRD template is removed from the chart. Not established; this record does not rely on it.
- Whether the `pre-upgrade` hook runs before resources are loaded on upgrade. Inferred from the install lifecycle; not established.
- The short name for `TalosStoragePreset`, which must not collide with `tsto` while the old kind is served.
- Whether the installer should refuse a removed enum value or a relaxed rule, which ADR 0167 also leaves open.
- Whether the proto message `TalosStorageSection` should be renamed for consistency. Left unchanged here.
- Whether an installation that does hit the refusal will need the migration command after all. Decided against now; revisit with a real case.

# 0168. `TalosStorage` is renamed `TalosStoragePreset`, and a shipped kind leaves through a migration the installer enforces

Status: Proposed · Date: 2026-11-17 · Design section: Storage · Supersedes in part: 0023

Decided: `TalosStorage` is renamed `TalosStoragePreset`; the old kind is served beside it for one release, `ichor migrate storage-preset` copies its objects across, and the release that drops it is refused while any old object remains

## Context

ADR 0023 named the layer-1 kind `TalosStorage`, and ADR 0025 and 0050 built on that name. The name says more than the kind holds: `TalosStorage` reads as the storage system, while the object only selects a Talos disk preset for a set of nodes. The storage system itself is the operator's (ADR 0023, ADR 0167), so the name invites the wrong expectation. The maintainer has decided to rename the kind.

The repository has shipped this kind. `TalosStorage` is in the tagged releases `v0.1.2`, `v0.2.3` and `v0.2.4` (`api/v1alpha1/talosstorage_types.go` exists at each tag), so an installation may hold objects of it. The only mechanism for retiring a shipped thing is the one ADR 0101 built for fields, and it does not cover a kind. This ADR decides the mechanism as well as the name.

### How the installer treats a kind that leaves the bundle

- `internal/installer/install.go` `checkCRDs` loops over the CRDs of the target bundle only: `for _, t := range target {`, with `target` the `p.crds` passed in from `install.go:123`. A CRD absent from the bundle is never compared and is never refused.
- `internal/installer/prune.go` deletes only kinds in `prunableKinds`. CustomResourceDefinitions are left out with this comment: `Left out on purpose: CustomResourceDefinitions (deleting one deletes every custom resource of its kind)`.
- The installer therefore never removes an old CRD. Dropping a kind from the bundle leaves the old CRD and its objects on the cluster, and nothing reconciles them.
- `internal/installer/crdcheck.go` `retiredFields` is a list of `{crd, path}` entries: fields, not kinds. ADR 0101 calls it `a deliberate exception to the append-only rule`, with one ADR per removal.

### What a stranded object does

The layer-1 render reads the kind. `pkg/render/system.go` `StorageFor` lists `TalosStorage` objects and `injectStorage` returns the configuration unchanged when none governs the machine: `if err != nil || chosen == nil {` then `return cfg, err`. The machine controller fingerprints the storage input (`desiredInputsHash fingerprints the generation of every object that fed this reconcile's render`). An object that stops being read therefore changes the render, and the volume document and kubelet mount it produced are dropped from a node's config. That is a node-affecting change, not an orphaned record.

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

**Unestablished:** what Helm does on `helm upgrade` when a CRD template is removed from the chart while its previous manifest carried `keep`. The keep sentence above says the resource is skipped and becomes orphaned, but the documentation retrieved does not state that case. This ADR does not rely on it. It treats a Helm-installed cluster as needing its own guard (Decision, step 4).

## Decision

### 1. The name

The kind is `TalosStoragePreset`. Its CRD is `talosstoragepresets.ichor.maesh.dev`, short name to be chosen when the kind is written, and not `tsto`, which the old kind holds until it leaves.

The existing kinds, from the ten `config/crd/bases/ichor.maesh.dev_*.yaml` files: `IchorInstallation`, `IchorUser`, `MachineReclaim`, `TalosCluster`, `TalosMachine`, `TalosConfigPatch`, `TalosOperation`, `TalosEtcdBackup`, `TalosCredentialRequest`, `TalosStorage`. Seven of the ten are `Talos` followed by a noun. The three without the prefix (`IchorInstallation`, `IchorUser`, `MachineReclaim`) are Ichor's own objects, not Talos ones. So the name fits the convention for Talos-side kinds. A bare `Preset` would not: none of the ten is a bare noun, and `Preset` is already a Go type name in `pkg/storage` and `pkg/cni`.

The object's central field is `spec.diskPreset`, and the other fields (`nodes`, `diskSelector`, `evacuationTimeout`) are parameters of that preset for one set of nodes. The name therefore says what the object is about without naming more than it holds. `TalosNodeStorage` was the alternative considered: it says which nodes and not which storage preparation, so it was not chosen.

### 2. The mechanism: a migration over two releases, enforced by the installer

**Release R (the rename lands).**

- `TalosStoragePreset` is served and is the kind that new objects use.
- `TalosStorage` stays served, with its schema unchanged. Nothing about it is deleted. Its CRD description says it is replaced by `TalosStoragePreset`; that text is the only deprecation marker a CRD carries here.
- The render and `storage.Select` read both kinds. Where a `TalosStoragePreset` and a `TalosStorage` share a namespace and name, the `TalosStoragePreset` is the one used, and the pair is treated as one object. A `TalosStorage` with no same-named preset renders exactly as it does today. The upgrade therefore changes no node's config.
- `ichor migrate storage-preset` (a CLI command, which may write custom resources) creates a `TalosStoragePreset` for each `TalosStorage`, with the same namespace, name and spec. It is create-only and idempotent. It never modifies or deletes a `TalosStorage`. If a same-named preset already exists with a different spec, it reports the conflict and stops for that object. The controller does not migrate on its own: creating objects is the operator's act.
- The status controller (`internal/controller/talosstorage_controller.go`, renamed with the kind) writes `Ready` on both kinds during R.

**Release R+1 (the old kind is removed from the bundle).**

- `TalosStorage` leaves `api/`, the bundle, the RBAC and the render.
- `internal/installer/crdcheck.go` gets a `retiredKinds` list beside `retiredFields`, one entry per removal, each citing this ADR. The entry for this removal is `{crd: talosstorages.ichor.maesh.dev}`.
- The installer refuses the upgrade while the live `talosstorages.ichor.maesh.dev` CRD holds any object in any namespace. The refusal names the count and the namespaces, and says to run `ichor migrate storage-preset` and then delete the old objects. This is the gate that stops a silent drop: the release cannot land on a cluster that still has objects of the kind.
- With no objects present the installer proceeds. It does not delete the CRD. The installer's own prune excludes CRDs for the reason quoted above, and an empty CRD left behind is inert. The operator removes it by hand with `kubectl delete crd talosstorages.ichor.maesh.dev`.
- The installer role needs a rule to list `talosstorages` objects. `config/rbac/ichor-installer/role.yaml` names `talosstorages.ichor.maesh.dev` only as a CRD resource name (line 36); the rule for listing the kind's objects is new and is part of R+1.
- The old ClusterRoles `ichor-talosstorage-{admin,editor,viewer}-role` (named in `config/rbac/ichor-installer/role.yaml` lines 91 to 93) are removed from the bundle in R+1. The installer prunes them only if they are in the inventory the last run wrote. A build from before the inventory has no such record, so each needs an entry in `renamedObjects` (`internal/installer/prune.go`, which today names only the break-glass ClusterRole). That entry is part of R+1.

**Helm-installed clusters.** The installer's refusal does not run on a `helm upgrade`. For those clusters R+1 adds a `pre-upgrade` hook Job to the chart, using the controller image, that lists `talosstorages` and exits non-zero when any object exists. The Helm documentation for hooks says of a Job hook: `If they are Job or Pod kind, Helm will wait until it successfully runs to completion. And if the hook fails, the release will fail.` The documentation retrieved states this for the install lifecycle; that a `pre-upgrade` hook runs before the chart's resources are loaded on upgrade is the same lifecycle applied to upgrade, and it is not separately established here. The hook's ServiceAccount needs the same list rule.

### 3. Alternatives and what each costs

| Option | Cost | Outcome |
|---|---|---|
| a. Remove the kind from the bundle and record it in a `retiredKinds` list that only warns | Small Go change | Rejected. Objects are stranded, and the next render drops layer 1 for their nodes. A warning does not stop that. |
| b. Keep the old CRD in the bundle, deprecated, and refuse create and update with a ValidatingAdmissionPolicy | Permanent second kind in every bundle; the admission policy is new | Rejected. Ownership is kept, but the old objects still drive layer 1 unless the render keeps reading them, which is option d without the copy. The cost never ends. |
| c. Remove the kind with nothing and document that the CRD and objects are left behind | None | Rejected. Same stranded objects and silent drop as (a), with no warning at all. |
| d. Migration over two releases with a dual read, a CLI copy, and an installer and Helm gate (chosen) | Two releases; a CLI command; a dual read in the render, `storage.Select` and the UI for R; a `retiredKinds` entry with a list-rule in the installer; a `pre-upgrade` hook in the chart; the old Go types live one more release | Chosen. It is the only option here that loses no data and changes no node at upgrade. |

Variant of (d) rejected: a controller that refuses to render a machine whose old object has no copy. It keeps the data but stalls the node's reconcile until someone runs the CLI, which is worse than the dual read.

### 4. The rest of the rename is mechanical

The inventory below is what the rename changes. It applies to R's new kind; the old kind's entries stay until R+1.

**How it was enumerated.** At `e08423ba4` (origin/main):

```
git grep -c -i -E 'talosstorage|talos-storage|talos storage|tsto\b'
```

This gives 89 files and 374 hits. The 21 files under `docs/adr/` (74 hits) keep their historical names and are not rewritten, except for status markers. `CHANGELOG.md` (1 hit) is written by the maintainer. Excluding those leaves 67 files and 299 hits. Counts are computed at that commit and go stale when main moves; re-run the command before the rename.

| Area | Files | Hits | Examples |
|---|---|---|---|
| API types, deepcopy, tests | 3 | 36 | `api/v1alpha1/talosstorage_types.go`, `zz_generated.deepcopy.go`, `samples_test.go` |
| CRD bases, chart CRD, samples, testdata, PROJECT | 7 | 26 | `config/crd/bases/ichor.maesh.dev_talosstorages.yaml`, `dist/chart/templates/crd/talosstorages.ichor.maesh.dev.yaml`, `PROJECT` |
| RBAC (config, chart, UI markers) | 20 | 46 | `config/rbac/talosstorage_{admin,editor,viewer}_role.yaml`, `dist/chart/templates/rbac/*`, `internal/uiapi/rbac_markers.go` |
| Controller and `cmd/main.go` | 10 | 85 | `internal/controller/talosstorage_controller.go` and its test, `evacuation.go`, `cmd/main.go` |
| Render path | 6 | 28 | `pkg/render/system.go` (`StorageFor`, `injectStorage`), `storage_test.go` |
| `pkg/storage` selection and evacuation | 4 | 14 | `pkg/storage/select.go`, `evacuation.go` |
| UI backend reads and tests | 7 | 15 | `internal/uiapi/node_service.go` (`mapListError("TalosStorage", err)`) |
| Proto and generated Go and TS | 3 | 28 | `proto/ichor/v1alpha1/talosdetails.proto` (`TalosStorageSection`), `gen/go/.../talosdetails.pb.go`, `ui/src/gen/.../talosdetails_pb.ts` |
| `internal/tools/mirrorgen` test | 1 | 2 | `internal/tools/mirrorgen/main_test.go` |
| Docs other than ADRs | 6 | 19 | `docs/design/architecture/storage.md`, `api.md`, `docs/reference/crds/talosstorage.md` (generated) |
| ADR files (incl. README) | 21 | 74 | historical; status markers only |
| CHANGELOG | 1 | 1 | maintainer writes the entry |

The 13 tracked file names that carry the kind are: `api/v1alpha1/talosstorage_types.go`, `config/crd/bases/ichor.maesh.dev_talosstorages.yaml`, `config/rbac/talosstorage_{admin,editor,viewer}_role.yaml`, `config/samples/ichor_v1alpha1_talosstorage.yaml`, `dist/chart/templates/crd/talosstorages.ichor.maesh.dev.yaml`, `dist/chart/templates/rbac/talosstorage-{admin,editor,viewer}-role.yaml`, `docs/reference/crds/talosstorage.md`, `internal/controller/talosstorage_controller.go` and `_test.go`.

Outside the repository the rename also reaches: the CRD and its objects on each cluster, the installer inventory ConfigMap, the three ClusterRoles and their bindings that earlier installers created, and any user role bound to them.

Left as they are: `TalosStorageSection` in the proto (it names the node-detail section that the UI reads; the wire format does not change with a message name, and the generated TS names follow). The `pkg/storage` package name, which is not the kind.

Regenerate, do not edit: `make manifests` (CRD and RBAC), `make sync-crds`, `make docs-reference`, `make generate` (deepcopy and mirror types), and the proto generation target listed by `make help`.

### 5. The `diskPreset` enum and the object-level rule from ADR 0167

ADR 0167 is on the unlanded branch `docs/rook-ceph-is-the-storage-ichor-prepares-for`, file `docs/adr/0167-rook-ceph-is-a-second-preset-that-renders-no-volume-and-has-no-evacuation-hook.md`. Its decisions 3 and 6 add `rook-v1` to the `diskPreset` enum and replace the field-level `self != ''` on `diskSelector` with an object-level rule on the spec: `self.diskPreset == 'rook-v1' || self.diskSelector != ''`, message `diskSelector is required for diskPreset longhorn-v1`.

Both carry over unchanged. The enum is a property of the field, not of the kind. The rule reads `self.diskPreset` and `self.diskSelector` on the spec, not the kind's name. The new kind's schema is generated from the same types, so the rule and the enum are in the new CRD as written.

### 6. Order against the in-flight work

The rename lands **after** both of these, because each would conflict with it:

- `feat/the-rook-ceph-storage-preset` (origin head `4407bae06`, the head at the time this record was written), which touches `api/v1alpha1/talosstorage_types.go`, `api/v1alpha1/validation_test.go`, `config/crd/bases/ichor.maesh.dev_talosstorages.yaml`, `dist/chart/templates/crd/talosstorages.ichor.maesh.dev.yaml`, `pkg/storage/rookv1.go` and its test, `pkg/storage/storage.go`, `pkg/storage/storage_test.go`, `pkg/storage/evacuation_test.go`.
- `feat/the-ui-shows-the-storage-presets` (origin head `ac0110677`), which adds `proto/ichor/v1alpha1/storage.proto`, `internal/uiapi/storage_service.go`, `ui/src/pages/StorageList.tsx` and routes, and names the current kind in its backend.

### 7. ADR number

The next free number on `origin/main` is `0167`: the highest number on origin/main is `0166`, and `0160` is absent there (it exists only on the local branch `docs/adr-storage-kernel-modules`). `0167` is taken on `origin/docs/rook-ceph-is-the-storage-ichor-prepares-for`, which lands first. This record takes `0168`, so the number does not collide with `0167` when that lands. Recheck the number at land time.

### 8. Supersession

This ADR supersedes part of ADR 0023, and the header records it as `Supersedes in part: 0023`, as ADR 0099 records its supersessions. ADR 0023's Decision line is not edited. The reverse marker belongs in ADR 0023's `Status:` line, as ADR 0013 and ADR 0099 carry theirs (`superseded in part by ADR 0099`). That one-line edit is not part of this branch and is left to the maintainer's decision, together with the note that ADR 0167 also supersedes part of 0023.

## Consequences

- Release R adds a kind and keeps the old one. The render, the selection and the UI read both for one release. The old Go types stay compiled for that release.
- Release R+1 can land only on a cluster with no `TalosStorage` objects. Clusters that did not migrate are refused rather than silently changed.
- The old CRD is left in place after R+1 and removed by hand. The installer does not delete CRDs, and this record does not change that.
- The installer gains a `retiredKinds` list, a list rule for the old kind's objects, and `renamedObjects` entries for the three old ClusterRoles. Each is a new piece of installer code, written with its own tests in its own change.
- The Helm guard depends on the `pre-upgrade` hook behaving as the install lifecycle describes. That is the part of this design least established (see Context).

## Open

- Whether a Helm-installed cluster that upgrades without running the hook can still lose objects. The Helm documentation retrieved does not settle the removed-template-with-`keep` case.
- The short name for `TalosStoragePreset`, which must not collide with `tsto` while both kinds are served.
- Whether the installer should refuse a removed enum value or a relaxed rule, which ADR 0167 also leaves open.
- Whether the proto message `TalosStorageSection` should be renamed for consistency. Left unchanged here.

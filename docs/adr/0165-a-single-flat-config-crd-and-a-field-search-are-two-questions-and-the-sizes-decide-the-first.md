# 0165. A single flat config CRD and a field search are two questions, and the sizes decide the first

Status: Proposed · Date: 2026-11-15 · Design section: Typed configuration · Relates to: 0003, 0009, 0039, 0072, 0099, 0101, 0104, 0152, 0158

Decided: nothing yet; this records the question and the options with their sizes. The proposal is to keep `spec.documents[]` as the one typed shape, to build the field search from an index generated out of the CRD schema (it does not need a new CRD), and to decide separately whether to retire the unused second copy of the union in `TalosCluster`, which is the one change that frees real room in the Helm release record.

## Context

Two requests arrived together.

1. Replace the per-kind documents with one centralised CRD that joins every setting into a single flat shape, with no duplicated setting, even if it gets long.
2. In the UI, replace "pick a document kind, then write a document of that shape" with a search box: look a field up, then fill in its value.

They are separable. The first changes the API and the second changes only how the UI finds a field. This ADR sizes the first, says what the second needs, and shows that the second does not depend on the first.

The constraint is the Secret Helm stores a release in, capped at 1,048,576 bytes. The record is Helm's `encodeRelease`: JSON, gzip at best compression, base64. Its size is taken here from a client-only dry-run install of `dist/chart` with Helm v3.21.4 (the version in `go.mod`), Kubernetes v1.36.0, status `deployed` and a fixed timestamp. At `origin/main` 3f4660630 the record is **991,560 bytes, 57,016 free**. The figures below come from the same procedure on variants of the chart, so they compare with that number and with each other.

### What the current shape is, and why

ADR 0003 chose `documents[]` as a discriminated union: a required `kind` and one optional field per Talos document kind, so a document reads like a Talos config document and a validation error names the kind it expected. The CRD has 65 kinds (the enum), of which `KubeStaticPodConfig` is the 65th. ADR 0003 also records that CRD schemas cannot use `$ref`; the apiextensions code at the pinned v0.36.6 still refuses it (`validation.go`: "$ref is not supported"; `convert.go`: "OpenAPIV3Schema '$ref' is not supported"). A type used in three places is three copies, in any CRD, flat or not.

ADR 0039 chose not to build a schema-driven renderer in the UI, because it would duplicate in TypeScript rules the API server already enforces, and said to revisit it only if the JSON `Textarea` proved too rough. ADR 0072 added hand-written help for each kind's top-level fields. Documents are still edited as JSON in a `Textarea` with a kind `Select`, which is the experience the second request objects to.

## Findings

### 1. Where the record goes

The two CRDs that carry the union are `TalosConfigPatch` (`spec.documents[]`) and `TalosCluster`, where the union sits at `spec.cni.custom.documents`. That field is deprecated and unused; ADR 0099 and 0101 keep it only because CRD fields are append-only, and the webhook and controller refuse `custom`. The chart files are 1,061,124 and 1,249,561 bytes. Each CRD is held twice in the record (the template and the rendered manifest), and gzip's 32 KB window cannot make the two copies one: gzip of the patches CRD is 106,267 bytes alone and 211,767 bytes for two copies back to back.

| Variant of the chart | Record | Free |
|---|---|---|
| Today | 991,560 | 57,016 |
| `KubeStaticPodConfig` removed from both CRDs | 452,972 | 595,604 |
| The unused union in `TalosCluster.spec.cni.custom.documents` removed | 598,916 | 449,660 |

So `KubeStaticPodConfig` (the core v1 `Pod`) is 538,588 bytes of the record, 54%. Its schema is 302,765 of the 461,397 bytes of per-kind schema in JSON, against 53,254 for the whole v1alpha1 `Config`. The unused copy in `TalosCluster` is 392,644 bytes, 40%. Any design that touches the other 64 kinds works on the remaining part, which is about 158,632 bytes of JSON per copy.

### 2. How large the union is

The union was built as a throwaway: every kind's fields merged into one tree by their path inside the kind, so `name` or `mtu` at the root of any kind becomes one `name` or `mtu`. Two fields at the same path with different types are both kept, the second under `name__Kind`. Descriptions are kept first-wins, or all distinct ones. The `required` lists of merged objects are not carried over (540 entries across the kind schemas).

| | per-kind schemas (JSON) | union (JSON) | change |
|---|---|---|---|
| All 65 kinds, first description | 461,397 | 434,744 | -5.8% |
| All 65 kinds, every distinct description | 461,397 | 437,813 | -5.1% |
| 64 kinds without the static pod, first description | 158,632 | 132,187 | -16.7% |
| 64 kinds without the static pod, every description | 158,632 | 135,256 | -14.7% |

The union is smaller than the per-kind schema, because a repeated field is written once, but by 5.1% of the whole, because the static pod cannot be merged with anything. In the chart, with the union replacing `documents[]` in place in both CRDs, the record is 941,472 bytes, 50,088 smaller (107,104 free). That variant is not shippable: replacing a field is not append-only. The shippable variant is a new CRD beside the old one, with the union at `spec.settings`:

| Shippable variant | Record | Free |
|---|---|---|
| New unified CRD beside the existing ones | 1,352,056 | -303,480 |
| The same, with the static pod kept whole under `staticPod` | 1,352,660 | -304,084 |
| The same, with the unused `TalosCluster` copy removed | 958,756 | 89,820 |

A unified CRD beside the existing ones costs 360,496 bytes against 57,016 free: it does not fit. It fits only if something else frees at least 303,480 bytes first, and the unused `TalosCluster` copy (392,644) is the one candidate that does.

The omit tables of the three subset kinds (`internal/tools/mirrorgen/omits.go`) hold 105 rows plus one for the pod's `status`: 86 unused, 11 protected by Talos, 7 whose type cannot be written as a CRD schema, 1 free-form `RawExtension`, and `Pod.status`. The 86 unused rows cost about 5.7 KB of record to widen, from a regeneration with those rows dropped; the figure is quoted, not re-derived here. Widening and a unified CRD together fit in 57,016 bytes only if the `TalosCluster` copy goes. The 8 fields that cannot be represented (kubelet `logging` and `memoryThrottlingFactor`; kube-proxy `clientConnection` and `logging`; scheduler `leaderElection`, `clientConnection`, `extenders` and `profiles[].pluginConfig`) are an obstacle to the generator and to the typed-only rule, not to the union's shape: a flat CRD meets the same wall, so "covers everything" means "everything representable" in either shape.

### 3. What counts as the same setting

Leaf fields are fields with no child properties (arrays and maps of scalars count as leaves). A name is a collision when it is a leaf in at least two kinds.

| | 65 kinds | 64 (no static pod) | 63 (no static pod, no v1alpha1 `Config`) |
|---|---|---|---|
| Leaf (kind, path) pairs | 1,719 | 934 | 614 |
| Distinct paths once the kind is dropped | 1,532 | 748 | 428 |
| Distinct leaf field names | 563 | 407 | 304 |
| Names that are a leaf in two or more kinds | 148 | 133 | 56 |
| ... identical type, format and constraints | 122 | 116 | 52 |
| ... same type, different constraint | 5 | 0 | 0 |
| ... conflicting type | 21 | 17 | 4 |
| Names that are a leaf in one kind and an object in another | 24 | 20 | 7 |

Examples. Identical and plainly the same setting: `enabled` (boolean, 11 kinds), `endpoint` (string, 9), `address`, `destination`, `source`, `metric`, `table`, `interval`, `match`. Conflicting: `mtu` (int32 in six network kinds, plain integer in `Config`), `hardwareAddr` (a byte string in four kinds, a string in `Config`), the bond fields `adSelect`, `lacpRate` and `xmitHashPolicy` (an integer in `BondConfig`, a string in `Config`: one setting spelled two ways by Talos itself, once in the v1alpha1 document and once in the newer one), and, with `Config` set aside, `mode` (the file mode integer of `EtcFileConfig` against a string in `ContainerConfig` and `KubeProxyConfig`), `type`, `level` and `metadata`.

Identical type is not identical meaning. `SysctlConfig.params` and `SysfsConfig.params` are both a string map, neither has a description, and they configure different subsystems; merged by name or by path they become one field that cannot say which. 81 of the 116 identical-signature names carry no description at all, so a description comparison cannot separate same-setting from coincidence for most of them, and only 27 have identical non-empty text. By name the dedup is unsafe, by name and type it is unsafe, and by path inside the kind it removes 186 of 934 leaf entries (20%) and still needs a rule for the 4 to 17 conflicting names. A disambiguator that always works is the kind, which puts the structure back.

The kind carries more than a prefix. 35 of the 65 kinds are named documents with a root `name` that is the instance key, so a cluster can hold several `BondConfig` or `UserVolumeConfig`; a flat object holds one value per field, so those need a list keyed by something, which is the document. 18 of the 63 kinds other than `Config` and the pod have no field path that no other kind has, and 14 have every path also present in another kind (`SysctlConfig` and `SysfsConfig`, `VolumeConfig` and `UserVolumeConfig`, `FilesystemScrubConfig` and `FilesystemTrimConfig`), so a set of populated fields cannot say which document to write. A flat shape needs an explicit discriminator on those, and it would reintroduce the kind.

### 4. Going back out

`apply-config` takes documents. Today the mapping exists in generated code: `TalosConfigPatchDocument.Document()` and `TalosConfigPatchDocumentFromMachinery` (`api/v1alpha1/zz_generated.talosconfigpatchdocument.go`) over the 408 conversion functions of `pkg/typedconfig`, regenerated when `github.com/siderolabs/talos/pkg/machinery` (v1.14.5 in `go.mod`) is bumped. A flat CRD needs one more layer: a table from each union path to the (kind, path) pairs it feeds, a classifier for the kinds that fields cannot identify, and a splitter that creates one document per instance key. The table can be generated by the same tool that builds the union, so it is per machinery version in the same way the mirror types already are, and a rename in Talos fails the generator, not a user. The classifier and the splitter are hand-written and have no counterpart today. Two existing tables are keyed by Talos document path and would be keyed by union path or translated: `pkg/fieldmeta` (reboot prediction, ADR 0009) and the per-document validation in `Validate()`. The 540 `required` entries and the `minProperties`/`maxProperties` rule of the documents would have to be rewritten as per-kind rules over the flat object.

### 5. The search

What exists. `docs/reference/crds/talosconfigpatch.md` is generated from `config/crd/bases` by `make docs-reference`: 2,405 lines, one row for each field with its path, type, required flag and description, and the docs site already searches it with a command-palette dialog (`site/src/components/SearchDialog.tsx`, a stock preactcn `Command`). The UI has no search: `ui/src/components/ui` has the preactcn `combobox`, `dialog`, `input-group` and `field`, but no `command`. The UI backend cannot read a CRD at run time (its role holds `get` on one named CRD, the Gateway API one), and ADR 0039 left the schema unread by the browser on purpose.

What the schema carries. For the 64 kinds without the static pod, 1,206 fields: 414 (34%) have a description, 157 a format, none has an `enum` and none has a default. The cause is how Talos documents its fields: as `description:` blocks in YAML comments (`//   description: |`), which the mirror generator does not read, while machinery's generated `*_doc.go` tables hold a `Description` for each field and a `Values` list of allowed values where the field is an enumeration (about 700 descriptions and 41 `Values` lists in the non-v1alpha1 tables; the v1alpha1 tables are structured differently and were not counted). Not in either place: an example, the Talos version that introduced a field, and whether a change needs a reboot (that is `pkg/fieldmeta`, by path).

Size of an index. One row for each field (kind, path, type, description, required) is 1,206 rows, 159,504 bytes of JSON, 23,946 gzipped, for the 64 kinds; 2,281 rows and 75,529 gzipped with the static pod. An index belongs in the UI bundle, not in the chart, so it takes nothing from the record. Adding the machinery descriptions and `Values` to the index and not to the CRD keeps the record unchanged; adding them to the CRD would put the same text into four copies.

The smallest build that does what was asked:

1. A generator step, beside `internal/tools/gendocs`, that writes the index as one JSON file the UI imports. No cluster access.
2. The preactcn `command` component, added with the scaffold command, and a search dialog copied in shape from the docs site one: a query, a list of rows with path, type and description, scored by substring over name, path and description. A plain scan is enough for about 1,200 rows; move to a real index only if the static pod's 1,075 rows go in.
3. Choosing a row adds a document of the row's kind to the patch and opens its value input: an `Input`, `Switch` or `Select` by type, and the existing `Textarea` for an object or array. The kind comes from the row, so the user never chooses a kind first, and the API server's strict validation stays the only validator (ADR 0039).

None of this depends on the CRD shape. With the unified CRD the same index would need the mapping of finding 4 to know which document a row belongs to, so the per-kind shape makes the search simpler, not harder.

## Options

- **A. Keep the per-kind union; build the search.** No schema change, no record growth. The search is the build of finding 5.
- **B. Add a unified flat CRD beside the existing one.** The record grows by 360,496 bytes and does not fit in 57,016. It needs the discriminator, instance keys and mapping layer of findings 3 and 4, and it leaves two ways to say the same thing for ever, since the old fields cannot be removed.
- **C. Retire the unused union in `TalosCluster.spec.cni.custom.documents`.** Frees 392,644 bytes (the record goes to 598,916). ADR 0152 is the precedent for retiring a field as an exception to append-only; the webhook and controller already refuse `custom`. It decides nothing about the shape, and it makes both B and the 5.7 KB widening fit. It needs the maintainer's decision and a look at whether any installed object holds the field.
- **D. Move the CRDs to their own chart and release.** Helm's own guidance names this as the way to template CRDs outside the main chart. The CRDs are 92% of the record, so a CRD-only release is 965,140 bytes (83,436 free): 26 KB more room, and a second install step. Putting the CRDs in `crds/` instead frees about 341,000 bytes (650,500 total) and stays off the table: Helm installs `crds/` content only on install, never on upgrade or rollback, and ADR 0104 section 4.6 chose `templates/` so that CRDs upgrade with the chart.

## Proposed decision

Keep the per-kind union (A), build the search from a generated index, and decide C on its own merits because every other lever, including widening the subsets, waits on it. B is not recommended: it saves 5.1% of the schema at best, does not fit without C, needs a discriminator and an instance key on a large share of the kinds, and leaves the old shape in place.

If the maintainer still wants B, it supersedes ADR 0003's union shape (its mirror generation and round-trip tests stay), collides with ADR 0158's `KubeStaticPodConfig` (the pod cannot be merged and stays 54% of the record in any shape) and with ADR 0104 section 4.6, and C would have to land first.

## Not determined

- Whether any installed object, the demo included, holds `spec.cni.custom.documents`, and whether the API server drops the field from such an object when it is no longer in the schema.
- Whether machinery's `Description` and `Values` tables can be matched to the mirror fields by path for every shipped kind; the counts above are of the tables, not of matched fields.
- The record cost of the 86 unused omits on top of a unified CRD; only the per-kind figure (about 5.7 KB) was reported.
- Whether a semantic dedup rule exists that is better than name, name and type, or path; the description text is too sparse to decide it.
- Whether the numbers move when the throwaway union is serialised by controller-gen itself rather than by `sigs.k8s.io/yaml`.

# 0165. A single flat config CRD and a field search are two questions, and the sizes decide the first

Status: Accepted · Date: 2026-11-15 · Amended · Design section: Typed configuration · Relates to: 0003, 0009, 0039, 0072, 0099, 0101, 0104, 0152, 0158

Decided: Ichor's patch schema is Ichor's own, not a copy of Talos's. `TalosConfigPatch.spec.config` is one nested object, a `machine` and a `cluster` section in the shape of the `v1alpha1` `Config` document, with every document kind placed beside the setting of the same meaning, each setting written once, and the controller converts it to Talos documents when it renders. It is a new field of the existing CRD, not a new CRD. `spec.documents[]` is removed from the CRD in `v1alpha1`, so `spec.config` is the only way to write Talos configuration (see the amendment). The schema is generated from the mirror types, so a field Talos adds appears with no list edited. The field search stays a separate question and is built from a generated index.

## The decision

The first version of this ADR proposed keeping the per-kind union and recorded why a single shape was not worth it. That proposal assumed Ichor's shape had to mirror Talos's document types, so that a unified shape needed a classifier, a splitter and a rule for every setting Talos spells two ways. The maintainer's decision removes the assumption: what Ichor builds is Ichor, not Talos, and the controller converts back to Talos when it applies. The objections below assume the mirroring, and most of them go with it.

- **A new field on the existing CRD**, `spec.config`, not a second CRD: a second CRD would repeat the metadata, controller, webhook and RBAC for nothing, and CRD fields are append-only, which permits adding one.
- **A patch that sets `spec.documents[]` and `spec.config` was refused** while both existed, with a message naming both; the amendment removes `spec.documents[]`, and with it the case.
- **Typed only.** There is no raw-YAML field.
- **The conversion is the controller's**, at render time. The controller still never mutates `spec`.
- **`spec.documents[]` stayed, deprecated, in this decision as first accepted.** The amendment below removes it.

### Why the recorded objections no longer hold

- *The conflicting types (finding 3: 21 names, a leaf in two kinds with different types).* They were conflicts because the unified field had to be both. In Ichor's schema a setting has one type, and the conversion writes the spelling Talos validates. The cases that are one setting spelled two ways by Talos itself (`disabled` against `enabled` for time sync, CoreDNS and the controller manager, `certSANs` against `certExtraSANs`, a list of addresses against a list of objects for nameservers, mirror endpoints as strings against objects) are listed in the typed configuration page, with the spelling Ichor took: the one of the document, because Talos adds new fields to documents and not to the `v1alpha1` block. The settings of the `v1alpha1` document that Ichor does not carry for this reason are recorded in `pkg/ichorconfig/testdata/not_carried.txt`, outside the generator. The bond fields the finding names (`adSelect`, `lacpRate`, `xmitHashPolicy`) are not resolved yet, see the open part below.
- *The classifier for the 18 kinds with no unique field path.* It existed to infer the target kind from the filled fields. The schema defines the placement, so the target is known by construction, and no classifier exists.
- *The splitter for the 35 repeatable named documents.* They are maps from the name in Ichor's shape, and the conversion makes one document for each key. A kind whose order matters (the API server's authorizers) is a list with the name in each entry. No guessing splitter exists.
- *"No duplication is impossible."* That held only under mirroring. A setting that two documents spell, `mtu`, `enabled` or the nameservers, is one field wherever the rows join the objects.
- *The 540 `required` and `minProperties`/`maxProperties` rules.* A field a document requires stays required where that document is the only one whose fields the object holds, which is every entry of a collection and every section a single kind owns. Where two documents meet in one object (the kubelet, the API server, the time and network sections) the field is optional, because a patch is a sparse overlay and one document cannot require a field of the other; Talos validates the finished configuration when the machine is rendered. The generator reports each field it relaxes.
- *The table from union path to document path, and `pkg/fieldmeta`'s paths.* The table is the `ichor` struct tag of each field, written by the generator. `pkg/fieldmeta` is keyed by the path in the Talos document and is used on documents after conversion, so it needs no new paths for the effective-configuration view; a view of the paths of `spec.config` itself would key them by Ichor's path and is not built.

Two objections held: the record, and the append-only rule, which is why `spec.documents[]` stayed until the amendment retired it as an exception.

### The sizes of this design

The Helm release record is the encoding of `encodeRelease` (JSON, gzip at best compression, base64) of a client-only dry-run install of `dist/chart` with Helm v3.21.4 against Kubernetes v1.36.0, status `deployed` and a fixed timestamp.

| Chart | Record | Free of 1,048,576 |
|---|---|---|
| `origin/main` 6720301b2 (also 0dfa6e550, the retirement of `spec.cni.custom.documents`) | 598,888 | 449,688 |
| 0dfa6e550^, before the retirement | 991,560 | 57,016 |
| A throwaway union of every kind merged by path, in the patches CRD | 937,660 | 110,916 |
| The same without `KubeStaticPodConfig` | 687,780 | 360,796 |
| `spec.config` as built, beside `documents[]`, one version (`017be3a79`, no longer on the branch) | 950,492 | 98,084 |
| `spec.config` alone, `documents[]` removed | 575,308 | 473,268 |
| **The same, with every representable field of the kubelet, kube-proxy and kube-scheduler subsets** | **578,416** | **470,160** |

598,916, the figure the first version of this ADR gave for the tree after the retirement, was a projection of a tree that still held a doc comment the retirement removed; the tree itself is 598,888. The patches CRD as compact JSON is 902,068 bytes (472,817 before). The static pod is 249,880 of the throwaway union's 338,772 added bytes, 74%: the core v1 `Pod` is in the schema once in `documents[]` and once in `spec.config.machine.staticPods`, and no merge can shrink it.

Serving a second API version of the CRD costs a whole second schema, because each version carries its own. With `spec.config` in `v1alpha1`, a `v1alpha2` that carries `spec.config` alone gives a record of 1,312,924 bytes, 264,348 over the cap; a `v1alpha2` that carries both fields gives 1,336,324. A `v1alpha1` that has `documents[]` alone beside a `v1alpha2` that has `spec.config` alone gives 958,940, 89,636 free. So the intermediate state of a migration through a second API version fits only if `spec.config` never ships in `v1alpha1`.

### What is open

The links are not joined. `machine.network.interfaces[]` is the `v1alpha1` list and the link documents (bond, bridge, dummy, veth, VRF, Ethernet, DHCPv6, Layer 2 VIP, routing, BGP and the probes) are collections beside it, so a bond can still be written in two places, and the settings the two spell differently (the bond mode, hash policy, LACP rate, ARP validation and targets, fail-over MAC, primary reselect and AD select are words in one and numbers in the other; routes are `network` in one and `destination` in the other; addresses are strings in one and objects in the other; the hardware address is a string in one and bytes in the other) are not resolved. The same holds for `machine.disks` and `machine.systemDiskEncryption` against the volume documents. Joining them needs a row for each pair and a conversion for each setting spelled differently, and the entries of the interface list need an activation rule, since one entry would feed a document of the v1alpha1 list or one of the link kinds. The bond-mode words need the machinery enum tables, which the mirror types do not keep.

The web UI edits documents and the API server's patch messages carry documents. The backend converts between them and `spec.config` (see the amendment), so the editor works as before; the proto message and the "choose a kind first" flow are unchanged, and replacing them with `spec.config` paths is a separate change.

## The original sizing

What follows is the sizing of the first version, kept as the record of the question. Its decision is superseded by the one above.

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
| The unused union in `TalosCluster.spec.cni.custom.documents` removed | 598,888 | 449,688 |

The removal is 598,888 bytes, the value ADR 0152 records: that is what the tree from the retirement commit `0dfa6e550` onwards gives (`6720301b2`, `origin/main` at the time, among them), by the method of ADR 0158, and `0dfa6e550^` gives 991,560. The 598,916 this record first gave for the same removal was a projection, and the table and the text below now use 598,888.

So `KubeStaticPodConfig` (the core v1 `Pod`) is 538,588 bytes of the record, 54%. Its schema is 302,765 of the 461,397 bytes of per-kind schema in JSON, against 53,254 for the whole v1alpha1 `Config`. The unused copy in `TalosCluster` is 392,672 bytes, 40%. Any design that touches the other 64 kinds works on the remaining part, which is about 158,632 bytes of JSON per copy.

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

A unified CRD beside the existing ones costs 360,496 bytes against 57,016 free: it does not fit. It fits only if something else frees at least 303,480 bytes first, and the unused `TalosCluster` copy (392,672) is the one candidate that does.

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
- **C. Retire the unused union in `TalosCluster.spec.cni.custom.documents`.** Frees 392,672 bytes (the record goes to 598,888). ADR 0152 is the precedent for retiring a field as an exception to append-only; the webhook and controller already refuse `custom`. It decides nothing about the shape, and it makes both B and the 5.7 KB widening fit. It needs the maintainer's decision and a look at whether any installed object holds the field.
- **D. Move the CRDs to their own chart and release.** Helm's own guidance names this as the way to template CRDs outside the main chart. The CRDs are 92% of the record, so a CRD-only release is 965,140 bytes (83,436 free): 26 KB more room, and a second install step. Putting the CRDs in `crds/` instead frees about 341,000 bytes (650,500 total) and stays off the table: Helm installs `crds/` content only on install, never on upgrade or rollback, and ADR 0104 section 4.6 chose `templates/` so that CRDs upgrade with the chart.

## Proposed decision (superseded by the decision above)

Keep the per-kind union (A), build the search from a generated index, and decide C on its own merits because every other lever, including widening the subsets, waits on it. B is not recommended: it saves 5.1% of the schema at best, does not fit without C, needs a discriminator and an instance key on a large share of the kinds, and leaves the old shape in place.

If the maintainer still wants B, it supersedes ADR 0003's union shape (its mirror generation and round-trip tests stay), collides with ADR 0158's `KubeStaticPodConfig` (the pod cannot be merged and stays 54% of the record in any shape) and with ADR 0104 section 4.6, and C would have to land first.

## Not determined

- Whether any installed object, the demo included, holds `spec.cni.custom.documents`, and whether the API server drops the field from such an object when it is no longer in the schema.
- Whether machinery's `Description` and `Values` tables can be matched to the mirror fields by path for every shipped kind; the counts above are of the tables, not of matched fields.
- The record cost of the 86 unused omits on top of a unified CRD; only the per-kind figure (about 5.7 KB) was reported.
- Whether a semantic dedup rule exists that is better than name, name and type, or path; the description text is too sparse to decide it.
- Whether the numbers move when the throwaway union is serialised by controller-gen itself rather than by `sigs.k8s.io/yaml`.

## Amendment (2026-11-16): spec.documents is removed

Decided by the maintainer: `TalosConfigPatch.spec.documents` is removed from the CRD in `v1alpha1`, with no second API version and no conversion webhook. `spec.config` is the only way to write Talos configuration, so there is one place to write every setting, in Ichor's own shape. This is the third exception to the append-only rule, recorded in ADR 0152.

**Why.** Two spellings of one setting in one CRD is the ambiguity `spec.config` was built to remove, and `documents[]` was kept only because CRD fields are append-only. Keeping it also kept the schema twice: the patches CRD held the union and `spec.config`, 950,492 bytes of the record with 98,084 free. With `documents[]` gone the record is 575,308 bytes, 473,268 free of 1,048,576, by the method above, and 578,416 (470,160 free) once the subsets of ADR 0158 carry every representable field. Removing it costs the stored objects that set it (below); the maintainer accepted that.

**What stays.** The Go type `TalosConfigPatchDocument` and its generated conversion to and from the machinery types stay: the conversion of `spec.config` decodes into it, validates with it and calls `.Document()`, so a setting still reaches a node through the same mirror and machinery types. Only the CRD field goes. The `ichorgen` schema and the `ichor` struct tag are unchanged, and `ConfigFromDocuments`, the inverse of the conversion, writes the settings of documents in `spec.config` for what builds documents (`ichor init` and `ichor adopt` for their patches, the web UI's editor). It refuses a setting the schema does not carry, by name, and gives back the same documents or an error.

**Stored objects.** A patch that sets `documents` loses it: the API server prunes a field that is no longer in the schema when it decodes an object from storage as well as when it writes one, and the bytes in etcd lose it at the next write. The nodes the patch reached lose its settings at their next apply. This holds for any cluster, the maintainer's own included, and is in the release's upgrade notes. A client that asks for strict field validation (`kubectl`, and every Ichor client) is refused with `unknown field "spec.documents"`; `ichor patch apply` says to write `spec.config`.

**What changed in the conversion.**

- A cluster-layer patch reaches workers, and Talos refuses the control plane's documents on a worker; the render leaves a document only a control plane takes out of a worker's configuration. Until now the `v1alpha1` spelling was tolerated there.
- Where the machine takes no Kubernetes documents (before Talos 1.14), the inline manifests and extra manifests are written to the lists of the `v1alpha1` document, and any other Kubernetes document is refused with the Talos version named.
- A switch for a document (`machine.features.kubePrism.enabled`, `kubernetesTalosAPIAccess.enabled`) that is false is written to the `v1alpha1` document with every other setting of its object, because Talos refuses a setting in both the `v1alpha1` document and a document of its own.
- The renderer's guards on a patch (Ichor's CNI inline manifest, Talos API access on workers, `cluster.proxy.disabled`) apply to `spec.config` alone, and the code that rewrote `documents[]` for them is gone.

**What is still open.** The links are not joined, as above: `machine.network.interfaces[]` against the bond, bridge, dummy link, veth, VRF, Ethernet, DHCPv6 and VIP documents, and `machine.disks` and `machine.systemDiskEncryption` against the volume documents. A bond can be written in two places, so one place to write every setting is not yet true of the links. It needs a row for each pair, a conversion for each setting spelled differently, a rule for which document an interface entry writes, and the enumerations of the `nethelpers` package, because the mirror lowers them to `uint8`; Talos's own tolerance for both forms of one link is not established. It is a separate change.

**The field search.** The index is built from the `spec.config` schema: each setting is a row of the document that spells it, in the path it has in that document. The editor and the wire messages still carry documents.

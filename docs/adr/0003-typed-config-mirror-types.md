# 0003. Typed configuration through generated mirror types

Status: Accepted · Date: 2026-10-24 · Design section: 4.4 (Typed configuration)

## Context

The design assumed that `TalosConfigPatch.spec.documents[]` could embed the
Talos machinery config document types directly, so that the CRD's OpenAPI
schema came from Talos's own definitions and Ichor maintained no parallel
catalogue of fields. Verifying that was listed as the first spike, and as the
first of the design's open questions.

The spike was run against `github.com/siderolabs/talos/pkg/machinery v1.14.3`
(a separate Go module, tagged with the Talos release, MPL-2.0), with
controller-gen. The result is that direct embedding does not work, and does
not work partially:

- **The types carry no JSON tags at all.** `config/types/` has 103 files with
  `yaml:"…"` tags and zero occurrences of `json:"…"`. The types are YAML-first
  and Talos never JSON-marshals them.
- **controller-gen keys strictly off JSON tags** and hard-errors on the first
  untagged field: `encountered struct field "ConfigVersion" without JSON tag
  in type "Config"`, and the same for every field of every document kind down
  to the simplest one (`SysctlConfigV1Alpha1`, a single map). It still writes a
  CRD, but degrades each type to a bare `type: object` with no properties —
  which, with structural-schema pruning, would silently discard every field of
  every document on write. So ignoring the error is worse than the error.
- **Several field types cannot be described structurally at all**, because they
  have no exported fields: `block.ByteSize` (human sizes like `2.5GiB`, behind
  `encoding.TextMarshaler`), `cel.Expression` (holds a compiled CEL AST), and
  `meta.Addr` / `meta.Prefix` / `meta.AddrPort` (embedded `netip` types, used
  throughout the network documents). A generator that read `yaml` tags instead
  of `json` tags would still get these wrong.
- **Size is a second, independent problem.** CRD schemas under
  `apiextensions.k8s.io/v1` may not use `$ref`, so controller-gen inlines every
  nested type at every use site with no sharing. Talos's own published JSON
  Schema for all 55 kinds is 483 KB *with* `$ref` reuse and *without* the
  fields it marks `docgen:nodoc` — of `v1alpha1.MachineConfig`'s 26 real Go
  fields it describes 8. Fully inlined and complete, the v1alpha1 `Config`
  document alone is plausibly in the same range as the CRDs documented as
  exceeding etcd's 1.5 MiB `--max-request-bytes` ceiling.

DeepCopy, which looked like the harder half, is not a problem: machinery
already generates `DeepCopy`/`DeepCopyInto` on its own types, and
controller-gen detects and calls through to them.

## Decision

Take the fallback the design already named: generate mirror types from the
machinery types. Nothing about the Talos field catalogue is hand-maintained.

- A `go generate` tool walks the allow-listed machinery document types and
  emits mirror structs under `api/v1alpha1/` with JSON tags taken from the
  `yaml` tag name, the source doc comments carried over as CRD descriptions,
  and `+kubebuilder` markers. Machinery stays the single source of truth for
  which fields exist.
- Field types that have no faithful structural-schema representation are
  substituted through a table **keyed by Go type**, not by field path:
  `ByteSize`, `cel.Expression`, `meta.Addr`, `meta.Prefix`, `meta.AddrPort`,
  `meta.ArgValue`, `Base64Bytes`, `DiskSize`, `FileMode`,
  `InstallDiskSizeMatcher`, `time.Duration`. Each becomes a string (or
  `[]byte`) with a pattern or format, and converts through machinery's own
  `TextMarshaler`/YAML marshaller so Talos keeps defining the semantics. Being
  keyed by type is what keeps this table from growing with Talos's field count:
  roughly ten entries, not a thousand fields.
- Fields typed `meta.Unstructured` (`map[string]any`, an arbitrary-YAML hatch
  inside Talos's own types) are dropped by the generator and reported.
  Carrying them would reintroduce exactly the raw-YAML escape hatch that
  principle 1 and the typed-only decision rule out.
- Conversion is generated in both directions: mirror to machinery for
  rendering, machinery to mirror for `adopt`.
- The generator's correctness check is a **round-trip test per allowed kind**:
  machinery document → mirror → machinery document must come out
  YAML-identical. A machinery upgrade that removes or retypes a field then
  fails to compile or fails that test, instead of silently changing the API.
- The kind allow-list drives generation, so the CRD's size is a function of a
  decision we control. **Measured, and the answer is comfortable.** Two numbers
  matter and they are not the same thing: the generated YAML file for
  `talosconfigpatches` is 151,887 bytes, while the object a running API server
  actually serves — which is what etcd stores and what
  `--max-request-bytes` limits — is **69,455 bytes, 4.4% of the 1,572,864-byte
  ceiling**. Serialized JSON is roughly half the file's size because the file
  carries indentation and block scalars for every inherited Talos doc comment.
  Quote the served size when deciding anything; the file size is the
  pessimistic reading. No splitting into `TalosNetworkPatch`-style CRDs is
  needed, and there is room for the remaining 52 kinds many times over.
- `documents[]` is a **discriminated union**: a required `kind` field carrying
  `+unionDiscriminator` and an enum of the allowed kinds, plus one optional
  field per kind, with two CEL rules enforcing that exactly one field is
  populated and that it is the one `kind` names. The alternative — inferring
  the kind from whichever field is set — was rejected because Talos's own
  documents carry a kind, so this way the YAML reads like a Talos config
  document and the validation error says which kind it expected.
  The legacy v1alpha1 document has no `kind` string in Talos itself, so its
  discriminator value is **`Config`**, matching the Go type name. That is now
  published API surface.
- Phase 1 ships `TalosConfigPatch` without `documents[]`; the generator is the
  first work item of phase 2. The rest of phase 1 does not depend on it.

## What the generator actually dropped

Walking `v1alpha1.Config` found **eleven** `meta.Unstructured` fields, not the
two this ADR first guessed at:

`KubeletConfig.KubeletExtraConfig`, `KubeletConfig.KubeletCredentialProviderConfig`,
`MachineConfig.MachinePods`, `MachineSeccompProfile.MachineSeccompProfileValue`,
`MachineConfig.MachineBaseRuntimeSpecOverrides`,
`AdmissionPluginConfig.PluginConfiguration`, `APIServerConfig.AuditPolicyConfig`,
`ResourcesConfig.Requests`, `ResourcesConfig.Limits`,
`AuthorizationConfigAuthorizerConfig.AuthorizerWebhook`,
`SchedulerConfig.SchedulerConfig`.

They have one thing in common: every one is a pass-through to **another
project's** schema — kubelet's `KubeletConfiguration`, Kubernetes' audit
policy, the scheduler's component config, a seccomp profile, a resource list.
Talos types them as unstructured precisely so it need not depend on those
schemas. So this is not Talos being careless; it is a real property of its API,
and it collides with Ichor's typed-only rule in a way worth naming: for these
fields the choice is not "typed or raw", it is "pass-through or absent".

Phase 2 leaves them absent. The cost is expressiveness, and it is **not
silently paid** — the field is missing from the CRD, so the API server rejects
it with a clear error rather than dropping a value the user thought they set.
The most consequential one is `machine.kubelet.extraConfig`, which is a
commonly used field in hand-written Talos configuration; a user who needs it
cannot express it through Ichor today.

What closing the gap would actually cost was measured with controller-gen
v0.21.0 against the real candidate types. Served sizes use this CRD's own
file-to-served ratio.

| Field | Upstream type | New dep | Served | Generates? | Needed |
|---|---|---|---|---|---|
| `ResourcesConfig.Requests`/`Limits` | `corev1.ResourceList` | no | ~430 B | clean | common |
| `MachineSeccompProfileValue` | `specs.LinuxSeccomp` | no | ~1.3 KB | clean | occasional |
| `AuthorizerWebhook` | `apiserver.WebhookConfiguration` | no | ~3.4 KB | clean | rare |
| `AuditPolicyConfig` | `audit/v1.Policy` | no | ~4.7 KB | clean | occasional |
| `KubeletCredentialProviderConfig` | `kubelet/config/v1` | `k8s.io/kubelet` | ~6.5 KB | clean | common |
| `SchedulerConfig` | `kube-scheduler/config/v1` | `k8s.io/kube-scheduler` | ~35 KB | **errors** | occasional |
| `KubeletExtraConfig` | `kubelet/config/v1beta1` | `k8s.io/kubelet` | ~34 KB | **errors** | **common** |
| `MachineBaseRuntimeSpecOverrides` | `specs.Spec` | no | ~37 KB | **errors** | rare |
| `MachinePods` | `[]corev1.Pod` | no | **~332 KB** | clean | rare |
| `PluginConfiguration` | none — per-plugin | — | — | not mirrorable | occasional |

Four things that sketch had wrong or missing:

- **The generator cannot tell these eleven apart.** The substitution table and
  the drop rule are keyed by Go type, and all eleven *are* `meta.Unstructured`.
  So even the free, clean, commonly-wanted one needs a per-field override —
  new generator machinery, not a table row. That is the real floor on any of
  this.
- **Three of them do not generate cleanly**, and for the same reason this ADR
  exists: types never written with CRD embedding in mind. `specs.Spec` has
  untagged fields and an `interface{}`; the kubelet and scheduler
  configurations carry a `float32`, which controller-gen only emits under
  `allowDangerousTypes`, and the scheduler type also trips `listType` three
  times. Each needs either that flag and the risk, or hand-pruning.
- **`MachinePods` is disqualified by size alone** — one optional field at
  ~332 KB served is a fifth of etcd's ceiling and nearly five times the whole
  current CRD.
- **There is no fourth option.** Every one of the eleven is still
  `meta.Unstructured` in Talos's *modern*, non-deprecated documents too, so
  moving the allow-list off the legacy `Config` document would relocate this
  wall rather than remove it.

Everything except `MachinePods` and `PluginConfiguration` would land the CRD
around 192 KB served, 12% of the ceiling — so headroom is not the constraint.
Generation friction and the generator's type-keyed design are.

## Follow-up: field-level overrides close five of the eleven

The measurement above is now acted on. mirrorgen gained a second table,
`fieldOverrides` (`internal/tools/mirrorgen/main.go`), consulted before
`mapType` ever sees a field's static type. It exists because the type-keyed
`substTable` *can't* express this: all eleven dropped fields are
`meta.Unstructured`, so a table keyed by Go type can only say "drop
`meta.Unstructured`" or "don't" — never "drop ten of them, keep one." Keying
by `"StructName.FieldName"` instead is unambiguous and stays a five-row table,
not a rewrite of the generator.

Five fields now mirror as their upstream type, with no new dependency and a
clean CRD schema, exactly as measured:

| Field | Mirrors as |
|---|---|
| `ResourcesConfig.Requests` | `corev1.ResourceList` |
| `ResourcesConfig.Limits` | `corev1.ResourceList` |
| `MachineSeccompProfile.MachineSeccompProfileValue` | `specs.LinuxSeccomp` (OCI runtime-spec) |
| `APIServerConfig.AuditPolicyConfig` | `audit/v1.Policy` |
| `AuthorizationConfigAuthorizerConfig.AuthorizerWebhook` | `apiserver/v1.WebhookConfiguration` |

Conversion goes through two new generic helpers in `pkg/typedconfig/substitution.go`
(`mirrorToMachineryUnstructured` / `machineryToMirrorUnstructured`): marshal
the mirror value through its own JSON tags, land the result in
`meta.Unstructured.Object`, and back. This is a different shape from the
scalar substitutions above — those round-trip through machinery's own
YAML/TextMarshaler because the ten-entry table's types *define* their own
wire form; these five don't have a machinery-defined wire form to defer to
(the whole reason they were `meta.Unstructured`), so JSON — the form every
upstream schema here already commits to — is what both directions agree on.

The remaining six stay dropped, for the reasons already measured: `KubeletExtraConfig`
and `SchedulerConfig` need `allowDangerousTypes` (a `float32`, package-wide, not
field-scoped); `MachineBaseRuntimeSpecOverrides` has untagged fields and an
`interface{}`; `MachinePods` is ~332 KB served on its own; `PluginConfiguration`
has no single schema. `KubeletCredentialProviderConfig` is the one that
generates cleanly but needs a new dependency (`k8s.io/kubelet`) — left dropped
because whether that dependency is worth adding is a product call, not a
generator one; the table entry is a one-line addition whenever that call gets
made.

**The one override that wasn't clean: `MachineSeccompProfileValue`.** Its CRD
schema generates fine, but `controller-gen object:generate` (DeepCopy) doesn't:
`specs.LinuxSeccomp` has pointer and slice fields but, unlike the other four
overrides (all Kubernetes API types), no `DeepCopy`/`DeepCopyInto` of its own.
deepcopy-gen's fallback for a struct-shaped field type it doesn't otherwise
recognize is to assume one exists; true for every Kubernetes-ecosystem type,
false here, and it errors at `go build` rather than at generation time. The
fix is a hand-written `DeepCopyInto`/`DeepCopy`, but not on `MachineSeccompProfile`
itself: that type is reached through `MachineConfig.MachineSeccompProfiles
[]*MachineSeccompProfile`, and deepcopy-gen's generated code for a slice of
pointers to a type with a *known* `DeepCopy` method assumes a value-typed
element and double-dereferences — a second, independent limitation. The
methods live one level down instead, on `pkg/typedconfig.SeccompProfile`, a
local type with `specs.LinuxSeccomp`'s exact field layout (so the JSON shape,
and therefore the CRD schema and the generic conversion helpers, are
unaffected) that appears only through a plain pointer field. This is the only
hand-maintained deep copy in `pkg/typedconfig`; it needs revisiting only if
specs-go's `LinuxSeccomp`/`LinuxSyscall` shape changes.

## Follow-up: nine fields that must not be representable

A security review of this same drop mechanism found a second, unrelated
reason to drop a field: not because it can't be represented, but because it
must not be. Nine machinery fields carry cluster secret material — join
tokens, CA key pairs, etcd and service-account encryption keys — reachable
through the legacy `Config` document the same as everything else:

`ClusterConfig.ClusterSecret`, `ClusterConfig.BootstrapToken`,
`ClusterConfig.ClusterAESCBCEncryptionSecret`, `ClusterConfig.ClusterCA`,
`ClusterConfig.ClusterAggregatorCA`, `ClusterConfig.ClusterServiceAccount`,
`EtcdConfig.RootCA`, `MachineConfig.MachineToken`, `MachineConfig.MachineCA`.

None of these are `meta.Unstructured` — most are plain strings or
`*x509.PEMEncodedCertificateAndKey` — so they would mirror cleanly. The
problem is what carrying them would let a `TalosConfigPatch` author do: any
patch, however narrowly scoped (a `role`-layer sysctl tweak, say), could set
one of these and have `configpatcher.Apply`'s strategic merge overwrite the
real value the secrets bundle generated, reaching a node as a cluster
takeover rather than a config error. This directly contradicts design
principle 1, "secrets are referenced, never embedded."

A third table, `secretFields` (`internal/tools/mirrorgen/main.go`, next to
`fieldOverrides`), drops these nine unconditionally — before the field's
static type is even inspected, unlike every other table here. It is kept
separate from `fieldOverrides` and from the type-keyed drop rule in `mapNamed`
on purpose, and the generator reports it separately
(`mirrorgen: dropped fields (secret material; ...)` vs. `(meta.Unstructured; ...)`):
the two tables answer different questions — *can't represent this* vs.
*must not represent this* — and a future contributor adding a row needs that
distinction to pick the right table.

**Side effect for `ichor adopt`:** `MachineConfig.MachineToken` dropping out
of the schema removes a constraint the CLI had flagged — `machine.token` had
no `omitempty` in machinery, so a sparse `Config` document (one that doesn't
carry every field a full machine config would) could never validate against
the mirror while that field stayed required, which stood in the way of
`adopt` capturing a legacy `Config` document sparsely. With the field gone
entirely, a sparse `Config` document is representable. This is outside this
change's scope (`adopt`'s CLI is a different component); noted here so the
CLI side can pick it up.

**CEL was considered and rejected.** A `+kubebuilder:validation:XValidation`
rule guarding these nine fields would only run against whatever the CRD
schema already let through; it protects only the fields someone remembered
to name in the rule, including every field `machinery` adds in a future
Talos release. Dropping the field from the schema entirely means there is no
path to forget.

**What "dropped" actually buys, measured rather than assumed.** It was tempting
to describe this as "the API server structurally rejects the request," matching
how this ADR already describes dropped fields in [Consequences](#consequences)
below. Measured against envtest's apiserver (same methodology as the size
numbers above: a real running API server, not a schema reading), that is not
what happens by default: a plain `client.Create` with no `fieldValidation`
option set logs `unknown field "..."` and returns a **nil** error — the nine
fields are *pruned*, not rejected, and the object is persisted without them.
`api/v1alpha1/validation_test.go`'s `"drops all nine secret fields..."` test
asserts exactly this (create succeeds, read-back has none of the nine) rather
than asserting rejection, because asserting rejection would currently be
asserting something false.

The security property Ichor actually needs — the attacker's value can never
reach a node — holds either way: pruned or rejected, the value never persists,
so `configpatcher.Apply` never sees it. What pruning does *not* give is a loud
failure the submitter can see; strict, server-side rejection of unknown fields
is a client-chosen request option (`fieldValidation=Strict`) that Ichor cannot
force on third-party callers (`kubectl`, Argo CD, Flux) writing a
`TalosConfigPatch` directly, and the standard Kubernetes-native way to force
it server-side — a validating admission webhook inspecting the raw request
body before pruning happens — was out of scope for this change (`internal/webhook/`
is excluded). **Left open:** whether that gap is acceptable as-is, given the
takeover is already prevented, or whether a follow-up should add such a
webhook.

**Round-trip test treatment.** `pkg/typedconfig`'s per-kind round-trip test
(machinery → mirror → machinery, YAML-identical) does not set any of these
nine fields, for the same reason it never set the other six always-dropped
`meta.Unstructured` fields: a value the mirror does not carry does not come
back out, so setting one in the fixture would fail the test on a field it was
never trying to cover. The trade-off this narrows: the generic round-trip test
no longer says anything about these nine fields specifically — it would pass
just as well if `ToMachinery`/`FromMachinery` mishandled them, because they're
simply absent from both sides of the comparison. That gap is covered instead,
and more precisely, by the schema-presence test above (the field cannot be
set at all) rather than by a round-trip equality the field was dropped to
avoid satisfying. `adopt`'s read path (machinery → mirror, reading an existing
cluster's config) is affected the same way: these nine fields are silently
omitted from the mirror it produces, which is the intended outcome — adopting
a running cluster must not copy its secrets into a `TalosConfigPatch` either.

## Measured again: served CRD size, with both follow-ups applied

`config/crd/bases/ichor.maesh.dev_talosconfigpatches.yaml` is now 180,091
bytes on disk. Measured the same way as before — a real envtest apiserver,
GET on the `CustomResourceDefinition` object itself, not a schema reading —
it serves **81,965 bytes, 5.2% of the 1,572,864-byte ceiling** (up from
69,455 bytes / 4.4%; the five new fields' schemas account for the
difference). Headroom remains in no way the constraint.

## Consequences

- Ichor owns a code generator and a conversion layer it would not have needed
  if embedding had worked. That was the cost the design accepted when it named
  this fallback, and it buys back the CRD schema and schema-driven UI forms.
  "Apiserver-side rejection of unknown fields" needs a correction, measured in
  the secret-fields follow-up above: by default the apiserver *prunes* an
  unknown field (logs a warning, persists the object without it) rather than
  rejecting the request; the value is kept out of storage either way, but a
  caller does not get a loud error unless it opts into strict field
  validation itself.
- Machinery upgrades become: regenerate, run the round-trip tests, read the
  diff. Field additions are backwards compatible; removals and retypes surface
  as build or test failures, which is what the self-upgrade pre-check in 5.7.3
  needs.
- The UI can drive forms from the generated CRD schema, or from the JSON Schema
  Talos publishes per version — both describe the same fields, and the latter
  is maintained upstream.
- The ten-entry substitution table is the only hand-written piece, and the
  round-trip test is what keeps it honest.

## Alternatives considered

**Embedding the machinery types as-is.** The design's first choice. Rejected on
the evidence above: it fails outright, and the failure is structural rather
than a long tail of fixable edge cases.

**Forking machinery to add JSON tags.** Would mean maintaining a fork of the
entire Talos config type tree across every Talos release — the parallel
catalogue the design set out to avoid, with extra steps.

**Using Talos's published JSON Schema as the CRD schema.** It is JSON Schema
draft 2020-12 and leans on `$ref`, neither of which is valid in a structural
schema; de-`$ref`ing it makes the size problem worse, and it deliberately
omits the `docgen:nodoc` fields. It stays useful for UI forms, not for the CRD.

**A raw-YAML or `x-kubernetes-preserve-unknown-fields` field.** Rejected by
principle 1 and the typed-only decision, and it is what the whole typed-config
approach exists to avoid.

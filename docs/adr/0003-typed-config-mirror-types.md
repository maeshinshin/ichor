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

## Consequences

- Ichor owns a code generator and a conversion layer it would not have needed
  if embedding had worked. That was the cost the design accepted when it named
  this fallback, and it buys back the CRD schema, apiserver-side rejection of
  unknown fields, and schema-driven UI forms.
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

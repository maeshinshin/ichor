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
  decision we control. **Measured, and the answer is comfortable**: the mirror
  for the v1alpha1 `Config` document renders a 114,115-byte CRD, 7.3% of etcd's
  1,572,864-byte `--max-request-bytes` ceiling; all three of phase 2's allowed
  kinds in one CRD come to 122,353 bytes. No splitting into
  `TalosNetworkPatch`-style CRDs is needed, and there is room for the remaining
  52 kinds many times over.
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

If that gap needs closing, the options in increasing cost are: mirror
`ResourcesConfig.Requests`/`Limits` as `corev1.ResourceList`, which Ichor
already depends on and which is one table entry rather than a catalogue; take
dependencies on the `k8s.io/kubelet`, `k8s.io/apiserver` and
`k8s.io/kube-scheduler` component-config modules to mirror four more; or revisit
the typed-only decision for pass-through fields specifically. The first is
cheap and uncontroversial. The last is a scope change and not the
implementation's call.

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

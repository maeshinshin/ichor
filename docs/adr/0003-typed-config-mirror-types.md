# 0003. Typed configuration through generated mirror types

Status: Accepted · Date: 2026-10-24 · Design section: 4.4 (Typed configuration)

Decided: Generate the Talos machinery mirror types instead of hand-maintaining a parallel field catalogue

## Context

The design assumed that `TalosConfigPatch.spec.documents[]` could embed the
Talos machinery config document types directly, so that the CRD's OpenAPI
schema came from Talos's own definitions and Ichor maintained no parallel
catalogue of fields. Whether that works was the design's first open question.

Against `github.com/siderolabs/talos/pkg/machinery v1.14.3` (a separate Go
module, tagged with the Talos release, MPL-2.0) and controller-gen, direct
embedding does not work, and does not work partially:

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
  decision we control. The size is comfortable. Two numbers
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

## What the generator drops

`v1alpha1.Config` has **eleven** `meta.Unstructured` fields:

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

Absent is the default. The cost is expressiveness. A value the user sets is
never persisted and never reaches a node, because the field is missing from
the CRD and the API server prunes it (a caller that opts into strict field
validation gets a rejection instead; see "Dropped means pruned, not rejected"
below). The most consequential one is `machine.kubelet.extraConfig`, which is a
commonly used field in hand-written Talos configuration; a user who needs it
cannot express it through Ichor today.

What closing the gap costs, with controller-gen v0.21.0 against the candidate
types (served sizes use this CRD's own file-to-served ratio):

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

Four constraints on closing it:

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
  CRD before any override.
- **There is no fourth option.** Every one of the eleven is still
  `meta.Unstructured` in Talos's *modern*, non-deprecated documents too, so
  moving the allow-list off the legacy `Config` document would relocate this
  wall rather than remove it.

Everything except `MachinePods` and `PluginConfiguration` would land the CRD
around 192 KB served, 12% of the ceiling — so headroom is not the constraint.
Generation friction and the generator's type-keyed design are.

## Field-level overrides mirror six of the eleven

mirrorgen has a second table, `fieldOverrides`
(`internal/tools/mirrorgen/main.go`), consulted before `mapType` ever sees a
field's static type. It exists because the type-keyed `substTable` *can't*
express this: all eleven dropped fields are `meta.Unstructured`, so a table
keyed by Go type can only say "drop `meta.Unstructured`" or "don't" — never
"drop ten of them, keep one." Keying by `"StructName.FieldName"` instead is
unambiguous and stays a small table, not a rewrite of the generator.

Five fields mirror as their upstream type, with no new dependency and a clean
CRD schema:

| Field | Mirrors as |
|---|---|
| `ResourcesConfig.Requests` | `corev1.ResourceList` |
| `ResourcesConfig.Limits` | `corev1.ResourceList` |
| `MachineSeccompProfile.MachineSeccompProfileValue` | `specs.LinuxSeccomp` (OCI runtime-spec) |
| `APIServerConfig.AuditPolicyConfig` | `audit/v1.Policy` |
| `AuthorizationConfigAuthorizerConfig.AuthorizerWebhook` | `apiserver/v1.WebhookConfiguration` |

Conversion goes through two generic helpers in `pkg/typedconfig/substitution.go`
(`mirrorToMachineryUnstructured` / `machineryToMirrorUnstructured`): marshal
the mirror value through its own JSON tags, land the result in
`meta.Unstructured.Object`, and back. This is a different shape from the
scalar substitutions above — those round-trip through machinery's own
YAML/TextMarshaler because the ten-entry table's types *define* their own
wire form; these fields don't have a machinery-defined wire form to defer to
(the whole reason they are `meta.Unstructured`), so JSON — the form every
upstream schema here already commits to — is what both directions agree on.

Of the remaining six, five stay dropped, for the reasons in the table above:
`KubeletExtraConfig` and `SchedulerConfig` need `allowDangerousTypes` (a
`float32`, package-wide, not field-scoped); `MachineBaseRuntimeSpecOverrides`
has untagged fields and an `interface{}`; `MachinePods` is ~332 KB served on
its own; `PluginConfiguration` has no single schema. The sixth,
`KubeletCredentialProviderConfig`, generates cleanly but needs a new
dependency (`k8s.io/kubelet`); it is mirrored as well, see its own section
below.

**`MachineSeccompProfileValue` needs a hand-written deep copy.** Its CRD
schema generates fine, but `controller-gen object:generate` (DeepCopy) doesn't:
`specs.LinuxSeccomp` has pointer and slice fields but, unlike the other
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

## Secret fields: nine that must not be representable

The drop mechanism has a second, unrelated reason to drop a field: not because
it can't be represented, but because it must not be. Nine machinery fields
carry cluster secret material — join tokens, CA key pairs, etcd and
service-account encryption keys — reachable through the legacy `Config`
document the same as everything else:

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
`fieldOverrides`), drops these unconditionally — before the field's static type
is even inspected, unlike every other table here. It is kept separate from
`fieldOverrides` and from the type-keyed drop rule in `mapNamed` on purpose,
and the generator reports it separately
(`mirrorgen: dropped fields (secret material; ...)` vs. `(meta.Unstructured; ...)`):
the two tables answer different questions — *can't represent this* vs.
*must not represent this* — and a contributor adding a row needs that
distinction to pick the right table.

**Side effect for `ichor adopt`:** `MachineConfig.MachineToken` has no
`omitempty` in machinery, so while the field stayed required a sparse `Config`
document (one that doesn't carry every field a full machine config would)
could never validate against the mirror, which would stand in the way of
`adopt` capturing a legacy `Config` document sparsely. With the field gone
entirely, a sparse `Config` document is representable.

**CEL was considered and rejected.** A `+kubebuilder:validation:XValidation`
rule guarding these nine fields would only run against whatever the CRD
schema already let through; it protects only the fields someone remembered
to name in the rule, including every field `machinery` adds in a future
Talos release. Dropping the field from the schema entirely means there is no
path to forget.

**Dropped means pruned, not rejected.** The API server does not structurally
reject a request that carries a dropped field. A plain `client.Create` with no
`fieldValidation` option set logs `unknown field "..."` and returns a **nil**
error: the nine fields are *pruned* and the object is persisted without them.

The security property Ichor needs — the attacker's value can never reach a
node — holds either way: pruned or rejected, the value never persists, so
`configpatcher.Apply` never sees it. What pruning does *not* give is a loud
failure the submitter can see; strict, server-side rejection of unknown fields
is a client-chosen request option (`fieldValidation=Strict`) that Ichor cannot
force on third-party callers (`kubectl`, Argo CD, Flux) writing a
`TalosConfigPatch` directly, and the standard Kubernetes-native way to force
it server-side — a validating admission webhook inspecting the raw request
body before pruning happens — is not part of this decision. **Open:** whether
that gap is acceptable as-is, given the takeover is already prevented, or
whether such a webhook should be added.

**Round-trip test treatment.** `pkg/typedconfig`'s per-kind round-trip test
(machinery → mirror → machinery, YAML-identical) does not set any of these
nine fields, for the same reason it never sets the other always-dropped
`meta.Unstructured` fields: a value the mirror does not carry does not come
back out, so setting one in the fixture would fail the test on a field it was
never trying to cover. The generic round-trip test therefore says nothing
about these nine fields specifically — it would pass just as well if
`ToMachinery`/`FromMachinery` mishandled them, because they are absent from
both sides of the comparison. That gap is covered instead, and more precisely,
by the schema-presence check (the field cannot be set at all) rather than by a
round-trip equality the field was dropped to avoid satisfying. `adopt`'s read
path (machinery → mirror, reading an existing cluster's config) is affected
the same way: these nine fields are silently omitted from the mirror it
produces, which is the intended outcome — adopting a running cluster must not
copy its secrets into a `TalosConfigPatch` either.

## `KubeletCredentialProviderConfig` has its override

`KubeletCredentialProviderConfig` is the one field that generates a clean CRD
schema but needs a new direct dependency (`k8s.io/kubelet`). Whether that
dependency is worth taking is a product call rather than a generator one, and
the answer is yes. `fieldOverrides` has a sixth entry,
`KubeletConfig.KubeletCredentialProviderConfig` mirrors as
`k8s.io/kubelet/config/v1.CredentialProviderConfig`, pinned at `v0.36.0` to
match every other `k8s.io/*` module in `go.mod` (`k8s.io/api`,
`k8s.io/apiserver`, `k8s.io/client-go`, etc. — all `v0.36.0`; a mismatched
`k8s.io/kubelet` would otherwise double up Kubernetes' own types in the
build).

Under a typed-only API, "absent" means "inexpressible," not "dependency
saved." `machine.kubelet.credentialProviderConfig` is a commonly used field in
hand-written Talos configuration (image-pull credentials for ECR, GCR,
ACR-style registries), so leaving it dropped would be a product gap, not a
saved dependency — the same class of gap as `extraConfig`. `k8s.io/kubelet` is
one more official `k8s.io/*` staging module, versioned in lockstep with
Kubernetes the same way the modules of the other mirrored overrides are
(`k8s.io/api`, `k8s.io/apiserver`'s two packages); it carries no transitive
dependency of its own that Ichor doesn't already have.

`machine.kubelet.extraConfig` stays dropped, for the reason the table above
gives: what blocks it is not the dependency, it is a `float32` field deep in
`kubelet/config/v1beta1`, which controller-gen only emits under
`allowDangerousTypes` — and that flag is package-wide, not field-scoped, so
taking it for one field would relax type safety across every CRD
controller-gen touches. `KubeletCredentialProviderConfig` has no such field;
that is why it, and not `extraConfig`, is the one worth taking.

**DeepCopy needs no wrapper here.** `k8s.io/kubelet/config/v1` is a Kubernetes
API type (`+k8s:deepcopy-gen` generated, same family as the other overrides'
modules), and its `zz_generated.deepcopy.go` carries
`DeepCopy`/`DeepCopyInto`/`DeepCopyObject` for `CredentialProviderConfig` and
every type nested under it. `MachineSeccompProfileValue`'s hand-written
wrapper (above) is needed only because OCI's `runtime-spec` has no such
generated methods; that is not this case, so no local wrapper is needed.

**Round-trip coverage.** `TestRoundTripConfig` (`pkg/typedconfig/roundtrip_test.go`)
sets `MachineKubelet.KubeletCredentialProviderConfig` to a non-trivial
`CredentialProviderConfig` (one provider, `defaultCacheDuration` and
`apiVersion` set) built through `mirrorToMachineryUnstructured`, the same
helper the generated `ToMachinery` conversion itself calls — so the fixture
takes the field through the override's full round trip, not just an empty
placeholder.

## Served CRD size

With the six overrides and the secret drops applied,
`config/crd/bases/ichor.maesh.dev_talosconfigpatches.yaml` is 227,641 bytes on
disk and the object the API server serves (a GET on the
`CustomResourceDefinition`, which is what etcd stores and what
`--max-request-bytes` limits) is **106,077 bytes, 6.7% of the 1,572,864-byte
ceiling**, up from 69,455 bytes / 4.4% before them. The
`KubeletCredentialProviderConfig` override alone adds about 24 KB, well above
the ~6.5 KB the table above estimates for it: that estimate is the field
generated in isolation with `controller-gen`'s file-to-served ratio, and
`CredentialProviderConfig` pulls in `CredentialProvider`,
`ServiceAccountTokenAttributes` and `ExecEnvVar` nested underneath it, each
carrying the same long upstream doc comments (several paragraphs per field,
explaining cache semantics and image-matching rules) that CRD schemas inline at
every use site with no `$ref` sharing — the size mechanism the Context section
identifies, realized on a type with unusually verbose upstream docs rather than
a flat one. Headroom is still not the constraint: the remaining five dropped
`meta.Unstructured` fields and 52 kinds outside the allow-list still fit
comfortably inside the ceiling.

## Secret-field and override keys use machinery's own type names

`typeRenames` is a third type-keyed table (after `fieldOverrides` and
`secretFields`). Phase 3 (disk roles, ADR 0012) added it to resolve the
generator's first mirror-struct name collision: `block.EncryptionKey` and three
sibling types share a bare name with the already-mirrored, unrelated legacy
`v1alpha1.EncryptionKey` family, so the `block` versions are renamed
`VolumeEncryptionKey` and so on on the mirror side.

`secretFields` and `fieldOverrides` are matched during the struct walk, and
`typeRenames` is applied to `name` *before* the field loop runs. A key built
from `name` would therefore see `VolumeEncryptionKey.KeyStatic` for the `block`
type, not `EncryptionKey.KeyStatic`: one `"EncryptionKey.KeyStatic"` entry
would drop the field from the legacy type only, and the modern side would keep
`KeyStatic` (and so the passphrase nested inside it) in the CRD schema, for all
three kinds that embed it (`VolumeConfig`, `UserVolumeConfig`,
`RawVolumeConfig`) — the exact category of hole `secretFields` exists to close,
reopened by the mechanism meant to keep mirror names unique.

**The key is structural, not another row.** `fieldKey` is built from
`named.Obj().Name()` — the real machinery type name, read directly off the
type being walked — instead of `name`, which may already have been
overwritten by `typeRenames`. `secretFields` and `fieldOverrides` are both
written against machinery's own names, because that is what a contributor
reads when deciding whether a field needs a row. Adding "a second entry under
the new name" was rejected as the fix: matching against a name a *different*,
unrelated table might have rewritten is the bug, and a second entry would
recur on the next rename. All three tables (`fieldOverrides`, `secretFields`,
`typeRenames`) are keyed by Go type or struct-field name; `typeRenames` is the
only one of the three allowed to see the post-rename name, since renaming is
its entire job.

**The guard covers the whole generated CRD.** `TestSecretFieldsAreNotInCRD`
(`internal/tools/mirrorgen/main_test.go`) runs the real generator, takes the
YAML name each `secretFields` entry reports dropping, and fails if any of them
exists anywhere in the real generated CRD under a matching shape (object vs.
scalar, see below for why shape, not just name, matters), reporting
`CRD schema still has a property secretFields says it dropped: [...]`. Adding
a `secretFields` row needs no change to this test to stay covered.

**Disk encryption stays representable.** Talos's disk encryption has four key
sources: `KeyStatic` (a passphrase written directly into the document),
`KeyKMS` (an endpoint, sealed/unsealed by a remote KMS server), `KeyTPM`
(sealed by the node's own TPM), and `KeyNodeID` (deterministically derived from
the node's UUID and partition label). Only `KeyStatic` carries a secret in the
document itself; the generated CRD has `kms`, `tpm` and `nodeID` all present
and usable on `UserVolumeConfig`, `RawVolumeConfig` and `VolumeConfig`'s
`EncryptionSpec.keys[]`. Dropping `KeyStatic` does not make disk encryption
inexpressible through Ichor; it leaves exactly the three key sources that never
put a secret in a CR in the first place, which is the outcome `secretFields`'
whole design is for. Anything that writes volume encryption through Ichor can
therefore use those three key sources.

## Further secret fields, and a report for the next one

**A sibling.** `ClusterConfig.ClusterAESCBCEncryptionSecret` (etcd's AES-CBC
encryption key) is in `secretFields`, and so is its sibling
`ClusterConfig.ClusterSecretboxEncryptionSecret` — etcd's other supported
encryption key, same struct, same shape, same reason to drop it.

**Four credentials that violate design principle 2.** Design principle 2
is "Secrets are referenced, never embedded."
`RegistryAuthConfig.{RegistryPassword, RegistryAuth, RegistryIdentityToken}`
(private registry pull auth), `VIPEquinixMetalConfig.EquinixMetalAPIToken`,
`VIPHCloudConfig.HCloudAPIToken` (cloud provider API tokens for VIP
election), and `DeviceWireguardConfig.WireguardPrivateKey` are representable
fields in the machinery types, and so would be writable into a
`TalosConfigPatch`, which the GitOps design treats as something that lives
in Git. These are different in kind from the first `secretFields`
entries: those exist to stop a `TalosConfigPatch` from
overriding a value the bundle generates (a join token, a CA key pair, a
disk encryption passphrase) — none of those is something a user
would ever *want* to write in a patch. These four are the opposite: a user
legitimately wants to set a private registry password, a cloud API token,
or a WireGuard key. That does not make embedding them in a document Git
sees any less a violation of principle 2. They are dropped in the same table,
with the reason text distinguishing the two cases so a future reader does not
mistake one for the other: the first set reads "etcd cluster bootstrap token" /
"disk encryption static passphrase" / etc. (bundle-value override
prevention); these four read "(user credential, not a bundle value)".

**What dropping registry auth costs, and what it doesn't.** VIP tokens and
the WireGuard key need no replacement — nothing in any current phase uses
them. Private registry auth is different: phase 10 (air-gap) needs to pull
images from an authenticated private registry, and dropping
`RegistryAuthConfig`'s credential fields with no replacement would make that
inexpressible through Ichor. The replacement is a reference form — a field
shaped like `TalosCluster.spec.secretsRef` already is, pointing at a
Kubernetes Secret the controller reads and injects, never a value the patch
document carries directly. That reference form, and the controller-side
plumbing that reads it, is a separate piece of work: ADR 0027.

**A report for the next missed secret.** A sibling field is easy to overlook
by hand, and that does not scale. `suspiciousSurvivors()`
(`internal/tools/mirrorgen/suspicious.go`) walks every mirrored field after
the real drop decisions are made and reports — to `go generate`'s stdout,
not by removing anything — any field whose Go or YAML name contains
`secret`, `token`, `key`, `password`, `passphrase` or `privatekey`
case-insensitively, and is not already in `secretFields`. It is a report,
not an enforcement mechanism, deliberately: a false positive here costs a
human one line to read and dismiss, while a false positive in the drop table
costs a field silently disappearing from the CRD. Reported and safe, because
none hold secret material and they just have "key" in the name:
`EncryptionKey.KeyKMS/KeyTPM/KeyNodeID/KeySlot/KeyLockToSTATE`,
`DeviceWireguardPeer.WireguardPublicKey`, `Bond.BondADUserPortKey`,
`FeaturesConfig.ApidCheckExtKeyUsage`, and `EncryptionConfig`/`EncryptionSpec`'s
`EncryptionKeySize`/`EncryptionKeys`. Undecided:
`PEMEncodedCertificateAndKey.Key`, left for a future reviewer to judge on its
own.

**The guard test matches by name and shape.** `TestSecretFieldsAreNotInCRD`
compares a candidate CRD property with a dropped field by YAML name *and* by
shape: `droppedSecretField` records the dropped field's own shape (`Scalar
bool`, computed from the Go field's type during the walk). Matching only
object-shaped properties would never check a plain-string drop, and matching
bare names hits real false positives: `machine.registries.*.tls.ca` is an
unrelated, legitimate plain base64 string also named `ca`, and
`RegistryConfig`'s own `auth` field (the object wrapping `RegistryAuthConfig`,
which legitimately still has a `username` property) collides on bare name with
the dropped *scalar* `RegistryAuthConfig.RegistryAuth` (also tagged `auth`, one
level deeper inside that same object). Shape distinguishes them: the wrapper
is an object, the dropped field is a scalar, so they do not match each other.

## Consequences

- Ichor owns a code generator and a conversion layer it would not have needed
  if embedding had worked. That was the cost the design accepted when it named
  this fallback, and it buys back the CRD schema and schema-driven UI forms.
  A dropped field is pruned, not rejected: by default the apiserver logs a
  warning and persists the object without the unknown field rather than
  rejecting the request. The value is kept out of storage either way, but a
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

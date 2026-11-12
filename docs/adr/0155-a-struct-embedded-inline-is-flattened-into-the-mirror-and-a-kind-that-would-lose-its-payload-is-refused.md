# 0155. A struct embedded inline is flattened into the mirror, and a kind that would lose its payload is refused

Status: Accepted · Date: 2026-11-13 · Design section: Typed configuration · Amends: 0150

Decided: a Talos document that embeds a struct with the YAML `inline` option has that struct's fields mirrored at the top level of the document, under the names Talos writes them with, and converted back by promotion. A pointer embed, an embed without `inline`, and two fields that collide on Go or YAML name stop generation. A shipped kind whose mirror has no field, or that loses a free-form (`meta.Unstructured`) field nobody decided to drop, also stops generation. The generated union carries the conversions the renderer and the UI backend used to write by hand. Fifty kinds are added to `shipped`; ten `accept` kinds stay out for the reasons below.

## Context

Seven of the 67 `accept` kinds (the bond, bridge, dummy, VRF and veth links, and the TCP and HTTP probes) embed `network.CommonLinkConfig` or `network.CommonProbeConfig` by value with `yaml:",inline"`; the veth document embeds the link struct twice, once in itself and once in its peer. The generator refused any embedded field except the document identity. Two other things stood between a kind and a working field in the CRD: the renderer and the UI backend each kept a hand-written table of shipped kinds, and a kind whose only content is a free-form field is accepted by the CRD and renders as an empty document.

## Decisions

1. **Flatten, do not nest.** Talos writes an inline struct's fields at the top level of the document. A mirror that nested them under a field of their own would give a TalosConfigPatch a different shape from the Talos document it carries, so an author could not copy a Talos document into it. Flattening also leaves the schema one object smaller per embed; either way the fields are written out in full in each document that has them, since a CRD schema has no references. The conversion names a flattened field as `out.LinkUp`, which Go resolves through the embedded value, so no conversion code is generated for the embed itself.
2. **A field is looked up under the struct that declares it.** The secret, override and sparse tables are keyed by the declaring struct, so a row written for `CommonLinkConfig` applies to every document that embeds it.
3. **What is refused.** An embed without `inline` is a named field in YAML, a pointer embed is nil until set and conversion could not promote through it, and two fields with one Go or YAML name could not both be written. None occurs in the shipped kinds. A struct with an unexported field and no entry in the substitution table was mirrored as an empty struct; it is now refused. `meta.URL` (a string behind a custom marshaller, embedding a pointer to `net/url.URL`) and `net/netip` addresses and prefixes are scalar substitutions like the other address types.
4. **A kind that loses its payload is not shipped.** A `meta.Unstructured` field has no schema, and carrying it would be the raw-YAML escape hatch, so the mirror drops it. For a kind that is useful without it that is a loss to record; for a kind that is nothing but it, every document is accepted and renders empty. `droppedPayloads` holds one row, with a reason, for each such field a shipped kind loses; generation and a test fail, naming the kind and the field, for a shipped kind with a mirror that has no fields or with an unrecorded drop, and for a row that no longer matches anything.
5. **The union generates its conversions.** Next to `Validate`, mirrorgen emits `Document` (the typed document to the machinery document of its kind), `TalosConfigPatchDocumentFromMachinery` (the inverse) and `TalosConfigPatchDocumentFieldKey` (kind to JSON field name). The renderer and the UI backend call them. Adding a kind is its `kindClasses` row and its `shipped` entry, as ADR 0150 said, now without a third and fourth place to edit. The UI's list of kinds it offers an editor for stays hand-written, with its help text.
6. **A version marker is not a word.** `fieldName` lowered `DHCPv6Config` to `dhcPv6Config`; a lowercase letter followed by a digit now stays with the capital run, giving `dhcpv6Config`. The field name is API and cannot change once shipped, so the names of the kinds with a leading capital run are fixed by tests.
7. **Round trip is checked for every shipped kind.** A document filled in every field the mirror carries must come back from the mirror's JSON form to the same YAML Talos itself writes for it, and the mirror's JSON must have the key layout of that YAML.

## Held back

- **Nine kinds carry a free-form payload**: KubeletConfig, KubeProxyConfig, KubeSchedulerConfig, KubeAdmissionControlConfig, KubeAuditPolicyConfig, KubeAuthenticationConfig, KubeAuthorizerConfig, KubeCredentialProviderConfig and KubeStaticPodConfig. Three of them would have no field at all. Carrying the payload means mirroring an upstream Kubernetes type (for the static pod, the Pod type, whose schema is several hundred kilobytes), which is a decision about the size of the CRD and not a change to the generator. They stay `accept` and unshipped until it is made.
- **KubeExternalManifestConfig** has a `headers` map whose usual content is an authorization header. ADR 0152 dropped the same map from the legacy document as a credential; the same decision has to be made for this kind before it ships.

## Rejected

- Mirroring the embedded struct as a named field: it changes the document's shape and gains no schema size.
- Promoting a pointer embed by allocating it: a pointer embed that is nil would convert to an empty struct and write a key Talos would not.
- Recording the gate in the classification table: a kind is `accept` for what it is, and whether its mirror loses a field depends on the walk.

## Consequences

`TalosCluster` embeds the union, so each shipped kind is paid twice in CRD size; the CEL cost does not grow with kinds (ADR 0154). A kind that gains a free-form field in a later Talos release fails generation until the drop is recorded, which is the point. A shipped kind's mirror has no per-kind code to review beyond its table rows.

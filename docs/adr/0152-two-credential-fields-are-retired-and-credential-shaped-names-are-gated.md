# 0152. Two credential fields are retired and credential-shaped names are gated

Status: Accepted, amended · Date: 2026-11-12 · Design section: Typed configuration · Builds on: 0003, 0012, 0099, 0101

Decided: the registry client identity (`machine.registries.config.<host>.tls.clientIdentity`, a certificate and a PEM private key) and `cluster.extraManifestHeaders` (HTTP headers, usually `Authorization`) are dropped from the v1alpha1 `v1alpha1` document mirror and retired from the `TalosConfigPatch` and `TalosCluster` CRDs through the installer's retired-field list, as an exception to the append-only rule; and the generator's suspicious-name scan is now a test that fails on any credential-shaped field that is neither dropped nor on a reviewed list with a reason. Amended 2026-11-15: `TalosCluster.spec.cni.custom.documents`, the deprecated and unused second copy of the document union, is retired whole by the same exception; `spec.cni.custom` and `spec.cni.custom.source` stay.

## Context

`secretFields` (`internal/tools/mirrorgen/main.go`) drops the credential fields of the mirrored Talos types so a credential cannot be stored in a CR (design principle 2, ADR 0003, ADR 0012). Two fields were missed and shipped in v0.1.0, v0.1.1 and v0.2.0:

- `PEMEncodedCertificateAndKey.Key`, reached through `RegistryTLSConfig.clientIdentity`. The same key is refused when it arrives as a `RegistryTLSConfig` document, because that kind is classified `secret` (ADR 0149); the v1alpha1 document was the way around it.
- `ClusterConfig.ExtraManifestHeaders`, a map sent as HTTP headers when Talos fetches `extraManifests`. Its normal use is a bearer token or basic-auth header for a private source.

The generator already printed `PEMEncodedCertificateAndKey.Key` among its "suspicious name" survivors, in a list of seventeen, and nothing failed. The headers field has no credential word in its name and was never listed.

## Decisions

1. **Drop `RegistryTLSConfig.TLSClientIdentity` whole**, not only its key. A client certificate without its key cannot be used, and keeping `crt` alone is confusing.
2. **Drop `ClusterConfig.ExtraManifestHeaders` whole.** A map of header names to values cannot be split into harmless and secret entries, and the common value is a credential. `extraManifests` itself stays: public sources need no header.
3. **Retire both from the CRDs, as an exception to the append-only rule.** The rule protects stored objects and installed builds from a field vanishing. Here the field is the hole: keeping it in the schema keeps a place to store a private key in a CR, and a webhook refusal would guard only the paths that remember to call it (the reason ADR 0012 rejected CEL for this class). ADR 0101 set the mechanism for a deliberate removal: an explicit entry per path in `retiredFields` (`internal/installer/crdcheck.go`) and one ADR. This is that ADR. Four paths, two on each CRD that embeds the mirror (`TalosClusters` through the deprecated `spec.cni.custom.documents[]`).
4. **Stored objects.** Once the property is out of the schema the API server prunes it, so a stored `TalosConfigPatch` that carried either field renders without it from the next read. A node that relied on a registry client certificate or manifest headers loses them at the next apply. `kubectl apply` of a manifest that still has the field is refused by its default strict field validation. There is no replacement yet: a reference form (a Secret the controller resolves at render time, like `TalosCluster.spec.secretsRef`) is the follow-up already recorded for the registry credentials in ADR 0012.
5. **The scan becomes a test.** `TestSuspiciousSurvivorsAreReviewed` runs the generator and fails when a surviving field matches `suspiciousKeywords` and is neither in `secretFields` nor in `reviewedSurvivors` (`internal/tools/mirrorgen/suspicious.go`), which holds a reason per entry. It fails as well on an entry that matches no field, so the list cannot go stale. The keywords gain `credential`, `auth`, `header`, `bearer` and `cookie`.

## Rejected

- Keeping both fields deprecated and unused, refused in the webhook. It leaves a private key storable whenever the webhook is not consulted, and keeps the credential in objects already stored.
- Dropping only the `key` member. Leaves a half identity.
- Allowing `extraManifestHeaders` for non-secret headers. The field cannot say which are which.

## Consequences

Anyone using a registry client certificate or manifest headers in a patch must move them out of the CR before upgrading; they are applied to the nodes outside Ichor until a reference form exists. A Talos bump that adds a credential-shaped field fails `go test ./internal/tools/mirrorgen` until a maintainer drops it or lists it with a reason.

## Amendment (2026-11-15): spec.cni.custom.documents is retired

Decided by the coordinator, not the maintainer: `TalosCluster.spec.cni.custom.documents` is retired as a second exception to the append-only rule. This extends this ADR rather than opening a new one because the rule, the mechanism (an entry in `retiredFields`) and the consequence for stored objects are the ones above; only the reason differs. Here the field is not a hole but dead weight.

**Why.** ADR 0099 decision 8 and ADR 0101 deprecated `spec.cni.custom` and kept it only because CRD fields are append-only. Its `documents` member is the whole `TalosConfigPatchDocument` union a second time, and nothing renders it. In the chart's Helm release record (ADR 0158's method) the field and the words about it in the doc comment of `custom` take 392,672 bytes, 40% of the record: the record goes from 991,560 to 598,888 bytes, leaving 449,688 of the 1,048,576 free instead of 57,016. ADR 0165 sets out what that room is needed for.

**Why only `documents`.** The rest of `custom`, which is `source` (`repoURL`, `chart`, `path`, `targetRevision`), takes 1,256 bytes of the record. Removing it too would save that and lose three things. The webhook's refusal ("spec.cni.custom is deprecated and unused: Cilium is the only CNI") would be replaced by the API server's unknown-field error, because strict field validation answers before admission runs. The CEL rule `custom must be set when, and only when, preset is custom` names `custom` and would have to change. A stored object would lose `source` as well as `documents`. An empty `custom` object would serve nothing, so `custom.source` stays as the deprecated, refused field it was.

**Can a stored object hold it?** `spec.cni.custom` can only be set together with `preset: custom` (the CEL rule above), `preset` cannot change after create, and the webhook has refused `preset: custom` and a populated `spec.cni.custom` on create since 2026-10-29 (7291ee156). Every tagged release, from v0.1.0 on, is later than that. The webhook also refuses any spec change to a stored `flannel` or `custom` cluster. So the field could be stored only by an object created between 2026-10-25, when `spec.cni` was added, and 2026-10-29, or by a create that bypassed the webhook. One live cluster holds one `TalosCluster`, with `preset: cilium` and no `custom`; the kind cluster `ichor-local` holds none; the repository ships no manifest, sample, fixture or example that sets it.

**Stored objects.** The API server prunes a field that is no longer in the schema when it decodes an object from storage as well as when it writes one (`schemaCoercingDecoder` wraps the storage codec in `k8s.io/apiextensions-apiserver` v0.36.6), so a stored object stops serving `documents` as soon as the new CRD is established, and the bytes in etcd lose it at the object's next write. Nothing used it, so no node changes. `kubectl apply` of a manifest that still carries `documents` is refused by its default strict field validation. The installer's additive check (`retiredFields`) now lists `spec.cni.custom.documents` once, which also covers the two credential paths this ADR first listed under it.

**Consequences.** There is no replacement and none is needed: a custom CNI is not an Ichor feature. A manifest or script that sets `spec.cni.custom.documents` must stop; the cluster it describes was already refused.

## Amendment (2026-11-16): TalosConfigPatch.spec.documents is retired

Decided by the maintainer: `TalosConfigPatch.spec.documents` is retired as a third exception to the append-only rule. The mechanism is the one above, an entry in `retiredFields`, and so is the consequence for stored objects; the reason is the one of ADR 0165, that `spec.config` is the only place a patch carries Talos configuration.

**Why.** The field is the `TalosConfigPatchDocument` union as a list, which `spec.config` carries a second time in Ichor's own shape. Both together took the chart's Helm release record to 950,492 of the 1,048,576 bytes allowed (ADR 0158's method); `spec.config` alone is 580,420, with 468,156 free.

**Stored objects.** A `TalosConfigPatch` that sets `documents` loses it as soon as the new CRD is established, and the nodes it reached lose its settings at their next apply. The maintainer was told this, and accepted it, for every cluster that has such a patch. The upgrade notes say so and say what to write instead. A client that asks for strict field validation is refused with an unknown-field error; one that does not has the field pruned. The installer's additive check lists `spec.documents` of `talosconfigpatches.ichor.maesh.dev`, and a test shows the check refuses the upgrade without that entry.

**What stays.** The `TalosConfigPatchDocument` Go type, which the conversion of `spec.config` decodes into.


# 0152. Two credential fields are retired and credential-shaped names are gated

Status: Accepted · Date: 2026-11-12 · Design section: Typed configuration · Builds on: 0003, 0012, 0101

Decided: the registry client identity (`machine.registries.config.<host>.tls.clientIdentity`, a certificate and a PEM private key) and `cluster.extraManifestHeaders` (HTTP headers, usually `Authorization`) are dropped from the legacy `v1alpha1` document mirror and retired from the `TalosConfigPatch` and `TalosCluster` CRDs through the installer's retired-field list, as an exception to the append-only rule; and the generator's suspicious-name scan is now a test that fails on any credential-shaped field that is neither dropped nor on a reviewed list with a reason.

## Context

`secretFields` (`internal/tools/mirrorgen/main.go`) drops the credential fields of the mirrored Talos types so a credential cannot be stored in a CR (design principle 2, ADR 0003, ADR 0012). Two fields were missed and shipped in v0.1.0, v0.1.1 and v0.2.0:

- `PEMEncodedCertificateAndKey.Key`, reached through `RegistryTLSConfig.clientIdentity`. The same key is refused when it arrives as a `RegistryTLSConfig` document, because that kind is classified `secret` (ADR 0149); the legacy document was the way around it.
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

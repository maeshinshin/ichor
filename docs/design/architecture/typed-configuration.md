---
sidebar_group: "Configuration"
sidebar_order: 7
---

# Typed configuration

`TalosConfigPatch.spec.documents[]` holds the Talos config documents as typed
fields, restricted to an allow-list of kinds. There is no raw-YAML field. The
reasons:

- Machinery's types are Talos's own definition; we don't maintain a parallel
  catalogue that drifts.
- A CRD schema keeps an unknown field out of storage — by default the API
  server prunes it (logs a warning, persists the object without it) rather
  than rejecting the request, but either way the value a field's absence was
  meant to keep out never reaches a node.
- A newer machinery renders config for older Talos versions when a version
  contract is set. The reverse doesn't hold, so supporting a newly released
  Talos means bumping machinery.

The types cannot be embedded directly. Machinery is YAML-first and carries no
JSON tags, so controller-gen refuses them outright, and several field types
(`ByteSize`, CEL expressions, the `netip` wrappers in the network documents)
have no exported fields to describe. Instead the mirror types are generated
from machinery with a `go generate` tool, together with conversion in both
directions and a round-trip test per kind; machinery stays the only place
where the field catalogue lives.

A handful of fields need the generator's type-keyed substitution and drop
rules to be overridden per field instead, because the type alone can't say
what to do with them:

- Eleven fields are `meta.Unstructured` — Talos's own escape hatch for a
  pass-through to another project's schema (kubelet's configuration, an
  audit policy, a resource list, a seccomp profile). A type-keyed rule can
  only drop all eleven or none; a field-level override table drops five
  (controller-gen errors, or no single schema to mirror) and mirrors six as
  their upstream type instead: five with no new dependency, plus
  `machine.kubelet.credentialProviderConfig`, which takes one (`k8s.io/kubelet`,
  an official `k8s.io/*` staging module versioned in lockstep with
  Kubernetes, the same class of dependency the other five come from). Under
  a typed-only API an absent field is an inexpressible one, so a commonly
  wanted field being absent is a product gap, not a saved dependency —
  that's why this one took the dependency where the other five didn't need
  to. `machine.kubelet.extraConfig` still gets nothing: what blocks it was
  never a dependency but a `float32`, which controller-gen only emits under
  `allowDangerousTypes` — and that flag applies to the whole API package, so
  one field would relax the type safety of every CRD in it.
- Nine more fields are plain strings or certificate pairs, not
  `meta.Unstructured`, but carry cluster secret material — join tokens, CA
  key pairs, encryption keys. These are dropped unconditionally, by a
  separate table, for a different reason than the eleven above: not because
  they can't be represented, but because a `TalosConfigPatch` must not be
  able to overwrite the real values the secrets bundle generates, however
  narrowly the patch claims to be scoped.

Both are the follow-up.

No validating admission webhook rejects a `TalosConfigPatch` carrying a
secret field. Dropping the fields from the schema is what keeps the value
out of storage, and that holds whatever the caller does, so a webhook adds
nothing to the security property and only improves the error message. In
exchange it would put `failurePolicy` on the write path: `Fail` means a
controller outage blocks every configuration change, `Ignore` means the
guard is absent exactly when it is invoked. Ichor's own clients ask for
strict field validation instead, so the CLI and the UI backend report the
field loudly, and `kubectl` is strict by default. A client that opts out of
strict validation gets pruning, which is the safe outcome anyway.

Ichor adds what the types can't carry — whether a field needs a reboot,
whether it can only be set at install time, which Talos version introduced
it — in a table keyed by field path. Unknown paths are treated as
reboot-required.

`adopt` reads existing node configs with the same types, so an existing cluster
imports as-is. Kinds outside the allow-list are listed, and the user chooses
between extending the list and dropping them. Nothing is dropped silently.

Typed hardening options for the clusters Ichor creates, all off by default and
rendered only when set: `spec.security.secureBoot` (`Off`, `Preferred`,
`Required`) picks the SecureBoot installer for approval and for each Talos upgrade
and holds the first configuration of a node without SecureBoot under `Required`;
`spec.security.kernelLockdown` is checked against what each node reports; both
surface as per-node and cluster conditions, and a node's state is read from its
`SecurityState`, never changed in place. A reset with `spec.node.wipeMode`
`system-disk` or `all` (a TalosOperation of kind node; the CLI has no flag for it)
wipes more than STATE and EPHEMERAL so a node can be installed afresh from the
SecureBoot ISO.
`spec.security.kubeletServingCertificates` renders `serverTLSBootstrap` and the
kube-apiserver's kubelet CA, and the controller approves each kubelet's
serving-certificate request itself, only for that node's own names and addresses,
read from the node.

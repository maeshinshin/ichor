---
sidebar_group: "Configuration"
sidebar_order: 7
---

# Typed configuration

![How typed configuration is built and used. At build time Talos machinery feeds mirrorgen, which makes the mirror types, and ichorgen with the place.go rows, which writes pkg/ichorconfig and the CRD schema of spec.config. At render time a TalosConfigPatch's spec.config goes through the controller's render, which reads each field's ichor tag, into Talos documents and then to the node.](../../images/typed-configuration.light.drawio.svg)

*Machinery is the only catalogue of fields; the schema and the conversion are generated from it.*

`TalosConfigPatch.spec.config` is the only place a patch carries Talos
configuration. It is typed, has no raw-YAML field, and writes each setting once.
`spec.documents[]` is gone from the CRD: a client that asks for strict field
validation (`kubectl` and every Ichor client) refuses an object that still sets
it, and one that does not prunes it.

## The schema: `spec.config`

One nested object in the spirit of the `v1alpha1` document: `machine` and
`cluster` sections, with `debug` and `persist` beside them as in `Config`. Every
setting Talos accepts through an allowed document is in it, once, where its
meaning puts it:

```yaml
spec:
  config:
    machine:
      sysctls: {net.ipv4.ip_forward: "1"}      # was SysctlConfig
      time: {enabled: true, ntp: {servers: [time.example.org]}}  # was TimeSyncConfig
      kernel:
        modules:                               # was one KernelModuleConfig each
          nbd: {}
          br_netfilter: {parameters: [nf_conntrack_helper=1]}
      volumes:
        user:                                  # was one UserVolumeConfig each
          data: {provisioning: {...}}
    cluster:
      network: {podSubnets: [10.244.0.0/16]}   # was KubeNetworkConfig
      coreDNS: {enabled: false}                # was KubeCoreDNSConfig
```

It is generated. `internal/tools/ichorgen` reads the mirror types `mirrorgen`
makes from machinery and the rows of `place.go` (one per document kind) and
writes `pkg/ichorconfig`. A row says where a kind goes; fields and types come
from the mirror. A field Talos adds appears at the next `make generate`, with no
row edited.

- **A kind that is a named thing is a map from its name**: `machine.kernel.modules`,
  `machine.volumes.user`, `machine.network.bonds`, `cluster.inlineManifests` and
  the rest, keyed by the document's `name`. The API server's authorizers are the
  one list, because their order matters.
- **A kind that is one thing joins the object of the same meaning**: KubeletConfig
  and the v1alpha1 `machine.kubelet` are one `machine.kubelet`. Settings of a
  kind placed beside one of the same meaning are joined by name; a setting two
  documents spell is one field.
- **Where Talos spells a setting two ways, Ichor takes the document's spelling**,
  because documents are where Talos adds fields. `machine.time.disabled` is
  `machine.time.enabled`, `cluster.coreDNS.disabled` is `coreDNS.enabled`,
  `cluster.apiServer.certSANs` is `certExtraSANs`; nameservers are `ResolverConfig`
  objects; registry mirrors, hosts, extra manifests and inline manifests are the
  keyed forms of their documents. The v1alpha1 settings left out for this reason
  are in `pkg/ichorconfig/testdata/not_carried.txt`, and a test fails when a
  setting of a document is in neither the schema nor that file.
- **A field is optional wherever its object is shared.** A patch is a sparse
  overlay: a field a document requires is required in the schema only where that
  document is the only one whose fields the object holds. Talos validates the
  whole configuration at render.
- **Descriptions are Ichor's own.** The schema carries no text of Talos's.

## Render

The controller converts `spec.config` to Talos documents through the mirror types,
reading each field's `ichor` struct tag, which names the documents and paths the
setting is written to.

- A setting a document spells goes to the document; one only `v1alpha1` has goes
  there. Talos refuses a Kubernetes component document (`KubeletConfig`,
  `KubeAPIServerConfig` and the rest of Talos 1.14) beside the `v1alpha1` block of
  the same component, so a patch that sets a document-only field and a
  `v1alpha1`-only field of one object is refused at render with Talos's message.
  A switch set to false goes to `v1alpha1` with the rest of its object, for the
  same reason.
- The Kubernetes documents need Talos 1.14 or later. For an earlier machine a
  setting both spell goes to `v1alpha1`, inline and extra manifests become that
  document's lists, and a document-only setting is refused, named.
- A document only a control plane takes (API server, controller manager,
  scheduler and the rest) is left out of a worker, so a cluster-layer patch can
  set the API server for the control planes it reaches.
- `machine.features.kubePrism.enabled` and `kubernetesTalosAPIAccess.enabled`
  are switches: true brings the document into being.
- Ichor's guards run on `spec.config` first: its CNI inline manifest is dropped
  once the cluster has handed over, Talos API access is left out of workers, and
  `cluster.proxy.disabled` is Ichor's.
- `spec.containerd.deviceOwnershipFromSecurityContext` on the `TalosCluster`
  (bool, default false; ADR 0172) renders one `CRICustomizationConfig`,
  `ichor-device-ownership`, on every node. It sets containerd's
  `device_ownership_from_security_context`, so a pod's `runAsUser` and `fsGroup`
  decide device ownership. It needs the v1.14 contract; the webhook and render
  refuse true under an older one. A change restarts containerd with no reboot,
  and only containers created afterwards benefit. Set it with
  `ichor set containerd-device-ownership true|false` or the cluster page's
  switch; both write that field as the caller. Ichor owns the TOML: a patch cannot reach the kind.

**Not yet joined.** The links: `machine.network.interfaces[]` is the `v1alpha1`
list, and the link documents (bonds, bridges, dummy links, veths, VRFs, Ethernet
tuning, DHCPv6, Layer 2 VIPs) are collections beside it. A bond can be written in
both, with different spellings (mode, hash policy and LACP rate are words in one
and numbers in the other; routes are `network` or `destination`). Joining them
takes a row per pair and a conversion per setting. `machine.disks` and
`machine.systemDiskEncryption` stand in the same relation to the volume
documents. So "one place to write every setting" is not yet true of bonds,
bridges and the other link documents, nor of disks and disk encryption.

## The typed mirror

The `TalosConfigPatchDocument` Go type, one field per document kind, is what the
conversion decodes into and validates with; it is no longer a field of any CRD.
Typed fields because:

- Machinery's types are Talos's own definition, so no parallel catalogue drifts.
- A CRD schema keeps an unknown field out of storage (the API server prunes it
  with a warning by default), so a value that must not reach a node never does.
- A newer machinery renders config for older Talos versions under a version
  contract; the reverse does not hold, so supporting a new Talos means bumping
  machinery.

Machinery is YAML-first with no JSON tags, so controller-gen refuses it outright,
and several field types (`ByteSize`, CEL expressions, the `netip` wrappers) have
no exported fields to describe. So `mirrorgen` generates the mirror types from
machinery, with conversion both ways and a round-trip test per kind.

Two override tables handle what the type alone cannot say:

- **Eleven `meta.Unstructured` fields** (Talos's pass-through to another
  project's schema: kubelet configuration, an audit policy, a resource list, a
  seccomp profile). A type-keyed rule can only drop all or none, so a field-level
  table drops five (controller-gen errors, or no single schema to mirror) and
  mirrors six as their upstream type: five with no new dependency, plus
  `machine.kubelet.credentialProviderConfig`, which takes `k8s.io/kubelet` (an
  official staging module versioned with Kubernetes). Under a typed-only API an
  absent field is an inexpressible one, so a commonly wanted field's absence is a
  product gap. `machine.kubelet.extraConfig` still gets nothing: a `float32`
  blocks it, and controller-gen emits one only under `allowDangerousTypes`, a
  flag that applies to the whole API package.
- **Nine secret fields** (join tokens, CA key pairs, encryption keys), plain
  strings or certificate pairs, dropped unconditionally by a separate table: a
  `TalosConfigPatch` must not be able to overwrite what the secrets bundle
  generates, however narrowly scoped.

No validating webhook rejects a patch carrying a secret field. Dropping the
fields from the schema keeps the value out of storage whatever the caller does, so
a webhook adds nothing to the guarantee and only a better message, at the price
of `failurePolicy` on the write path (`Fail` blocks every configuration change
during a controller outage; `Ignore` leaves the guard absent exactly when it is
invoked). Ichor's clients ask for strict validation, so the CLI and the UI
backend report the field loudly, and `kubectl` is strict by default. A client
that opts out gets pruning, which is safe.

A table keyed by field path records what the types cannot: whether a field needs
a reboot, whether it can only be set at install time, and which Talos version
introduced it. An unknown path is treated as reboot-required.

`adopt` reads existing node configs with the same types, so a cluster imports
as-is. Kinds outside the allow-list are listed, and the user chooses between
extending the list and dropping them; nothing is dropped silently.

## Hardening options

Typed, all off by default, rendered only when set:

- `spec.security.secureBoot` (`Off`, `Preferred`, `Required`) picks the SecureBoot
  installer for approval and for each Talos upgrade, and under `Required` holds
  the first configuration of a node without SecureBoot.
- `spec.security.kernelLockdown` is checked against what each node reports. Both
  surface as per-node and cluster conditions; a node's state is read from its
  `SecurityState`, never changed in place.
- `spec.security.kubeletServingCertificates` renders `serverTLSBootstrap` and the
  kube-apiserver's kubelet CA, and the controller approves each kubelet's
  serving-certificate request itself, only for that node's own names and
  addresses, read from the node.

## Reset wipe modes

A reset (a `TalosOperation` of kind node with `spec.node.wipeMode`, or `ichor
delete node --wipe-mode`) can wipe more than STATE and EPHEMERAL, so a node can
be installed afresh from the SecureBoot ISO.

| `wipeMode` | Erases |
|---|---|
| `system-partitions` (the default) | STATE and EPHEMERAL only |
| `system-disk` | the whole system disk, boot and EFI included; user disks stay |
| `all` | the same as `system-disk`; it does not mean every disk |
| `system-disk-and-user-disks` | the system disk and every user disk |

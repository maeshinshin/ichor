---
sidebar_group: "Configuration"
sidebar_order: 7
---

# Typed configuration

![How typed configuration is built and used. At build time Talos machinery feeds mirrorgen, which makes the mirror types, and ichorgen with the place.go rows, which writes pkg/ichorconfig and the CRD schema of spec.config. At render time a TalosConfigPatch's spec.config goes through the controller's render, which reads each field's ichor tag, into Talos documents and then to the node.](../../images/typed-configuration.light.drawio.svg)

*Machinery is the only catalogue of fields; the schema and the conversion are generated from it.*

`TalosConfigPatch.spec.config` is the only place a patch carries Talos
configuration: typed, no raw-YAML field, each setting written once.
`spec.documents[]` is gone; a strict-validating client (`kubectl`, every Ichor
client) refuses an object that sets it, and others prune it.

## The schema

One nested object in the spirit of the `v1alpha1` document: `machine` and
`cluster`, with `debug` and `persist` beside them as in `Config`.

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

`internal/tools/ichorgen` reads the mirror types `mirrorgen` makes from machinery
and the rows of `place.go` (one per document kind) and writes `pkg/ichorconfig`.
A field Talos adds appears at the next `make generate`, with no row edited.

| Rule | Detail |
|---|---|
| A named kind is a map from its name | `machine.kernel.modules`, `machine.volumes.user`, `machine.network.bonds`, `cluster.inlineManifests` and the rest, keyed by the document's `name`. API server authorizers are the one list (order matters). |
| A single kind joins its object | KubeletConfig and v1alpha1 `machine.kubelet` are one `machine.kubelet`; a setting two documents spell is one field. |
| The document's spelling wins | `machine.time.enabled`, `cluster.coreDNS.enabled`, `cluster.apiServer.certExtraSANs`; nameservers are `ResolverConfig` objects; mirrors, hosts, extra and inline manifests are keyed forms. Left-out v1alpha1 settings are in `pkg/ichorconfig/testdata/not_carried.txt`; a test fails when a setting is in neither the schema nor that file. |
| Fields are optional where the object is shared | A patch is a sparse overlay; Talos validates the whole configuration at render. |
| Descriptions are Ichor's own | No text of Talos's. |

## Render

The controller converts `spec.config` to documents through the mirror types; each
field's `ichor` struct tag names the documents and paths it is written to.

- A setting a document spells goes to the document; one only `v1alpha1` has goes
  there. Talos refuses a Kubernetes component document (`KubeletConfig`,
  `KubeAPIServerConfig`, ...) beside the `v1alpha1` block of that component, so a
  patch mixing a document-only and a `v1alpha1`-only field of one object is
  refused at render with Talos's message. A switch set to false goes to
  `v1alpha1` with the rest of its object.
- The Kubernetes documents need Talos 1.14 or later. Earlier, a setting both
  spell goes to `v1alpha1`, inline and extra manifests become its lists, and a
  document-only setting is refused, named.
- A control-plane-only document (API server, controller manager, scheduler, ...)
  is left out of workers, so a cluster-layer patch can set it.
- `machine.features.kubePrism.enabled` and `kubernetesTalosAPIAccess.enabled`
  are switches: true brings the document into being.
- Guards run first: Ichor's CNI inline manifest is dropped after hand-over, Talos
  API access is left out of workers, `cluster.proxy.disabled` is Ichor's.
- `TalosCluster.spec.containerd.deviceOwnershipFromSecurityContext` (bool, default
  false; ADR 0172) renders `CRICustomizationConfig` `ichor-device-ownership` on
  every node, so a pod's `runAsUser` and `fsGroup` decide device ownership. Needs
  the v1.14 contract (webhook and render refuse true otherwise). A change restarts
  containerd without a reboot, and only new containers benefit. Set it with `ichor
  set containerd-device-ownership true|false` or the cluster page switch; a patch
  cannot reach the kind.

**Not yet joined**: the links. `machine.network.interfaces[]` (`v1alpha1`) and
the link documents (bonds, bridges, dummy links, veths, VRFs, Ethernet tuning,
DHCPv6, Layer 2 VIPs) can both write a bond, with different spellings (mode, hash
policy and LACP rate are words in one, numbers in the other). The same holds for
`machine.disks` and `machine.systemDiskEncryption` against the volume documents.
So "one place for every setting" is not yet true there.

## The typed mirror

`TalosConfigPatchDocument` (one field per document kind) is what conversion
decodes into and validates with; it is no longer a CRD field. Typed because
machinery's types are Talos's own definition (no parallel catalogue); a CRD
schema keeps unknown fields out of storage (pruned, with a warning); and a newer
machinery renders for older Talos under a version contract, not the reverse, so
supporting a new Talos means bumping machinery.

Machinery has no JSON tags, so controller-gen refuses it, and several types
(`ByteSize`, CEL expressions, `netip` wrappers) expose no fields. `mirrorgen`
therefore generates mirror types, conversion both ways and a round-trip test per
kind. Two tables in `internal/tools/mirrorgen/main.go` handle the rest:

- `fieldOverrides`: a `meta.Unstructured` field (a pass-through to another
  project's schema) is mirrored as its upstream type (audit policy, resource
  lists, seccomp profile, authorizer webhook, kubelet credential provider, which
  takes `k8s.io/kubelet`) or dropped. `machine.kubelet.extraConfig` is free-form
  and not mirrored; the typed kubelet fields are. Under a typed-only API an absent
  field is inexpressible, so a commonly wanted one missing is a product gap.
- `secretFields`: fields that carry secret material (join tokens, CA key pairs,
  encryption keys) are dropped whatever their type, so a `TalosConfigPatch` cannot
  overwrite what the secrets bundle generates.

No webhook rejects a patch carrying a secret field. Dropping the field from the
schema keeps the value out of storage whatever the caller does; a webhook would
add only a message, and `failurePolicy` on the write path (`Fail` blocks every
change in a controller outage, `Ignore` leaves the guard absent when invoked).
Ichor's clients ask for strict validation and report the field; `kubectl` is
strict by default; a non-strict client gets pruning, which is safe.

A table keyed by field path records what types cannot: reboot needed, install-time
only, introducing Talos version. An unknown path is reboot-required.

`adopt` reads node configs with the same types. Kinds outside the allow-list are
listed, and the user extends the list or drops them; nothing is dropped silently.

## Hardening options

Typed, off by default, rendered only when set:

- `spec.security.secureBoot` (`Off`, `Preferred`, `Required`) picks the SecureBoot
  installer for approval and each Talos upgrade; `Required` holds the first
  configuration of a node without SecureBoot.
- `spec.security.kernelLockdown` is checked against what each node reports. Both
  surface as per-node and cluster conditions, read from `SecurityState`, never
  changed in place.
- `spec.security.kubeletServingCertificates` renders `serverTLSBootstrap` and the
  kube-apiserver's kubelet CA; the controller approves each kubelet's
  serving-certificate request itself, only for that node's own names and addresses.

## Reset wipe modes

A reset (`TalosOperation` kind node with `spec.node.wipeMode`, or `ichor delete
node --wipe-mode`) can wipe more than STATE and EPHEMERAL, so a node installs
afresh from the SecureBoot ISO.

| `wipeMode` | Erases |
|---|---|
| `system-partitions` (default) | STATE and EPHEMERAL only |
| `system-disk` | the whole system disk, boot and EFI included; user disks stay |
| `all` | the same as `system-disk`; not every disk |
| `system-disk-and-user-disks` | the system disk and every user disk |

---
sidebar_group: "Configuration"
sidebar_order: 7
---

# Typed configuration

`TalosConfigPatch.spec.config` is the only place a patch carries Talos
configuration. It is typed, with no raw-YAML field, and each setting is written
once. `spec.documents[]`, which held the same settings as Talos config documents,
is gone from the CRD: an object that still sets it is refused by a client that asks
for strict field validation, as `kubectl` and every Ichor client do, and pruned of
it by one that does not.

## Ichor's schema: `spec.config`

`spec.config` is one nested object in the spirit of the `v1alpha1` document: a
`machine` section and a `cluster` section, with `debug` and `persist` beside them
as in `Config`. Every setting Talos accepts through a document Ichor allows is in
it, each written once, in the place its meaning gives it:

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

The schema is not written by hand. `internal/tools/ichorgen` reads the mirror
types `mirrorgen` makes from machinery and the rows of `place.go`, one for each
document kind, and writes `pkg/ichorconfig`. A row says where a kind goes; the
fields of the kind, and their types, come from the mirror. A field Talos adds to
a kind therefore appears in the schema the next time `make generate` runs, in
the place its kind has, with no row edited. The settings of a kind whose row puts
it beside a setting of the same meaning are joined with it by name, and a setting
two documents spell is one field.

How the schema is built:

- **A kind that is a named thing is a map from its name.** `machine.kernel.modules`,
  `machine.volumes.user`, `machine.network.bonds`, `cluster.inlineManifests` and the
  rest hold one entry for each document of that kind, and the key is the document's
  `name`. The authorizers of the API server are the one list, because their order
  matters.
- **A kind that is one thing joins the object of the same meaning.** KubeletConfig
  and the v1alpha1 `machine.kubelet` are one `machine.kubelet`.
- **Where Talos spells a setting two ways, Ichor has the spelling of the document**,
  because the document is where Talos adds new fields. `machine.time.disabled`
  is `machine.time.enabled`, `cluster.coreDNS.disabled` is `cluster.coreDNS.enabled`,
  `cluster.apiServer.certSANs` is `certExtraSANs`, the nameservers are the objects
  of `ResolverConfig`, and the registry mirrors, hosts, extra manifests and inline
  manifests are the keyed forms of their documents. The v1alpha1 settings that
  are not in the schema for this reason are listed in
  `pkg/ichorconfig/testdata/not_carried.txt`, apart from the generator, and a test
  fails when a setting of a document is in neither the schema nor that file.
- **A field that is optional anywhere its object is shared.** A patch is a sparse
  overlay. A field a document requires is required in Ichor's schema only where
  that document is the only one whose fields the object holds; Talos validates the
  whole configuration when it is rendered.
- **Descriptions are Ichor's own.** The schema carries no text of Talos's.

The controller converts `spec.config` to Talos documents when it renders a
machine, through the mirror types, so a setting reaches the node as the Talos
document that carries it. The conversion reads the `ichor` struct
tag of each field, which names the documents and paths the setting is written to:

- A setting that a document spells goes to the document, and one that only the
  `v1alpha1` document has goes there. Talos refuses the Kubernetes component
  documents (`KubeletConfig`, `KubeAPIServerConfig`, and the others of Talos 1.14)
  beside the `v1alpha1` block of the same component, so a patch that sets a
  document-only field and a `v1alpha1`-only field of one object is refused at
  render with Talos's own message. A switch set to false is written to the
  `v1alpha1` document with every other setting of its object, for the same reason.
- The Kubernetes documents need a machine on Talos 1.14 or later. For an earlier
  machine a setting that both spell is written to the `v1alpha1` document, the
  inline manifests and extra manifests become the lists of that document, and one
  only the document spells is refused with the setting named.
- A document only a control plane takes (the API server, the controller manager,
  the scheduler and the rest) is left out of a worker, so a cluster-layer patch can
  set the API server for the control planes it reaches.
- `machine.features.kubePrism.enabled` and `kubernetesTalosAPIAccess.enabled` are
  switches for their documents: true brings the document into being.
- The guards Ichor applies to patches apply to `spec.config` first: Ichor's own
  CNI inline manifest is dropped once the cluster has handed over, Talos API access
  is left out of workers, and `cluster.proxy.disabled` is Ichor's.

What is not joined yet, and is therefore still a place where one setting can be
written twice: the links. `machine.network.interfaces[]` is the
`v1alpha1` list, and the link documents (bonds, bridges, dummy links, veths, VRFs,
Ethernet tuning, DHCPv6, Layer 2 VIPs) are collections beside it. A bond can be
written in both, and the two spell its settings differently (the bond mode, the
hash policy and the LACP rate are words in one and numbers in the other; the
routes are `network` in one and `destination` in the other). Joining them is a
row for each pair with a conversion for each setting spelled differently.
`machine.disks` and `machine.systemDiskEncryption` have the same relation to the
volume documents. These are the only places where a setting can still be written
in two places, so "one place to write every setting" is not yet true of bonds,
bridges and the other link documents, nor of disks and disk encryption.

## The typed mirror

The settings of `spec.config` are the typed mirror of the Talos documents; the
`TalosConfigPatchDocument` Go type, one field for each document kind, is what the
conversion decodes into and validates with. It is no longer a field of any CRD.
The rest of this page is about those types. The reasons for typed fields:

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
`SecurityState`, never changed in place.

A reset, a TalosOperation of kind node with `spec.node.wipeMode` or
`ichor delete node --wipe-mode`, can wipe more than STATE and EPHEMERAL, so a node
can be installed afresh from the SecureBoot ISO. The modes, by what they erase:

- `system-partitions` (the default; no `wipeMode` on the operation): STATE and
  EPHEMERAL only.
- `system-disk`: the whole system disk, boot and EFI included. User disks stay.
- `all`: the same whole system disk as `system-disk`. User disks stay; it does
  not mean every disk.
- `system-disk-and-user-disks`: the whole system disk and every user disk.

`spec.security.kubeletServingCertificates` renders `serverTLSBootstrap` and the
kube-apiserver's kubelet CA, and the controller approves each kubelet's
serving-certificate request itself, only for that node's own names and addresses,
read from the node.

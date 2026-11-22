# Hardening the clusters Ichor creates

Every hardening option is a typed field of `spec.security` on the `TalosCluster`.
All are off unless you set them, on new and existing clusters. Without
`spec.security` the machine configuration is byte for byte what it was before
the field existed. Ichor renders the options at the system layer, below every
`TalosConfigPatch`, so a patch can still override them.

Edit the `TalosCluster` (UI, CLI or CR); the controller applies it and never edits your `spec`.

## How a change reaches the nodes

![How a change to spec.security reaches the nodes. The controller renders the option below every TalosConfigPatch. The ingress firewall applies without a reboot but can lock Ichor out. WireGuard, Pod Security, audit and kubelet serving certificates restart a component and leave the nodes up. Disk encryption and SecureBoot wipe and rejoin each node, one at a time, and you run the wipe. Kernel lockdown is only checked, and Talos API access from pods is only warned about.](../images/hardening-rollout.light.drawio.svg)

*The option decides what happens to the nodes: some apply without a reboot, some restart a component, and disk encryption and SecureBoot erase each node in turn.*

## The options

| Option | Field | Rollout impact |
|---|---|---|
| [Ingress firewall](hardening/ingress-firewall.md) | `firewall` | No reboot; [lock-out risk](hardening/ingress-firewall.md#not-to-lock-ichor-out) |
| [WireGuard](hardening/cilium.md) | `wireGuard` | Cilium agents restart, no reboot |
| [Pod Security](hardening/pods-and-api.md) | `podSecurity` | kube-apiserver restarts per control plane |
| [Audit policy](hardening/pods-and-api.md) | `audit` | kube-apiserver restarts per control plane |
| [Disk encryption](hardening/disk-encryption.md) | `diskEncryption` | [Wipe and rejoin every node](hardening/disk-encryption.md#existing-machines) |
| [SecureBoot](hardening/secureboot.md) | `secureBoot` | Never touches an installed node: [reset and add each](hardening/secureboot.md#moving-an-installed-cluster) |
| [Kubelet serving certificates](hardening/kubelets.md) | `kubeletServingCertificates` | Kubelet restarts; `verifyFromAPIServer` also restarts the kube-apiserver |
| [Kernel lockdown](hardening/secureboot.md#lockdown) | `kernelLockdown` | Checked only: the level is baked into the boot asset |
| [Talos API from pods](hardening/pods-and-api.md#talos-api-access-from-kubernetes-pods) | a `Config` patch | None; warned about |

All fields are under `spec.security`.

## Related pages

- [The local secrets bundle](hardening/secrets-bundle.md): the passphrase, and encrypting the bundle file.
- [Rotating the CAs](hardening/rotating-cas.md): etcd, aggregator, Talos API and Kubernetes API CAs.

## Ichor's own hardening

These protect Ichor itself, not the cluster it manages.

| Item | What it is |
|---|---|
| `ichor-storage-extensions` | ServiceAccount the controller impersonates to write `spec.schematic.id` for a storage preset's extensions. It holds `patch` on `talosclusters` only; the controller writes no spec itself. The upgrade it creates waits for `ichor approve operation NAME` |
| `ichor-consent-sweep` | ServiceAccount the controller impersonates to remove one annotation, the kube-proxy switch consent. The webhook allows nothing else |
| `ichor-bundle-secret-role` | Role that grants `get` on the secrets bundle Secret and nothing else; installed when the chart value `rbac.bundleSecret.names` lists it. `create credentials`, `export bundle`, the `rotate ca` hand-off and `adopt --add-machine` need it bound. The Secret holds every CA key, so bind it only to people you would give `os:admin` |
| `InstallerImageUnverified` | `IchorInstallation` condition, True once an installer image ran with its signature unchecked (`--skip-signature-check`). A later verified run does not clear it; only a person removes it from `status.conditions` |
| Image Factory egress | When a cluster has a `TalosStorage`, the controller (not ichor-ui) may reach that cluster's Image Factory, `spec.imageFactoryURL` (default `https://factory.talos.dev`), over https. The Cilium policy for it is separate and removed when no cluster needs it |

## Not modelled

Private NTP and a Kubernetes audit log sink are not options yet.

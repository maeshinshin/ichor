# Hardening the clusters Ichor creates

Every hardening option below is a typed field of `spec.security` on the
`TalosCluster`. All of them are off unless you set them, for new and
existing clusters alike. A cluster without `spec.security` renders the same
machine configuration, byte for byte, as before the field existed. Ichor renders
the options at the system layer, below every `TalosConfigPatch`, so a patch can
still override what they render.

You change an option by editing the `TalosCluster` (in the UI, with the CLI, or
by writing the CR). The controller applies it; it never edits your `spec`.

## How a change reaches the nodes

![How a change to spec.security reaches the nodes. The controller renders the option below every TalosConfigPatch. The ingress firewall applies without a reboot but can lock Ichor out. WireGuard, Pod Security, audit and kubelet serving certificates restart a component and leave the nodes up. Disk encryption and SecureBoot wipe and rejoin each node, one at a time, and you run the wipe. Kernel lockdown is only checked, and Talos API access from pods is only warned about.](../images/hardening-rollout.light.drawio.svg)

*The option decides what happens to the nodes: some apply without a reboot, some restart a component, and disk encryption and SecureBoot erase each node in turn.*

## The options

| Option | Field | What it does | Rollout impact |
|---|---|---|---|
| [Ingress firewall](hardening/ingress-firewall.md) | `spec.security.firewall` | Talos ingress firewall in block mode, with the rules Ichor, Talos and Cilium need | No reboot. Lock-out risk, see [Ingress firewall](hardening/ingress-firewall.md#not-to-lock-ichor-out) |
| [WireGuard](hardening/cilium.md) | `spec.security.wireGuard` | Cilium encrypts pod traffic between nodes | Rolling restart of the Cilium agents (Ichor starts it), no node reboot |
| [Pod Security](hardening/pods-and-api.md) | `spec.security.podSecurity` | Pod Security admission levels and exempt namespaces | kube-apiserver restarts on each control plane, no node reboot |
| [Audit policy](hardening/pods-and-api.md) | `spec.security.audit` | One of three kube-apiserver audit policy presets | kube-apiserver restarts on each control plane, no node reboot |
| [Disk encryption](hardening/disk-encryption.md) | `spec.security.diskEncryption` | LUKS2 on STATE and EPHEMERAL | Wipe and rejoin of every node, see [Disk encryption](hardening/disk-encryption.md#existing-machines) |
| [SecureBoot](hardening/secureboot.md) | `spec.security.secureBoot` | Uses Talos's signed UKI installer, reports each node's SecureBoot state, refuses a `Required` cluster's unsecured machines where it can | Never touches an installed node: reset and add each one, see [SecureBoot](hardening/secureboot.md#moving-an-installed-cluster) |
| [Kubelet serving certificates](hardening/kubelets.md) | `spec.security.kubeletServingCertificates` | Kubelets serve certificates signed by the Kubernetes CA; Ichor approves the requests; optionally the kube-apiserver verifies them | Kubelet restart on each node (no node reboot); with `verifyFromAPIServer` also a kube-apiserver restart, see [Kubelet serving certificates](hardening/kubelets.md#verifyfromapiserver) |
| [Kernel lockdown](hardening/secureboot.md#lockdown) | `spec.security.kernelLockdown` | Checks each node's lockdown level and reports a node below it | Checked only: the level is baked into the boot asset |
| [Talos API from pods](hardening/pods-and-api.md#talos-api-access-from-kubernetes-pods) | (a `Config` patch) | Warns about `kubernetesTalosAPIAccess` (`os:admin` most strongly); rendered on control planes only | None |

## What is here

- [Ingress firewall](hardening/ingress-firewall.md): the rules Ichor opens, the lock-out risk and the way back.
- [Cilium: WireGuard and Helm values](hardening/cilium.md): transparent encryption, and who may change the Cilium values.
- [Pods and the API server](hardening/pods-and-api.md): Pod Security, the audit policy and pod access to the Talos API.
- [Kubelet serving certificates](hardening/kubelets.md): the kubelets' certificates and how Ichor approves them.
- [Disk encryption](hardening/disk-encryption.md): LUKS2 for STATE and EPHEMERAL, and how existing machines are wiped.
- [SecureBoot, UKI and kernel lockdown](hardening/secureboot.md): the signed boot path and moving an installed cluster onto it.
- [The local secrets bundle](hardening/secrets-bundle.md): the passphrase, and encrypting the bundle file.
- [Rotating the CAs](hardening/rotating-cas.md): rotating the etcd, aggregator, Talos API and Kubernetes API CAs.

## Not modelled

Private NTP and a Kubernetes audit log sink are not options yet.

# Hardening the clusters Ichor creates

Every hardening option below is a typed field of `spec.security` on the
`TalosCluster`. All of them are off unless you set them, for new and
existing clusters alike. A cluster without `spec.security` renders the same
machine configuration, byte for byte, as before the field existed. Ichor renders
the options at the system layer, below every `TalosConfigPatch`, so a patch can
still override what they render.

You change an option by editing the `TalosCluster` (in the UI, with the CLI, or
by writing the CR). The controller applies it; it never edits your `spec`.

| Option | Field | What it does | Rollout impact |
|---|---|---|---|
| Ingress firewall | `spec.security.firewall` | Talos ingress firewall in block mode, with the rules Ichor, Talos and Cilium need | No reboot. Lock-out risk, see below |
| WireGuard | `spec.security.wireGuard` | Cilium encrypts pod traffic between nodes | Rolling restart of the Cilium agents, no node reboot |
| Pod Security | `spec.security.podSecurity` | Pod Security admission levels and exempt namespaces | kube-apiserver restarts on each control plane, no node reboot |
| Audit policy | `spec.security.audit` | One of three kube-apiserver audit policy presets | kube-apiserver restarts on each control plane, no node reboot |
| Disk encryption | `spec.security.diskEncryption` | LUKS2 on STATE and EPHEMERAL | Wipe and rejoin of every node, see below |
| Talos API from pods | (a `Config` patch) | Refuses `os:admin` in `kubernetesTalosAPIAccess` | None |

## Ingress firewall

```yaml
spec:
  security:
    firewall:
      enabled: true
      controllerCIDRs: [192.168.10.0/24]   # where Ichor's controller connects from
      nodeCIDRs: [10.0.0.0/24]             # every machine of the cluster
      controlPlaneCIDRs: [10.0.0.10/32]    # optional: control planes only
      talosAPICIDRs: [172.16.0.0/12]       # optional: operators using talosctl
      apiServerCIDRs: [203.0.113.0/24]     # optional: users and load balancers
      kubeSpan: false
      allow:                               # optional: host-network services
        - {name: ingress, protocol: tcp, ports: ["80", "443"], cidrs: [0.0.0.0/0]}
```

Enabled sets `NetworkDefaultActionConfig` to `block` and renders one
`NetworkRuleConfig` per service, named `ichor-fw-<service>` (Talos v1.14,
"Ingress Firewall"). Talos always accepts `lo`, `siderolink` and `kubespan`, and
in block mode also rate-limited ICMP and traffic between the pod and service
subnets. What Ichor opens:

| Rule | Port | Sources | On |
|---|---|---|---|
| apid | TCP 50000 | `controllerCIDRs`, `talosAPICIDRs`, control planes | all nodes |
| kubelet | TCP 10250 | control planes, the pod subnets | all nodes |
| Cilium tunnel | UDP 8472 (vxlan, the default) or 6081 (`spec.cni.tunnel: geneve`) | `nodeCIDRs` | all nodes |
| Cilium health | TCP 4240 | `nodeCIDRs` | all nodes |
| trustd | TCP 50001 | `nodeCIDRs` | control planes |
| etcd | TCP 2379-2380 | control planes | control planes |
| Kubernetes API | TCP 6443 | `controllerCIDRs`, `apiServerCIDRs`, `nodeCIDRs`, the pod and service subnets | control planes |
| Cilium WireGuard | UDP 51871 | `nodeCIDRs` | all nodes, only if `wireGuard` is on |
| KubeSpan | UDP 51820 | anywhere | all nodes, only if `kubeSpan` is set |
| `allow` entries | yours | yours | all nodes |

Control planes means `controlPlaneCIDRs`, or `nodeCIDRs` when that is unset. The
pod and service subnets are `spec.network`, or Talos's defaults (10.244.0.0/16,
10.96.0.0/12). The control-plane metrics rule keeps working beside it.

Everything not listed is blocked, so a service you run with host networking
(an ingress controller on 80 and 443, a node exporter, a NodePort range, Hubble
relay peers on 4244) needs an `allow` entry.

**Not to lock Ichor out.** `controllerCIDRs` is required, and the admission
webhook refuses a firewall whose `controllerCIDRs` does not contain the address
the controller connects to the cluster endpoint from (it asks the routing table
of the webhook's own pod). If the controller runs in a pod, list the pod network
and its node's address. If the endpoint does not resolve yet, the check is
skipped. Ichor does not yet apply the firewall in Talos's `try` mode with an
automatic revert (follow-up, needs hardware to verify). The firewall applies
without a reboot; if you do lock yourself out, the way back is the node's console
or a reset.

## WireGuard encryption

```yaml
spec:
  security:
    wireGuard: {enabled: true, nodeEncryption: false}
```

Sets `encryption.enabled=true`, `encryption.type=wireguard` and
`encryption.nodeEncryption` in the managed Cilium Helm values (Cilium 1.18,
"Transparent Encryption with WireGuard"). The typed option wins over a
conflicting `encryption.*` in `spec.cni.cilium.values`. The Cilium Helm Job runs
again and the agents restart one after another; nodes do not reboot. Pod traffic
between nodes is encrypted once both ends have switched, with short resets of
connections as each agent flips. The tunnel MTU shrinks. With the firewall on,
UDP 51871 is opened between nodes. Strict mode (`encryption.strictMode`) is not
modelled; set it through `spec.cni.cilium.values` only after every agent reports
encryption.

## Pod Security and audit

```yaml
spec:
  security:
    podSecurity: {enforce: restricted, exemptNamespaces: [rook-ceph]}
    audit: {policy: rbacChanges}
```

Both need `versionContract` v1.14 or later (they are Talos's multi-document
`KubeAdmissionControlConfig` and `KubeAuditPolicyConfig`); on an older contract
Ichor refuses the option instead of raising the contract. Both restart the
kube-apiserver on each control plane, one after another by Talos; nodes do not
reboot.

`podSecurity` changes only the levels you set (`enforce`, `audit`, `warn`; Talos
defaults are baseline, restricted, restricted) and adds `exemptNamespaces` to
`kube-system`, which stays exempt. Existing pods are not evicted, but new and
updated pods that break `enforce` are rejected. Namespaces that run privileged
workloads (storage, CNI add-ons) need an exemption.

`audit.policy` presets. In none of them is anything about Secrets,
ConfigMaps, service account tokens or token reviews logged above Metadata.

| Preset | Records |
|---|---|
| `metadata` | Who did what, without bodies, for every request (Talos's default policy) |
| `rbacChanges` | `metadata`, plus request and response of changes to roles and bindings |
| `writes` | `metadata`, plus the request body of every change |

Ichor does not change where Talos writes the audit log or ship it anywhere.

## Disk encryption

```yaml
spec:
  security:
    diskEncryption: {type: tpm, requireSecureBoot: true}   # or nodeID, or kms + kmsEndpoint
```

Renders `VolumeConfig` documents for STATE and EPHEMERAL with a LUKS2 key slot 0
(Talos v1.14, "Disk Encryption"), EPHEMERAL's key locked to STATE. The key type:

- `tpm`: sealed in the machine's TPM 2.0, bound to PCR 7. `requireSecureBoot`
  makes enrolment fail unless SecureBoot is on; without SecureBoot the TPM key
  gives no boot-chain protection.
- `nodeID`: derived from the node UUID. It protects against a removed disk only.
- `kms`: a KMS server seals and unseals the key. `kmsEndpoint` must be `https://`;
  Ichor refuses any other scheme because the key material would cross the network
  in plaintext.

Static passphrases are not available (they would sit in the machine config).

Ichor does not check that a node has a TPM: it records no hardware inventory.
Talos refuses to enrol a TPM key on a machine without one, so the node would
fail at provisioning (this needs a hardware run to confirm what the failure
looks like).

**Existing machines.** Talos encrypts a volume only when it creates it, so
setting this on a cluster that already has approved machines changes nothing on
them until each one is wiped and rejoined. The webhook therefore refuses the
change unless the `TalosCluster` carries the annotation
`ichor.maesh.dev/confirm-disk-encryption-wipe: "true"`. Ichor never wipes a node
for this. The path, one node at a time: set the option and the annotation, then
use "Reset and add" on a worker, wait for it to rejoin and come up
encrypted, then do each control plane in turn, leaving etcd quorum intact. Back
up etcd first.

## Talos API access from Kubernetes pods

`machine.features.kubernetesTalosAPIAccess` in a `Config` patch with `os:admin` in
`allowedRoles` hands a Talos administrator to every pod in the allowed
namespaces. The `TalosConfigPatch` webhook refuses it unless the user holds the
`break-glass` verb on that patch. Any enabled `kubernetesTalosAPIAccess` is
admitted with a warning naming the roles and namespaces. An update that does not
touch the documents is not re-checked, so a patch stored earlier stays editable.

## Not modelled

SecureBoot and UKI images, kernel lockdown, kubelet serving-certificate
bootstrap, private NTP and a Kubernetes audit log sink are not options yet.
SecureBoot and UKI are a matter of the install image (`spec.install.image` and
the schematic); moving to one is an upgrade to a different installer image with
a reboot and firmware key enrolment. A follow-up.

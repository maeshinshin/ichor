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
| WireGuard | `spec.security.wireGuard` | Cilium encrypts pod traffic between nodes | Rolling restart of the Cilium agents (Ichor starts it), no node reboot |
| Pod Security | `spec.security.podSecurity` | Pod Security admission levels and exempt namespaces | kube-apiserver restarts on each control plane, no node reboot |
| Audit policy | `spec.security.audit` | One of three kube-apiserver audit policy presets | kube-apiserver restarts on each control plane, no node reboot |
| Disk encryption | `spec.security.diskEncryption` | LUKS2 on STATE and EPHEMERAL | Wipe and rejoin of every node, see below |
| Talos API from pods | (a `Config` patch) | Warns about `kubernetesTalosAPIAccess` (`os:admin` most strongly); rendered on control planes only | None |

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
and its node's address: with Cilium the controller reaches apid on its own node
from its pod IP, but on any other node from its node's address (masqueraded),
measured on hardware. The webhook also refuses a firewall whose `controlPlaneCIDRs`
miss the address of an approved control plane (the controller reaches the other
nodes from a control plane's address, so Ichor itself would be cut off; a
machine whose endpoint is a hostname is not checked). It only warns when
`nodeCIDRs` miss a machine (that machine loses its Cilium and trustd rules, but
Ichor's own path is intact) and when `talosAPICIDRs` is empty: list your own
workstation there, it is the only way back if Ichor is cut off. If the endpoint
does not resolve yet, the controller-address check is skipped; machines
approved after the firewall was enabled are not re-checked, so keep the CIDRs
wide enough for them.

**Recovery after a lock-out** (a `controlPlaneCIDRs` that excluded the real
control plane on a cluster created before the check, say). The controller
reports `NodeReachable=False NodeUnreachable` on the machines it lost, and
reverting the spec alone cannot help because it cannot reach them to deliver
it. From a workstation in `talosAPICIDRs`, open apid to the control plane with
a temporary rule, then fix the spec:

```
talosctl -e NODE -n NODE patch machineconfig --patch @rule.yaml
# rule.yaml:
#   apiVersion: v1alpha1
#   kind: NetworkRuleConfig
#   name: recover
#   portSelector: {ports: [50000], protocol: tcp}
#   ingress: [{subnet: CONTROL_PLANE_IP/32}]
```

The machine was Ready again 26 seconds after the patch (measured) and the
controller removed the temporary rule once the corrected spec applied. With no
`talosAPICIDRs` entry the way back is the node's console or a reset. A pod's traffic to
another node's host ports is also seen as its own node's address, so rules keyed
to node CIDRs cannot tell pods on the same node apart. Ichor does not yet apply the firewall in Talos's `try` mode with an
automatic revert (follow-up, needs hardware to verify). The firewall applies
without a reboot.

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
again (about a minute) and, because the agents read encryption only when they
start, Ichor sets the chart's `rollOutCiliumPods` so the DaemonSet rolls (one
node at a time) when `cilium-config` changes. `CNIValuesApplied` turns True once
the Job is done and the DaemonSet has rolled out, i.e. the agents run with the
new values; you can confirm with `cilium-dbg encrypt status`. Nodes do not
reboot. Pod traffic
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
kube-apiserver on each control plane; nodes do not reboot. The restart is not
applied on its own: the control plane shows
`Ready=False APIServerRestartRequired` until you run
`ichor config apply --cluster NAME --follow` (or start the rollout on the
cluster page), which goes one control plane at a time. The condition message
names that command. With one control plane the API is down for about a
minute (measured: 109 to 171 seconds for the whole operation).

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
Talos refuses to enrol a TPM key on a machine without one: on a node
without a TPM the node installs, reboots and stays in stage `booting` with the
STATE volume `failed`. Ichor puts the failed volume and Talos's error in the
`Ready` message (`NodeStarting: ... stage "booting" ...; a volume failed:
STATE: stat /dev/tpm0: no such file or directory`).

**Existing machines.** Talos encrypts a volume only when it creates it, so
setting this on a cluster that already has approved machines changes nothing on
them until each one is wiped and rejoined. The webhook therefore refuses the
change unless the `TalosCluster` carries the annotation
`ichor.maesh.dev/confirm-disk-encryption-wipe: "true"`. Ichor never wipes a node
for this. The path, one node at a time: set the option and the annotation, then
reset a worker, wait for it to rejoin and come up encrypted, then do each
control plane in turn, leaving etcd quorum intact. Back up etcd first. "Reset
and add" is for machines of another cluster and refuses one already
in Ichor; for your own node use `ichor node reset NODE --break-glass REASON --confirm NODE`
(NODE is the machine's name or its hostname; `--confirm` takes the same name),
and approve the pending machine again once it is rediscovered. Pods that no
controller manages (bare pods) block the drain: the machine's `Removal`
condition says `DrainFailed` and names the pod, a Warning Event repeats it,
and the drain is retried with growing backoff, so delete the pod (there is no
force option; Ichor never deletes a pod nobody owns) and it carries on. The
Kubernetes Node object stays after a reset (a delete could race the kubelet);
Ichor marks it so that, while it is not Ready, discovery no longer counts its
address as a member and finds the wiped machine again. You may delete the stale
Node with `kubectl delete node` anyway. Measured: about 45 seconds to reset and
2 minutes from approval to Ready; the volumes show `encryptionProvider: luks2`
in `talosctl get volumestatus`.

## Talos API access from Kubernetes pods

`machine.features.kubernetesTalosAPIAccess` in a `Config` patch with `os:admin` in
`allowedRoles` hands a Talos administrator to every pod in the allowed
namespaces. That is your cluster's choice, so the `TalosConfigPatch` webhook
only warns (with extra wording for `os:admin`) and admits the patch. Talos
allows the feature on control planes only, so Ichor renders it on control
planes and leaves it out of workers, whatever layer the patch is on. The `TalosConfigPatch` webhook refuses it unless the user holds the
`break-glass` verb on that patch. Any enabled `kubernetesTalosAPIAccess` is
admitted with a warning naming the roles and namespaces. An update that does not
touch the documents is not re-checked, so a patch stored earlier stays editable.

## Not modelled

SecureBoot and UKI images, kernel lockdown, kubelet serving-certificate
bootstrap, private NTP and a Kubernetes audit log sink are not options yet.
SecureBoot and UKI are a matter of the install image (`spec.install.image` and
the schematic); moving to one is an upgrade to a different installer image with
a reboot and firmware key enrolment. A follow-up.

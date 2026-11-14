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
| SecureBoot | `spec.security.secureBoot` | Uses Talos's signed UKI installer, reports each node's SecureBoot state, refuses a `Required` cluster's unsecured machines where it can | Never touches an installed node: reset and add each one, see below |
| Kubelet serving certificates | `spec.security.kubeletServingCertificates` | Kubelets serve certificates signed by the Kubernetes CA; Ichor approves the requests; optionally the kube-apiserver verifies them | Kubelet restart on each node (no node reboot); with `verifyFromAPIServer` also a kube-apiserver restart, see below |
| Kernel lockdown | `spec.security.kernelLockdown` | Checks each node's lockdown level and reports a node below it | Checked only: the level is baked into the boot asset |
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

```sh
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
`ichor rollout config --cluster NAME --follow` (or start the rollout on the
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
remove a worker, wait for it to rejoin and come up encrypted, then do each
control plane in turn, leaving etcd quorum intact. Back up etcd first. "Reset
and add" is for machines of another cluster and refuses one already
in Ichor; for your own node use `ichor delete node NODE --reboot --override --confirm MACHINE`
(NODE is the machine's name or its hostname; `--confirm` is always the machine name, the
TalosMachine's UUID that the prompt and the UI dialog show),
and approve the pending machine again once it is rediscovered. Pods that no
controller manages (bare pods) block the drain: the machine's `Removal`
condition says `DrainFailed` and names the pod, a Warning Event repeats it,
and the drain is retried with growing backoff, so delete the pod (there is no
force option; Ichor never deletes a pod nobody owns) and it carries on. The
remove deletes the Kubernetes Node object once the node is not Ready. If the Node is still Ready after five minutes, Ichor keeps it, marks
it so that discovery no longer counts its address as a member and finds the wiped
machine again, and a Warning Event names `kubectl delete node`. Measured: about 45 seconds to reset and
2 minutes from approval to Ready; the volumes show `encryptionProvider: luks2`
in `talosctl get volumestatus`.

## SecureBoot, UKI and kernel lockdown

```yaml
spec:
  security:
    secureBoot: {mode: Preferred}      # Off (default), Preferred or Required
    kernelLockdown: integrity          # or confidentiality; checked, not rendered
```

Talos (v1.14, "SecureBoot") boots a signed Unified Kernel Image through
systemd-boot, so the firmware verifies kernel, initramfs and command line. It
needs UEFI (not BIOS) and a fresh install from the SecureBoot images: Talos
cannot turn a GRUB install into a UKI one. Image Factory serves the installer as
`factory.talos.dev/metal-installer-secureboot/<schematic>:<version>` and the
ISO as `metal-amd64-secureboot.iso`.

What the modes do:

- `Off` (or unset): nothing changes. A node that nevertheless runs SecureBoot is
  still upgraded with the SecureBoot installer, because the stock one writes an
  unsigned UKI the firmware would refuse; its `SecureBoot` condition says the
  mode and the node disagree.
- `Preferred`: where Ichor picks the installer it picks the SecureBoot variant
  (a Talos upgrade of a node that booted a UKI; an approval that names a disk
  and no `--install-image`, which then records
  `factory.talos.dev/metal-installer-secureboot/<spec.schematic or the stock
  schematic>:<spec.talosVersion>` in `spec.install.image`). Machines without
  SecureBoot are reported and left alone.
- `Required`: as `Preferred`, and also: a TPM disk key renders
  `checkSecurebootStatusOnEnroll: true` unless `requireSecureBoot: false` says
  otherwise; the webhook refuses to set `Required` while an approved machine does
  not run SecureBoot, unless the cluster carries
  `ichor.maesh.dev/confirm-secureboot-reinstall: "true"`; and the controller does
  not send the first configuration to a machine in maintenance mode that booted
  without SecureBoot (`Ready=False`, reason `SecureBootRequired`, nothing sent).
  A node that is already installed without SecureBoot is reported
  (`SecureBoot=False`), never reconfigured or wiped.

Every node's state is read from Talos (`talosctl get securitystate`) and kept in
`TalosMachine.status.security`; the conditions `SecureBoot` and `KernelLockdown`
exist on each machine and, summarised, on the cluster while the option is set.
The UI's node page (Talos details, System) shows the same fields live. The
condition reasons: `SecureBootOn`, `SecureBootOff` (a UKI node whose firmware has
SecureBoot disabled), `NotUKI` (a GRUB install), `ModeOffButSecureBoot`.

**Lockdown.** In Talos v1.14 SecureBoot images run with `lockdown=integrity`
implicitly and no longer force `lockdown=confidentiality`. The command line is
embedded in the UKI and ignored at boot, and `machine.install.extraKernelArgs` is
refused with the UKI command line, so Ichor cannot render a lockdown level: for
`confidentiality` create an Image Factory schematic with
`extraKernelArgs: [lockdown=confidentiality]` and set `spec.schematic.id` to it.
Ichor reads the node's command line and reports the level in effect
(`status.security.kernelLockdown`: the last `lockdown=` word, else `integrity`
under SecureBoot, else `none`).

**Moving an installed cluster.** Changing the mode never changes an installed
node, for three reasons Talos gives: a GRUB install cannot become UKI, key
enrolment happens in the firmware, and a TPM disk key is bound to the SecureBoot
state (PCR 7). The path, one node at a time, as for disk encryption: set the
mode (and the confirm annotation for `Required`); take an etcd backup; remove the
node with a full system-disk wipe, `ichor delete node NODE --wipe-mode system-disk --reboot`
(or choose "The whole system disk" under "What to wipe" in the UI's Remove node
dialog); boot the machine
from the SecureBoot ISO of this cluster's schematic and Talos version
with the firmware in setup mode (the keys are enrolled automatically; on bare
metal choose "Enroll Secure Boot keys: auto" in the boot menu); approve it again;
wait for `SecureBoot=True` before the next node, control planes one at a time
with etcd quorum intact. Keep the UKI and PCR signing keys if you build your own
images: losing them stops the next UKI booting and the encrypted disks unlocking.

**Why the default wipe is not enough here.** A remove without `--wipe-mode` wipes
only STATE and EPHEMERAL. The installed boot partition stays,
so a machine whose system disk comes first in the boot order boots its installed
UKI again, into maintenance mode, and the SecureBoot ISO is never started: nothing
is reinstalled, the node only takes the configuration again (seen on hardware). `--wipe-mode system-disk` (`wipeMode: system-disk` on a reset operation)
erases the whole system disk, boot and EFI included, so the firmware falls through
to the ISO; `--wipe-mode all` also erases every user disk (use it only when their
data is expendable). Keep the ISO attached or first in the boot order before you
remove the node. Without Ichor's wipe, the same effect comes from
`talosctl reset --wipe-mode all` through the admin path, or from putting the ISO
first in the boot order. After the wipe the machine is looked for again every 15
seconds for 3 minutes, so it shows up as a pending machine soon after it has
booted. The remove deletes the node's Kubernetes Node object.

## Kubelet serving certificates

```yaml
spec:
  security:
    kubeletServingCertificates: {enabled: true, verifyFromAPIServer: false}
```

By default a Talos kubelet serves a self-signed certificate on port 10250, so
the kube-apiserver cannot verify it when it opens logs, exec, port-forward and
metrics connections (and a metrics-server needs `--kubelet-insecure-tls`).
`enabled` renders `serverTLSBootstrap: true` for every kubelet (the `KubeletConfig`
document from v1.14, `machine.kubelet.extraConfig` before; Talos v1.14,
"Deploy Metrics Server"). Each kubelet then asks the `kubernetes.io/kubelet-serving`
signer for a certificate, and kube-controller-manager signs it with the Kubernetes
CA, but only after somebody approves the request. Ichor's controller does that,
instead of a third-party approver, and nothing else:

- It decides only requests of that signer, and only in a cluster that enabled the option.
- It approves a request when the requester is that node's own kubelet
  (`system:node:<name>`, group `system:nodes`), the subject is exactly
  `CN=system:node:<name>, O=system:nodes`, the usages are `digital signature` and
  `server auth` (with `key encipherment` allowed), the request has no extension but
  the names, and every DNS name and IP address in it belongs to that node. What
  belongs to the node is read from the node over the authenticated Talos API (the
  addresses Talos gives the kubelet as `--node-ip`, its node name and hostname),
  not from the Kubernetes Node object.
- It denies a request that fails any of those, with the reason on the request
  (`kubectl describe csr`) and a Warning Event on the TalosCluster.
- It leaves a request pending, never denied, when it cannot place it: the node
  name is not one of the cluster's machines, or the node cannot be read right now.
  You can still approve such a request yourself.

Until a kubelet's request is approved and signed it keeps serving the certificate
it had, so switching the option on cannot take a node down; the kubelet restarts
once on each node. `kubectl get csr` shows the requests and who approved them
(`IchorKubeletServing`). The condition `KubeletServingCertificates` on the
TalosCluster is True when every machine has been issued a certificate, and
otherwise names the machines still waiting; the machine's
`status.kubeletServingCertificate` has the request and the expiry. The kubelet
renews before expiry and Ichor approves the renewal the same way. After a
Kubernetes CA rotation Talos clears a kubelet's certificates and the kubelet asks
again.

`verifyFromAPIServer` also sets `--kubelet-certificate-authority` on the
kube-apiserver (the Kubernetes CA, `/system/secrets/kubernetes/kube-apiserver/ca.crt`),
so the apiserver refuses a kubelet whose certificate is not signed by it. A node
without a CA-signed certificate would break `kubectl logs` and `exec`, so Ichor
renders the flag only after the condition `KubeletServingCertificates` is True
and then records `KubeletServingVerified`; once recorded it stays, so a node that
joins later does not take it away. Turning `verifyFromAPIServer` or `enabled` off
removes the flag. Applying it restarts the kube-apiserver on each control plane,
one at a time, without a node reboot.

A metrics-server you run yourself needs no `--kubelet-insecure-tls` once the
option is on, because it verifies the kubelet against the cluster CA that every
pod receives and the certificates carry the node's IP address. Ichor installs
none.

To verify on a cluster (what to look at, not a promise about your network):

```sh
kubectl get csr                              # Approved,Issued, requestor system:node:<name>
kubectl get talosmachine -o yaml | grep -A4 kubeletServingCertificate
kubectl get talosclusters -o yaml | grep -B1 -A3 KubeletServing
kubectl logs -n kube-system <a pod on another node>   # works with verifyFromAPIServer on
openssl s_client -connect <node-ip>:10250 </dev/null 2>/dev/null | openssl x509 -noout -issuer -ext subjectAltName
```

## Talos API access from Kubernetes pods

`machine.features.kubernetesTalosAPIAccess` in a `Config` patch with `os:admin` in
`allowedRoles` hands a Talos administrator to every pod in the allowed
namespaces. That is your cluster's choice, so the `TalosConfigPatch` webhook
only warns (with extra wording for `os:admin`) and admits the patch. Talos
allows the feature on control planes only, so Ichor renders it on control
planes and leaves it out of workers, whatever layer the patch is on. The `TalosConfigPatch` webhook refuses it unless the user holds the
`override` verb on that patch. Any enabled `kubernetesTalosAPIAccess` is
admitted with a warning naming the roles and namespaces. An update that does not
touch the documents is not re-checked, so a patch stored earlier stays editable.

## Cilium Helm values

`spec.cni.cilium.values` is the Helm values of the Cilium chart, and the Helm Job
that applies them can create privileged, host-network pods with host paths on
every node (the chart takes `extraContainers`, `extraHostPathMounts`, image
overrides and the like). A user who could write the values could therefore become
root on the nodes. So changing them, from the UI, `ichor apply cni-values|edit`,
`ichor adopt --cilium-values` or `kubectl`, needs the verb `update` on
`talosclusters/cilium-values`. Only the built-in `ichor-admin` role
holds it; `ichor-operator` can still edit everything else on the cluster and start a
Cilium upgrade, but its writes are refused when they change the values (an edit that
leaves them as they are is not). The webhook checks the right as the requesting
user, so a hand-written CR obeys it too.

The namespace of the Roles that let Cilium read Secrets is pinned as well: the five
`*.secretsNamespace.name` values (`ingressController`, `gatewayAPI`, `envoyConfig`,
`bgpControlPlane` and `tls`) are set by Ichor to `cilium-secrets` and refused as
"set by Ichor" if you set them. If you use BGP with authentication Secrets, put them in
`cilium-secrets` (the chart's own default for BGP is `kube-system`).

## The local secrets bundle

`~/.ichor/clusters/<name>/secrets.yaml` holds every CA key of the cluster. It is an age file
sealed with a passphrase, so a copy of it on a laptop backup or a cloud drive is
not a copy of the cluster's keys. `ichor init`, `ichor adopt --recover-secrets`
and `ichor export bundle` write it that way by default.

The passphrase comes from, in order:

1. `--bundle-passphrase-file PATH`: the first line of the file. `PATH` may be a
   descriptor, so the passphrase need not touch the disk:
   `ichor restore ... --bundle-passphrase-file <(pass show ichor/demo)`.
2. The environment variable `ICHOR_BUNDLE_PASSPHRASE`. Anything running as you
   (and root) can read an environment, so prefer the file.
3. On a terminal: a prompt when opening a file; a generated passphrase, printed
   once, when creating one.

Never a flag value (it would be in `ps` and your shell history). A passphrase you
choose must be at least 12 characters. Every command that reads the bundle
(`restore cluster`, `adopt --secrets-bundle`, `create credentials
--bundle`, `export bundle`) takes it the same way. A wrong passphrase and
a damaged file give the same error, which names neither.

A bundle file you made before this change still works, with a warning on every
use. Encrypt it:

```sh
ichor secrets encrypt demo              # ~/.ichor/clusters/demo/secrets.yaml
ichor secrets encrypt --file ./secrets.yaml
```

The command seals the file, opens the sealed copy again, compares it with the
original, and only then replaces the file. If the plaintext file was ever copied
or exposed, encrypting it does not undo that: rotate the CAs
(`ichor rotate ca`). The `age` tool opens the file without Ichor:
`age -d secrets.yaml`.

The kubeconfig next to it stays plaintext, because `kubectl` reads it. It holds
the `system:masters` override certificate (see
[Users and login](users-and-login.md)); once login works, move it offline or delete it
and make a new one from the bundle when you need it. The Secret in the cluster
that holds the bundle for the controller is not affected.

## Rotating the CAs

`ichor rotate ca NAME` replaces the Talos API and Kubernetes API CAs by
default. `--ca etcd` and `--ca kubernetes-aggregator` (or `--all`) add the other
two. The service-account signing key and the secretbox key are not
rotated, so rotating after a compromise is complete only for the CAs
you list.

- **etcd.** Talos has no accepted-CA list for etcd, so Ichor puts the old and the
  new CA certificate into `cluster.etcd.ca` together and reboots every control
  plane, one at a time and only while etcd is healthy, after each of the three
  passes (Talos does not restart etcd on a configuration change and its etcd
  service has no restart call, so a reboot is the only way a member loads the
  CA): three reboots of every control plane, each preceded by cordon and drain.
  A cluster with one control plane has no Kubernetes API while its only member
  reboots. Take a snapshot first
  (`ichor backup etcd NAME`). Snapshots stay readable: the backup key does
  not depend on the CAs.
- **aggregator.** kube-apiserver publishes the CAs it accepts for extension API
  servers (for example metrics-server) in `extension-apiserver-authentication`.
  Ichor waits until it carries the new CA and then two minutes before the
  front-proxy certificate is signed by it. When the rotation ends it deletes
  `extension-apiserver-authentication` once, so that kube-apiserver recreates it
  with the new CA alone (it never removes an old CA from that ConfigMap by
  itself). It restarts none of the extension API servers: one that reads the CA
  only once needs a restart by its owner.
  The aggregator CA needs `versionContract` v1.14 or later.
- **No hand-off.** A rotation of etcd and the aggregator CA alone changes nothing
  your kubeconfig or talosconfig depends on, so it does not wait for new
  credentials. After any rotation run `ichor export bundle NAME`.
- **The hand-off annotation is an attestation.** A rotation of the Talos or Kubernetes
  CA stops in `AwaitingCredentials` until the operation carries the annotation
  `ichor.maesh.dev/credentials-handoff`. `ichor create credentials --acknowledge`
  and the UI check that the new kubeconfig works against the cluster, and only
  then set it. The controller checks only that the annotation names this
  rotation's new CAs, so setting it by hand with `kubectl annotate` says "I hold a
  working kubeconfig and talosconfig for the new CAs" and nothing verifies it: if
  you do not, the old ones stop working when the old CAs drop.
- **Abandoning.** `ichor delete operation NAME` on a rotation that has not yet
  made the new CA issue (Accepting and AwaitingCredentials; Issuing too when
  neither the Kubernetes nor the etcd CA is rotated) takes it back: the new CAs
  leave the bundle, the cluster lock is released and the machines are rendered
  from the old CAs again. The nodes then report a pending change (the extra
  accepted CAs, and for etcd the CA field, are still loaded); run
  `ichor rollout config` to drop them, which reboots the nodes it has to. Later in
  the rotation the delete is refused, because the nodes would be left between
  two CAs: let it finish.

## Not modelled

Private NTP and a Kubernetes audit log sink are not options yet.

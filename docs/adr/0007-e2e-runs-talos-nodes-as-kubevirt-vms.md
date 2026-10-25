# 0007. e2e runs Talos nodes as KubeVirt VMs

Status: Accepted · Date: 2026-10-25 · Design section: 7 (Testing)

## Context

The design says e2e creates throwaway clusters with `talosctl cluster create
qemu`. That provisioner runs QEMU on the host that invokes it and needs
`CAP_SYS_ADMIN` and `CAP_NET_ADMIN` — root. The development workstation has
`/dev/kvm` world-readable and VT-x enabled, but `sudo` requires a password that
an agent session cannot supply, and the machine has 4 vCPUs and 15 GB of RAM,
which caps it at a single-node cluster.

A second host was available: a three-node bare-metal Talos cluster
(`talos-cluster01`, Talos 1.13.1 / Kubernetes 1.36.3) whose first node has 16
cores and 62 GB. A privileged pod there sees real `/dev/kvm` and `/dev/net/tun`
on AMD-V hardware — no nesting involved.

Two shapes were possible: run the standard `talosctl cluster create qemu`
inside a privileged pod, or boot Talos as KubeVirt virtual machines. The first
keeps the documented provisioner; the second means writing the VM definitions
ourselves, since talosctl has no KubeVirt provisioner.

The second was tried and it works, with a result better than the first would
have given:

- A VM booting the Image Factory `metal-amd64.iso` for Talos 1.14.3 reaches
  `MachineStatus` `STAGE: maintenance, READY: true`.
- The insecure maintenance API answers on port 50000 from any pod in the
  cluster. It is **not** reachable through `kubectl port-forward`: with
  `bridge` binding the pod IP belongs to the guest, so the virt-launcher pod's
  own network namespace has no listener for port-forward to dial, and the
  attempt is refused. (Port-forward does work under `masquerade`, which is how
  this was first measured before the binding changed — a detail that cost a
  debugging cycle downstream.) Tests therefore reach the Talos API from a
  one-shot pod inside the cluster, which is also how the controller will reach
  nodes in production.
- `get systeminformation` returns an SMBIOS UUID and serial number — exactly
  the primary key `TalosMachine.spec.identity` is built on — with
  `manufacturer: KubeVirt`.
- `get disks` shows the ISO as `sr0` and a writable 11 GB `vda`, so there is a
  real install target, and `get links` shows a real NIC plus bond and dummy
  devices.

That is Ichor's entry point reproduced exactly: a node that is up, unconfigured,
and readable over an unauthenticated connection.

## Decision

e2e runs Talos nodes as KubeVirt virtual machines on `talos-cluster01`, in the
`ichor-e2e` namespace, instead of `talosctl cluster create qemu` on a
developer's workstation. KubeVirt v1.9.1 and CDI v1.66.1 are installed there
and managed by Argo CD from the cluster's own GitOps repository, so the test
platform is described in Git like everything else in that cluster.

CI keeps the `talosctl cluster create qemu` path: GitHub's `ubuntu-24.04`
runners provide `/dev/kvm` and passwordless sudo, and Talos's own
documentation treats them as sufficient. So there are two harnesses, which is
the cost of this decision, and the e2e suite takes the cluster it is given
through `KUBECONFIG` and a set of node endpoints rather than creating one
itself.

Three facts worth keeping, each of which cost a debugging cycle:

- **CDI must be told `volumeMode: Filesystem`.** `ceph-block`'s StorageProfile
  offers `Block` first for `ReadWriteOnce`, CDI takes it, and the importer —
  which does not run as root — dies with `blockdev: cannot open
  /dev/cdi-block-volume: Permission denied`. Setting the mode explicitly on the
  DataVolume fixes it.
- **`talosctl` 1.14.3 has no global `--insecure`.** It is `-i/--insecure` on
  the individual commands, and flags must follow the subcommand.
- **`--cert-fingerprint` pins the server certificate even in maintenance
  mode.** The design notes that insecure apply does not authenticate the node
  and assumes a separate provisioning network. Fingerprint pinning is a second
  defence worth offering once a node's fingerprint is known, and the discovery
  flow should record it.

## Consequences

- e2e gets real disks, a real install target and real maintenance mode, on 16
  cores and 62 GB — enough for the `standard` topology that the workstation
  could never host.
- Ichor owns the VM manifests that talosctl's provisioner would otherwise have
  provided, and does not get `--presets maintenance` or the provisioner's
  networking for free.
- Multi-node clusters work on the pod network with KubeVirt's **`bridge`**
  binding, not `masquerade`. `masquerade` NATs the VM behind its pod IP and
  forwards only the ports declared on the interface, which would mean
  enumerating every port Talos nodes use between themselves — apid, trustd,
  apiserver, etcd, kubelet — plus whatever the inner cluster's CNI
  encapsulates with. `bridge` gives the VM the pod IP itself, so there is no
  NAT and no port list: measured from an ordinary pod, with no ports declared,
  50000 is reachable while 50001, 22 and 6443 are refused — which is the VM's
  real state in maintenance mode, where only apid is listening, not a filter.
  VM-to-VM traffic therefore reduces to pod-to-pod traffic, which Cilium
  already provides, and **Multus is not needed**.
- **The QEMU harness is written but unverified.** It creates a cluster with one
  worker and resets that worker back into maintenance mode, because
  `--presets maintenance` would leave no cluster at all and the specs need
  both a cluster and a maintenance node. The part that cannot be tested here is
  how it learns the worker's address: it scrapes `talosctl cluster create`'s
  human-readable summary, a format that is not a documented interface and was
  never observed, because running the provisioner needs root this environment
  does not have. It is marked with a `shortcut:` comment and fails loudly
  rather than silently when the scrape comes back empty, so the first CI run on
  a QEMU-capable runner is its test. That is a deliberate trade: splitting the
  spec set to avoid the risk would have narrowed what CI checks, which costs
  more than a red build does.
- The e2e suite must not assume it created the cluster, which is the right
  shape anyway: it is also how `adopt` will be tested later.
- **Nor may it assume it created the nodes.** Two harnesses means the suite
  cannot contain the provisioning of either: a KubeVirt VM is meaningless on a
  CI runner that has no KubeVirt, and `talosctl cluster create qemu --presets
  maintenance` is meaningless here. So the suite consumes a cluster through
  `KUBECONFIG` *and* a set of maintenance-mode node endpoints through the
  environment, failing loudly when either is absent, and each harness's setup
  target supplies them its own way. One set of specs runs on both.

## Past maintenance mode, resolved

The install hang recorded in ADR 0009 was root-caused and fixed, so KubeVirt is
viable for more than maintenance mode. **The ISO path works end to end, and was
observed working**: booted from the Factory ISO, a control-plane config applied,
the console showed the install pull the installer image, write to `/dev/vda` and
log `install successful`, then reboot; the new boot read its configuration from
the STATE partition and ran the boot sequence rather than reinstalling, with
apid and trustd listening about two seconds in. From there the authenticated API
answered with RBAC, `talosctl bootstrap` succeeded, and `get machinestatus`
returned `stage: running, ready: true` with etcd Running and healthy.

Keep the ISO as the boot source. The Factory also publishes a pre-installed
`metal-amd64.raw.xz`, and booting that would skip the install entirely — which
sounds attractive until you notice that **phase 3's whole flow *is* install**:
discover a node in maintenance mode, approve it, install, join. A harness that
boots pre-installed disks cannot exercise any of it.

Five things the harness has to get right, each of which cost a debugging cycle:

- **Use a control-plane config, not a worker.** This was the hang. Talos's
  certificate controller, *for workers only*, blocks on `k8s.Endpoint` being
  non-empty — the address of a real control-plane apiserver — before it will
  mint apid's serving certificate. An isolated worker never satisfies that, so
  apid and trustd never start: the kernel is up, containerd and the kubelet are
  up, and 50000/50001 are refused forever. Nothing to do with KubeVirt. The
  control-plane certificate path needs only local secrets, and comes up about
  two seconds into the second boot. Any single-node Talos-API-reachability
  fixture must therefore be control-plane shaped; that is a Talos constraint,
  not something a harness can work around.
- **Pin the cluster endpoint to the VM's own address.** `talosctl gen config`'s
  placeholder endpoint resolved through the pod gateway to the **host cluster's
  real kube-apiserver**, and Talos's endpoint controllers dialled it repeatedly
  on their own. Every attempt was rejected by TLS verification — the test CA
  does not match — so nothing was read or written, but a VM on this
  bridge-bound pod network can reach the production control plane's apiserver
  port, and the harness must never leave that endpoint at a default.
- **Give the inner cluster non-overlapping subnets.** The defaults,
  `10.244.0.0/16` and `10.96.0.0/12`, collide with the host cluster's pod
  network, which is where the VM's own address comes from. Talos raises an
  `address-overlap` diagnostic and warns that it cannot find a suitable node IP.
- **Console output on the ISO, and only there.** A schematic's
  `extraKernelArgs: [console=ttyS0,115200n8]` gives a serial console for the
  ISO boot, which is what made the original hang understandable after three
  blind attempts. Keeping it *past* the install reboot would need
  `machine.install.extraKernelArgs` and `grubUseUKICmdline: false` in the
  machine config — and on Talos 1.14 that means hand-editing the legacy
  v1alpha1 section of a config whose install and network settings live in their
  own documents (`UnattendedInstallConfig`, `KubeNetworkConfig`,
  `KubeFlannelCNIConfig`), which the API rejects as incompatible.
  **So the harness does not try.** The post-install console was a debugging aid
  from when the cause was unknown; now that it is known, it is a nicety that
  was blocking the actual goal. Configure the inner cluster through the
  documents the generator emits rather than against them, and mark the missing
  post-install console with a `shortcut:` naming the Talos release that
  requires the trade.
- **Detach the ISO after install.** It happens to work without doing so,
  because Talos's own first-boot detection finds the STATE partition and runs
  the boot sequence rather than reinstalling. That is incidental and not worth
  relying on. The harness owns the `VirtualMachine` manifest, so it can patch
  the cdrom away once the install succeeds, as it already does for DataVolumes.

## Re-verified end to end; six more things it took

The whole flow — ISO boot, install, bootstrap, etcd healthy, the e2e spec set
against the inner cluster, teardown — was run for real multiple times on
`talos-cluster01` to confirm the above still holds and that cleanup actually
leaves `ichor-e2e` empty. It does, but getting there cost six more cycles:

- **`talosctl bootstrap` can race its own prerequisite.** Immediately after the
  authenticated API starts answering `machinestatus` (the harness's own signal
  that install finished), bootstrap can still return `FailedPrecondition:
  bootstrap is not available yet` — apid being reachable doesn't mean etcd's
  bootstrap RPC is registered yet. Retry it like every other condition the
  harness polls for, instead of treating it as fatal.
- **`MachineStatus.ready` is not where it looks.** It lives at
  `spec.status.ready`, not `spec.ready` — confirmed against a real node's JSON.
  Both the harness's running/ready wait and the e2e spec's own assertion had
  the wrong path, so the check was always false and either hung or always
  passed trivially depending on how it was written. Worth remembering for any
  other MachineStatus field: `spec.status` is itself an object, not a
  flattening of its parent.
- **The inner cluster needs its own `ichor-e2e` namespace.** The harness only
  gets the node and the CRDs up; nothing creates a namespace of that name on
  the *inner* cluster, and the e2e spec's CEL fixtures assume one exists
  there. Easy to miss because the name collides with the *outer* cluster's
  namespace, which does pre-exist — two different clusters, same name,
  one of them actually needs creating.
- **`nix develop` owns the name `out`.** It exports `out=<store path>` by
  convention (every Nix derivation builder gets one), and `make install`/
  `uninstall` reused that exact name for the kustomize-rendered CRD YAML
  without `export -n`. The variable kept the export attribute from the
  shell's own setup, so every subsequent subprocess in that shell inherited a
  170KB+ environment variable, until some later `exec` (observed: `kubectl`)
  hit the kernel's combined argv+envp limit with "Argument list too long" —
  nowhere near the command that actually assigned the value. Renaming the
  local variable is the whole fix; nothing about the CRDs or kustomize was
  wrong.
- **Cleanup must check its target as carefully as setup does.** `cleanup-test-
  e2e-kubevirt` had no prerequisite check, unlike `setup-test-e2e-kubevirt`,
  so it ran `kubectl` (no `--context`) against whatever `current-context`
  happened to be active. Observed for real: a dangling `kind-kind` context
  left over from before this project moved off Kind made cleanup fail
  outright (connection refused) instead of silently targeting the wrong
  cluster — but "fails outright" was luck, not a guarantee, for a target that
  could just as easily have been a live cluster with its own `ichor-e2e`
  namespace. Both targets now share the same prerequisite check, and the
  harness binary itself refuses to run at all (`up` or `down`) when
  `KUBECONFIG` is unset, rather than fall back to kubectl's ambient context.
- **`go run` cannot be trusted with a signal-triggered cleanup.** The harness
  traps SIGINT/SIGTERM to run `down()` before exiting, so a killed `up` can't
  strand the VM. That handler never ran when the process was started with
  `go run ./test/harness/kubevirt up` and killed with SIGTERM: `go run` wraps
  the compiled binary in its own process and does not reliably forward the
  signal to it. Confirmed by building the binary and exec'ing it directly
  instead (`go build -o bin/kubevirt-harness ./test/harness/kubevirt`, then
  run that) — the identical kill now logs the handler firing and leaves
  `ichor-e2e` empty. Any harness step whose correctness depends on catching a
  signal has to run as the actual binary, not under `go run`.

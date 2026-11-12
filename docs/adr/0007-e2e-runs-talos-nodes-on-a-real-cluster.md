# 0007. e2e runs Talos nodes on a real cluster

Status: Accepted · Date: 2026-10-25 · Design section: 7 (Testing)

Decided: e2e runs Talos nodes on a real cluster, not `talosctl cluster create qemu`

## Context

The design says e2e creates throwaway clusters with `talosctl cluster create
qemu`. That provisioner runs QEMU on the host that invokes it and needs
`CAP_SYS_ADMIN` and `CAP_NET_ADMIN` — root. e2e cannot assume it on the host
that drives it, and a single developer machine cannot host the `standard`
topology anyway.

The alternative is to run Talos nodes as privileged pods on an existing
cluster that exposes real `/dev/kvm` and `/dev/net/tun` and has the memory for
a multi-node topology. A privileged pod there sees real hardware
virtualization, with no nesting involved, and it gives a result better than the
provisioner would:

- A node booting the Image Factory `metal-amd64.iso` for Talos 1.14.3 reaches
  `MachineStatus` `STAGE: maintenance, READY: true`.
- The insecure maintenance API answers on port 50000 from any pod in the
  cluster. (Port-forward to the node works under the `masquerade` binding; the
  harness uses `bridge`, see Consequences.) Tests
  therefore reach the Talos API from a one-shot pod inside the cluster, which
  is also how the controller reaches nodes in production.
- `get disks` shows the ISO as `sr0` and a writable `vda`, so there is a real
  install target, and `get links` shows a real NIC plus bond and dummy
  devices.

That is Ichor's entry point reproduced exactly: a node that is up, unconfigured,
and readable over an unauthenticated connection.

## Decision

e2e runs Talos nodes as privileged pods on an outer cluster (the e2e host
cluster) that has real `/dev/kvm` and `/dev/net/tun`.

CI keeps the `talosctl cluster create qemu` path: GitHub's `ubuntu-24.04`
runners provide `/dev/kvm` and passwordless sudo, and Talos's own
documentation treats them as sufficient. So there are two harnesses, which is
the cost of this decision, and the e2e suite takes the cluster it is given
through `KUBECONFIG` and a set of node endpoints rather than creating one
itself.

`talosctl` behaviour the harness depends on:

- **`talosctl` 1.14.3 has no global `--insecure`.** It is `-i/--insecure` on
  the individual commands, and flags must follow the subcommand.
- **`--cert-fingerprint` pins the server certificate even in maintenance
  mode.** The design notes that insecure apply does not authenticate the node
  and assumes a separate provisioning network. Fingerprint pinning is a second
  defence worth offering once a node's fingerprint is known, and the discovery
  flow should record it.

## Consequences

- e2e gets real disks, a real install target and real maintenance mode, with
  enough memory for the `standard` topology that a single developer machine
  could never host.
- Ichor owns the node manifests that talosctl's provisioner would otherwise have
  provided, and does not get `--presets maintenance` or the provisioner's
  networking for free.
- `masquerade` NATs the node behind its pod IP and
  forwards only the ports declared on the interface, which would mean
  enumerating every port Talos nodes use between themselves — apid, trustd,
  apiserver, etcd, kubelet — plus whatever the inner cluster's CNI
  encapsulates with. `bridge` gives the node the pod IP itself, so there is no
  NAT and no port list: from an ordinary pod, with no ports declared,
  50000 is reachable while 50001, 22 and 6443 are refused — which is the node's
  real state in maintenance mode, where only apid is listening, not a filter.
  node-to-node traffic therefore reduces to pod-to-pod traffic, which Cilium
  already provides, and **Multus is not needed**.
- The QEMU harness creates a cluster with one worker and resets that worker
  back into maintenance mode, because `--presets maintenance` would leave no
  cluster at all and the specs need both a cluster and a maintenance node. It
  learns the worker's address by scraping `talosctl cluster create`'s
  human-readable summary, a format that is not a documented interface. That is
  marked with a `shortcut:` comment and fails loudly rather than silently when
  the scrape comes back empty. Splitting the spec set to avoid the risk would
  narrow what CI checks, which costs more than a red build does.
- The e2e suite must not assume it created the cluster, which is the right
  shape anyway: it is also how `adopt` will be tested later.
- **Nor may it assume it created the nodes.** So the suite consumes a cluster through
  `KUBECONFIG` *and* a set of maintenance-mode node endpoints through the
  environment, failing loudly when either is absent, and each harness's setup
  target supplies them its own way. One set of specs runs on both.
- The host driving the harness and the inner cluster's pod network are mutually
  unreachable: the host cannot reach a node's pod IP directly, and nothing on
  that pod network can reach back to the host. The controller under test
  therefore runs as a pod inside the e2e namespace, on the same flat pod
  network as the nodes; see "Running the controller under test".

## Past maintenance mode

The ISO path works end to end. Booted from the Factory ISO and given a
control-plane config, a node pulls the installer image, writes to `/dev/vda`
and reboots; the new boot reads its configuration from the STATE partition and
runs the boot sequence rather than reinstalling, with apid and trustd
listening about two seconds in. From there the authenticated API answers with
RBAC, `talosctl bootstrap` succeeds, and `get machinestatus` returns
`stage: running, ready: true` with etcd Running and healthy.

Keep the ISO as the boot source. The Factory also publishes a pre-installed
`metal-amd64.raw.xz`, and booting that would skip the install entirely — which
sounds attractive until you notice that **phase 3's whole flow *is* install**:
discover a node in maintenance mode, approve it, install, join. A harness that
boots pre-installed disks cannot exercise any of it.

Constraints the harness has to respect:

- **Use a control-plane config, not a worker.** Talos's certificate
  controller, *for workers only*, blocks on `k8s.Endpoint` being non-empty — the
  address of a real control-plane apiserver — before it will mint apid's
  serving certificate. An isolated worker never satisfies that, so apid and
  trustd never start: the kernel is up, containerd and the kubelet are up, and
  50000/50001 are refused forever. The control-plane certificate path needs
  only local secrets, and comes up about two seconds into the second boot. Any
  single-node Talos-API-reachability fixture must therefore be control-plane
  shaped; that is a Talos constraint, not something a harness can work around.
- **Pin the cluster endpoint to the node's own address.** `talosctl gen config`'s
  placeholder endpoint resolves through the pod gateway to the **host
  cluster's real kube-apiserver**, and Talos's endpoint controllers dial it
  repeatedly on their own. TLS verification rejects every attempt (the test CA
  does not match), so nothing is read or written, but a node on a bridge-bound
  pod network can reach the host cluster's apiserver port, and the harness must
  never leave that endpoint at a default.
- **Give the inner cluster non-overlapping subnets.** The defaults,
  `10.244.0.0/16` and `10.96.0.0/12`, collide with the host cluster's pod
  network, which is where the node's own address comes from. Talos raises an
  `address-overlap` diagnostic and warns that it cannot find a suitable node IP.
- **Console output on the ISO, and only there.** A schematic's
  `extraKernelArgs: [console=ttyS0,115200n8]` gives a serial console for the
  ISO boot. Keeping it *past* the install reboot would need
  `machine.install.extraKernelArgs` and `grubUseUKICmdline: false` in the
  machine config — and on Talos 1.14 that means hand-editing the legacy
  v1alpha1 section of a config whose install and network settings live in their
  own documents (`UnattendedInstallConfig`, `KubeNetworkConfig`,
  `KubeFlannelCNIConfig`), which the API rejects as incompatible.
  **So the harness does not try.** The post-install console is a debugging
  nicety, and it would work against the goal: configure the inner cluster
  through the documents the generator emits rather than against them, and mark
  the missing post-install console with a `shortcut:` naming the Talos release
  that requires the trade.
- **Detach the ISO after install.** It happens to work without doing so,
  because Talos's own first-boot detection finds the STATE partition and runs
  the boot sequence rather than reinstalling. That is incidental and not worth
  relying on.
- **`talosctl bootstrap` can race its own prerequisite.** Immediately after the
  authenticated API starts answering `machinestatus` (the harness's own signal
  that install finished), bootstrap can still return `FailedPrecondition:
  bootstrap is not available yet` — apid being reachable doesn't mean etcd's
  bootstrap RPC is registered yet. Retry it like every other condition the
  harness polls for, instead of treating it as fatal.
- **`MachineStatus.ready` lives at `spec.status.ready`, not `spec.ready`.**
  `spec.status` is itself an object, not a flattening of its parent; the same
  holds for any other MachineStatus field.
- **The inner cluster needs its own `ichor-e2e` namespace.** The harness only
  gets the node and the CRDs up, and the e2e spec's CEL fixtures assume the
  namespace exists on the *inner* cluster. The controller's leader election
  also needs it: `ctrl.Options.LeaderElectionNamespace` is unset in
  `cmd/main.go` and auto-detects from the pod's mounted service-account
  namespace file, which is the *outer* pod's namespace (`ichor-e2e`), so the
  lease lookup against the inner apiserver fails until a namespace of that name
  exists there. The name collides with the *outer* cluster's namespace, which
  does pre-exist: two different clusters, same name, one of them needs
  creating. Harmless, the inner cluster is fully disposable.
- **`nix develop` owns the name `out`.** It exports `out=<store path>` by
  convention, so a Makefile recipe must not reuse that name for the
  kustomize-rendered CRD YAML without `export -n`: the variable keeps the
  export attribute, every later subprocess inherits a 170 KB+ environment
  variable, and some later `exec` (such as `kubectl`) fails with "Argument list
  too long", far from the command that assigned it. `make install`/`uninstall`
  use a different local name.
- **Cleanup must check its target as carefully as setup does.** A stale
  kubeconfig context can make cleanup fail outright, or worse, silently target
  a live cluster that has its own `ichor-e2e` namespace. Both targets share the
  same prerequisite check, and the harness binary itself refuses to run at all
  (`up` or `down`) when `KUBECONFIG` is unset, rather than fall back to
  kubectl's ambient context.
- **`go run` cannot be trusted with a signal-triggered cleanup.** The harness
  traps SIGINT/SIGTERM to run `down()` before exiting, so a killed `up` can't
  strand the node. Any harness step whose correctness depends on catching a
  signal has to run as the actual binary, not under `go run`.
- **A `ReadOnlyMany` ISO disk shared across nodes does not work on RBD-backed
  storage.** rook-ceph's RBD CSI driver refuses the PVC outright
  (`readOnly accessMode is supported only with content source`), so every node
  gets its own copy of the Factory image.
- **`talosctl health`'s `--nodes` takes exactly one node, not the list.**
  `--control-plane-nodes` is the list to check; `--nodes` (`-n`) is the single
  node the check runs *from*. Passing the same multi-node list to both fails
  ("command health requires exactly one node (got 3)").

## e2e does not repeat apiserver-only checks

The design's "Testing" section lists phase-3 features as e2e candidates: the
delete guard's quorum/last-control-plane refusal (ADR 0010), the CNI preset
rules (preset immutability, flannel cannot replace kube-proxy, the
kube-proxy-switch confirmation annotation), `spec.discovery`'s CEL limits, and
the nine secret fields being pruned rather than rejected (ADR 0003).

None of these gets a scenario in this harness. They are apiserver behaviour
(CRD-embedded CEL, `ValidatingAdmissionPolicy`, structural-schema pruning, and
the quorum webhook served over TLS), they are covered against a real
`kube-apiserver` and `etcd` by the envtest suites, and none involves the
controller reconciling a node. Re-running them against a node exercises the
same apiserver code path a second time, slower and more fragile, for no new
coverage: "don't duplicate what a unit/envtest suite already covers" rules it
out.

## One harness invocation at a time

`vmName` and `relayLocalPort` are fixed, not generated, because `up` and `down`
are separate process invocations with no shared state between them. Two
concurrent runs against the same cluster would collide on both. That is
accepted: `make test-e2e`'s short set, and a human running
`setup`/`test-e2e`/`cleanup` by hand, run one invocation at a time against
`ichor-e2e`. Making the names unique would need a state file handed from `up` to
`down` across those separate invocations, for concurrency the harness is not
asked to support.

## Three control planes

The short e2e set uses a single-node path. The three-control-plane topology
(three nodes, bootstrap on the first, the other two joining on their own) runs
nightly only.

## Running the controller under test

Scenarios that need the real controller reconciling against the inner cluster
(removal ordering, the transitioning lock, killing the controller mid-removal
and resuming) hit the boundary stated in Consequences:

- **The controller's webhook cannot be reached from the host driving the
  harness.** The inner apiserver (inside a bridge-bound node on the outer
  cluster's pod network) has to dial the controller's webhook port for every
  `TalosMachine` admission request. A controller process on the host driving
  the harness is not reachable from that pod network at all. A kubeconfig
  pointed at the inner cluster lets the controller's own API calls go out
  through a tunnel; it does nothing for the inner apiserver's calls back in.
- **The controller runs as a pod inside `ichor-e2e` on the outer cluster, not
  behind a Service.** That pod sits on the same flat pod network the nodes do
  (the way the `talosctl` one-shot pods already reach a node's maintenance API
  directly by IP), so it is reachable both ways without a tunnel. A `ClusterIP`
  is only routable within the cluster whose own kube-proxy or Cilium programmed
  it, and the inner cluster knows nothing of the outer one's Services; the
  pod's own IP is the only address that means anything to both sides. The pod
  gets the inner cluster's admin kubeconfig (from `talosctl kubeconfig`) and
  the webhook configuration's `clientConfig.url` points at the pod's own IP.
- **The controller binary is copied into a minimal base-image pod, not
  delivered as an image**, so the harness needs no container runtime. The
  `ValidatingWebhookConfiguration` and the cert `Secret` are applied to the
  inner cluster under the exact names `cmd/main.go`'s cert-controller hardcodes
  (`ichor-validating-webhook-configuration`, `ichor-webhook-server-cert`);
  `config/webhook/`'s own files ship prefix-less and rely on `config/default`'s
  `namePrefix`, which this path applies by hand.
- **The kubeconfig that reaches the pod is not the one the harness publishes.**
  `publish()` rewrites the node's own endpoint to `127.0.0.1:<relayLocalPort>`
  for the host's port-forward tunnel. A pod on the outer cluster's pod network
  needs the node's real address (`https://<node pod IP>:6443`).
- **The webhook cert `Secret` must already exist, empty** (ADR 0006):
  `rotator.CertRotator` only ever `Get`s then `Update`s a Secret and never
  creates one.
- **`open-policy-agent/cert-controller`'s `CertRotator` cannot produce an IP-SAN
  certificate, and never writes certificate files to disk.** In
  `pkg/rotator/rotator.go` (v0.16.0), `generateCert`'s `x509.Certificate`
  template sets `DNSNames` only, never `IPAddresses`; `ExtraDNSNames` is still
  a DNS name, and Go's TLS client verifies an IP dial target against
  `IPAddresses`, so a certificate from this library can never satisfy a
  `https://<pod IP>` URL however it is configured. And there is no
  `os.WriteFile` in the package: `ensureCertsMounted` only *polls* `CertDir`
  for files to exist, and the real deployment's `manager.yaml` populates them by
  mounting the Secret as a volume, which a one-off pod cannot do. The harness
  therefore reads `ca.crt`/`ca.key` from the Secret the rotator already wrote,
  mints a leaf for the pod's own IP signed by that same CA key
  (`test/harness/webhookcert`, `MintLeafForIP`), and copies `tls.crt`/`tls.key`
  into `/tmp/k8s-webhook-server/serving-certs/`. controller-runtime's
  `certwatcher` picks the files up and starts serving within its normal poll
  interval, so `cmd/main.go` needs no change; this certificate never goes
  through the rotator, which merely owns the Secret.
- **The manager waits for every informer it starts to sync**, TalosOperation
  and TalosStorage included, so all five CRDs must be installed on the inner
  cluster or it exits with `failed to wait for talosoperation caches to sync`.
- **`kubectl exec` does not forward stdin to the remote command unless the
  invocation itself is given `-i`/`--stdin`.** Every inner-cluster call that
  pipes a manifest goes through one shared `podExec`, which passes `-i`.
- **Running the real reconciler against already-configured nodes manages them
  for real.** `TalosMachineReconciler.Reconcile` renders and applies a config
  for every approved control-plane `TalosMachine` unconditionally, in the same
  step that adds the removal finalizer. Fixtures for a controller running
  against live nodes therefore either match what the harness already applied or
  deliberately lack a working `secretsRef` Secret (the machine lands on
  `Ready=False/SecretsBundleUnavailable` without touching the Talos API).
- **Admission alone is not evidence that a removal will proceed.** The webhook's
  `ValidateDelete` calls only `CheckControlPlaneQuorum`; the controller's own
  post-admission recheck inside the lock also calls `CheckControlPlaneCount`
  (ADR 0010), which refuses any removal that would leave fewer control planes
  than `controlPlane.count` declares. That second gate is what makes "3->4->3,
  never 3->2" hold while a cluster sits at its declared size.

## Pinning the target cluster

The `KUBECONFIG` check alone does not pin the target: with a multi-context
file, `kubectl` without `--context` still uses its `current-context`. Every
outer-cluster `kubectl` call in the harness therefore goes through
`test/internal/kube.Command`, which always adds
`--context $ICHOR_E2E_KUBE_CONTEXT` and builds a command that fails to start
when the variable is unset. Deletes are additionally refused unless they carry
`-n ichor-e2e` and name their objects (no `-A`, `--all`, `-l`, `-f`). Inner
cluster calls use `kube.InnerCommand(<kubeconfig>, ...)` and never delete.
`talosctl` runs only in pods; `test/internal/talosctl` refuses a call with no
explicit `--nodes`, or one that addresses a protected node (a fixed list of real
machines that no e2e path may touch).

Environment variables an e2e run needs:

| Variable | Meaning |
| --- | --- |
| `KUBECONFIG` | kubeconfig file containing the outer cluster |
| `ICHOR_E2E_KUBE_CONTEXT` | context in it to target; required, no default |

`make test-e2e-prereqs` and the harness binary both refuse to run when
`ICHOR_E2E_KUBE_CONTEXT` is unset or is not a context in the kubeconfig.

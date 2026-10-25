# 0007. e2e runs Talos nodes on a real cluster

Status: Accepted · Date: 2026-10-25 · Design section: 7 (Testing)

Decided: e2e runs Talos nodes on a real cluster, not `talosctl cluster create qemu`

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

The second was tried and it works, with a result better than the first would
have given:

- A node booting the Image Factory `metal-amd64.iso` for Talos 1.14.3 reaches
  `MachineStatus` `STAGE: maintenance, READY: true`.
- The insecure maintenance API answers on port 50000 from any pod in the
  cluster. (Port-forward does work under `masquerade`, which is how
  this was first measured before the binding changed — a detail that cost a
  debugging cycle downstream.) Tests therefore reach the Talos API from a
  one-shot pod inside the cluster, which is also how the controller will reach
  nodes in production.
- `get disks` shows the ISO as `sr0` and a writable 11 GB `vda`, so there is a
  real install target, and `get links` shows a real NIC plus bond and dummy
  devices.

That is Ichor's entry point reproduced exactly: a node that is up, unconfigured,
and readable over an unauthenticated connection.

## Decision

CI keeps the `talosctl cluster create qemu` path: GitHub's `ubuntu-24.04`
runners provide `/dev/kvm` and passwordless sudo, and Talos's own
documentation treats them as sufficient. So there are two harnesses, which is
the cost of this decision, and the e2e suite takes the cluster it is given
through `KUBECONFIG` and a set of node endpoints rather than creating one
itself.

Facts worth keeping, each of which cost a debugging cycle:

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
- Ichor owns the node manifests that talosctl's provisioner would otherwise have
  provided, and does not get `--presets maintenance` or the provisioner's
  networking for free.
- `masquerade` NATs the node behind its pod IP and
  forwards only the ports declared on the interface, which would mean
  enumerating every port Talos nodes use between themselves — apid, trustd,
  apiserver, etcd, kubelet — plus whatever the inner cluster's CNI
  encapsulates with. `bridge` gives the node the pod IP itself, so there is no
  NAT and no port list: measured from an ordinary pod, with no ports declared,
  50000 is reachable while 50001, 22 and 6443 are refused — which is the node's
  real state in maintenance mode, where only apid is listening, not a filter.
  node-to-node traffic therefore reduces to pod-to-pod traffic, which Cilium
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
- **Nor may it assume it created the nodes.** So the suite consumes a cluster through
  `KUBECONFIG` *and* a set of maintenance-mode node endpoints through the
  environment, failing loudly when either is absent, and each harness's setup
  target supplies them its own way. One set of specs runs on both.

## Past maintenance mode, resolved

**The ISO path works end to end, and was
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
  up, and 50000/50001 are refused forever. The
  control-plane certificate path needs only local secrets, and comes up about
  two seconds into the second boot. Any single-node Talos-API-reachability
  fixture must therefore be control-plane shaped; that is a Talos constraint,
  not something a harness can work around.
- **Pin the cluster endpoint to the node's own address.** `talosctl gen config`'s
  placeholder endpoint resolved through the pod gateway to the **host cluster's
  real kube-apiserver**, and Talos's endpoint controllers dialled it repeatedly
  on their own. Every attempt was rejected by TLS verification — the test CA
  does not match — so nothing was read or written, but a node on this
  bridge-bound pod network can reach the production control plane's apiserver
  port, and the harness must never leave that endpoint at a default.
- **Give the inner cluster non-overlapping subnets.** The defaults,
  `10.244.0.0/16` and `10.96.0.0/12`, collide with the host cluster's pod
  network, which is where the node's own address comes from. Talos raises an
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
  relying on.

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
- **Cleanup must check its target as carefully as setup does.** Observed for real: a dangling `kind-kind` context
  left over from before this project moved off Kind made cleanup fail
  outright (connection refused) instead of silently targeting the wrong
  cluster — but "fails outright" was luck, not a guarantee, for a target that
  could just as easily have been a live cluster with its own `ichor-e2e`
  namespace. Both targets now share the same prerequisite check, and the
  harness binary itself refuses to run at all (`up` or `down`) when
  `KUBECONFIG` is unset, rather than fall back to kubectl's ambient context.
- **`go run` cannot be trusted with a signal-triggered cleanup.** The harness
  traps SIGINT/SIGTERM to run `down()` before exiting, so a killed `up` can't
  strand the node. Any harness step whose correctness depends on catching a
  signal has to run as the actual binary, not under `go run`.

## Phase 3: no new scenario was added, and why

Design's "Testing" section lists phase-3 features as e2e candidates: the
delete guard's quorum/last-control-plane refusal (ADR 0010), the CNI preset
rules (preset immutability, flannel cannot replace kube-proxy, the
kube-proxy-switch confirmation annotation), `spec.discovery`'s CEL limits, and
the nine secret fields being pruned rather than rejected (ADR 0003). The
roadmap names one of these explicitly: "an explicit test has to show that the
annotation alone does not let the last control plane go."

All four already exist against a real apiserver, so none were duplicated
into this harness:

- **Deletion guard quorum**, including the exact sequential case the roadmap
  asks for (three control planes, delete one — allowed, delete another —
  refused for quorum, even with `allow-delete` set): `internal/webhook/v1alpha1/
  talosmachine_webhook_test.go`, `Describe("TalosMachine Webhook
  control-plane quorum")`, run under `webhook_suite_test.go`'s envtest
  environment with the real webhook server and its TLS serving certificate —
  the actual admission path, not a bare function call.
- **CNI preset immutability, the kube-proxy-switch `ValidatingAdmissionPolicy`,
  and `spec.discovery`'s CEL limits** (IPv4-only, `/24` ceiling, `MaxItems=4`
  CIDRs): `api/v1alpha1/validation_test.go`, under `api/v1alpha1/
  suite_test.go`'s envtest environment. That suite's own comment says why the
  VAP is included even though it isn't controller-gen output: it loads
  `config/admission/kubeproxy-switch-policy.yaml` verbatim, "so the test
  exercises the exact manifest a real deploy installs rather than a copy that
  could drift from it."
- **The nine secret fields being pruned, not rejected**: the same file,
  `It("drops all nine secret fields from a Config document, however it
  arrives")`.

envtest runs a real `kube-apiserver` and `etcd` binary — CRD-embedded CEL,
`ValidatingAdmissionPolicy` and structural-schema pruning are all apiserver
features, identical under envtest's apiserver and this harness's inner one.
None of these four checks involves the controller (the quorum rule is the
only webhook among them, and envtest already stands up the real webhook
server over TLS), so re-running them against a node would exercise the
same apiserver code path a second time, slower and more fragile, for no new
confidence — exactly what "don't duplicate what a unit/envtest suite already
covers" rules out.

## Fixed `vmName`/`relayLocalPort`: why the collision risk was left as-is

`vmName` and `relayLocalPort` are fixed, not generated, because `up` and
`down` are separate process invocations with no shared state between them
(see `vmName`'s own doc comment). Two concurrent runs against the same
cluster would collide on both. Left as-is: every caller of this harness —
`make test-e2e`'s short set, and a human running `setup`/`test-e2e`/`cleanup`
by hand — runs one invocation at a time against `ichor-e2e`; nothing here
runs two. Making the names unique would need a state file handed from `up` to
`down` across those separate invocations, for concurrency nobody has asked
this harness to support.

## Three control planes: etcd healthy, confirmed; removal ordering, not yet

Separate from `up`/`down`: the short e2e set's single-node path is untouched, and three
nodes (~40Gi of Ceph for a few minutes) is nightly-only.

**Confirmed for real** (`talos-cluster01`, three nodes, bootstrap on the first, the other
two joining on their own): all three reach `running`/`ready`, and `talosctl health
--control-plane-nodes <all three>` passes every check through "all k8s nodes to report
schedulable". `talosctl etcd members` against any one of them shows all three as
voters, not learners:

```
NODE           ID                 HOSTNAME         PEER URLS                   CLIENT URLS                 LEARNER
10.244.1.223   42e7cb66e97d0e6b   ichor-e2e-ha-2   https://10.244.1.226:2380   https://10.244.1.226:2379   false
10.244.1.223   c1a928177d9c3bd5   ichor-e2e-ha-0   https://10.244.1.223:2380   https://10.244.1.223:2379   false
10.244.1.223   f80ae7490b84eb49   ichor-e2e-ha-1   https://10.244.1.173:2380   https://10.244.1.173:2379   false
```

Two things cost a debugging cycle each, both already fixed in the harness:

- **A single ISO disk shared read-only across all three nodes does not work on
  this cluster's storage.** Tried first, to avoid three copies of the identical Factory
  image: rook-ceph's RBD CSI driver refused the `ReadOnlyMany` PVC outright --
  `rpc error: code = InvalidArgument desc = readOnly accessMode is supported only with
  content source` -- confirmed against a real, standalone `disk`.
- **`talosctl health`'s `--nodes` takes exactly one node, not the list.**
  `--control-plane-nodes` is the list to check; `--nodes` (`-n`) is the single node the
  check runs *from*. Passing the same three-node list to both failed outright
  ("command health requires exactly one node (got 3)") -- confirmed for real, fixed by
  passing `controlPlaneNodes[0]` to `--nodes`.

**Not attempted this round: the removal-ordering scenarios themselves** (quorum/
last-control-plane admission refusal, a single removal completing in order, the
transitioning lock, killing the controller mid-removal and resuming). All of them need
the real controller reconciling against this inner cluster, which surfaces a boundary
"run outside the inner cluster" doesn't resolve on its own:

- **The controller's webhook cannot be reached from "the workstation."** The inner
  apiserver (inside a bridge-bound node on `talos-cluster01`'s pod network) has to dial
  the controller's webhook port for every `TalosMachine` admission request. A controller
  process running on the machine driving this harness is not reachable from that pod
  network at all -- the same one-way boundary ADR 0007 already states for `kubectl
  port-forward` ("the workstation running this harness cannot reach the node's pod IP
  directly"), just in the opposite direction this time: nothing on that pod network can
  reach *back* to the workstation either. A kubeconfig pointed at the inner cluster
  lets the controller's own API calls go out through a tunnel; it does nothing for the
  inner apiserver's calls back in.
- **The fix is where the controller runs, not what it's pointed at.** A pod inside
  `ichor-e2e` on `talos-cluster01` sits on the same flat pod network the nodes do --
  exactly how the existing `talosctl` one-shot pods already reach a node's maintenance
  API directly by IP, no relay needed. A controller pod there, given the inner
  cluster's admin kubeconfig (from `talosctl kubeconfig`, already has full access --
  no separate RBAC setup needed on either cluster) and with the webhook config's
  `clientConfig.url` pointed at that pod's own IP, would be reachable both ways without
  a tunnel in either direction.
- **This sandbox has no `docker`/`podman`**, confirmed (`which docker` finds nothing),
  so there is no image to run that pod from through the normal path
  (`docker-build`/`Dockerfile`). The workable alternative is `kubectl cp`-ing a
  statically built `bin/manager` into a running pod on a minimal base image and
  `exec`-ing it there, which also needs the webhook `ValidatingWebhookConfiguration`
  and cert `Secret` applied to the inner cluster under the exact names
  `cmd/main.go`'s cert-controller hardcodes (`ichor-validating-webhook-configuration`,
  `ichor-webhook-server-cert` -- `config/webhook/`'s own files ship prefix-less,
  relying on `config/default`'s `namePrefix` to add it, which this path would have to
  do by hand instead).
- **Separately, running the real reconciler at all means it starts managing these
  already-configured nodes for real** -- `TalosMachineReconciler.Reconcile` renders and
  applies a config for every approved control-plane `TalosMachine` unconditionally,
  the same step that also adds the removal finalizer. Making the `TalosCluster`/
  `TalosMachine` specs match what `upHA` already applied closely enough that this is a
  safe no-op sync rather than a surprise reconfiguration of a live three-node etcd
  cluster is its own piece of care, not a given.

None of this is a dead end -- the pod-on-the-outer-cluster shape above is a concrete,
believable path -- but it is three new pieces of infrastructure (image delivery, webhook
TLS/naming, a config that matches reality closely enough) stacked on top of a live,
Ceph-backed three-node cluster, each a plausible place to fail twice and have to stop.
Given that stack and the time already spent standing the cluster up, it was left for a
dedicated pass rather than attempted inline here. Verified and cleaned up instead: the
part that needed real hardware and nothing else -- three control planes, real etcd,
real quorum.

## The controller's webhook, reachable from the inner apiserver: confirmed working

The proposal above (a controller pod inside `ichor-e2e`, on the same pod network the nodes
use) is no longer untested. Scope on purpose: reachability only, stopping at seeing
admission refuse one delete. Quorum, last-control-plane, removal ordering,
`transitioning` and chaos-resume are still deferred to a dedicated pass.

**Confirmed for real**, against a single-node inner cluster: a `TalosMachine` deleted
without `ichor.maesh.dev/allow-delete` is refused by the live admission webhook, and the
identical object deletes cleanly once annotated:

```
$ kubectl delete talosmachine wh-machine
Error from server (Forbidden): admission webhook "vtalosmachine-v1alpha1.kb.io" denied the request: delete refused: set the annotation ichor.maesh.dev/allow-delete="true" to allow it

$ kubectl annotate talosmachine wh-machine ichor.maesh.dev/allow-delete=true
talosmachine.ichor.maesh.dev/wh-machine annotated
$ kubectl delete talosmachine wh-machine
talosmachine.ichor.maesh.dev "wh-machine" deleted from default namespace
```

That first error travelled the whole real path: the inner apiserver dialled
`https://<pod IP>:9443/validate-...` over TLS, verified the leaf certificate against the
`caBundle` in the `ValidatingWebhookConfiguration`, and the real `cmd/main.go` binary
(statically built, `kubectl cp`'d in, no image) ran `deleteguard.CheckDelete` and
returned the refusal.

### How it was built, one piece per trap

- **A pod anywhere in `ichor-e2e`, not a Service.** `alpine:3` running `sleep infinity`,
  `kubectl cp`'d the statically-built `bin/manager` binary and the inner cluster's
  kubeconfig into it, then `kubectl exec`'d the binary with that kubeconfig and
  `POD_NAMESPACE=default`. No image build (out of scope). A Service was never an option
  for the inner apiserver to reach: a `ClusterIP` is only routable within the cluster
  whose own kube-proxy/Cilium programmed it -- the inner cluster has no idea the outer
  one's Services exist, flat pod network or not. The pod's own IP, on that same flat
  network, is the only address that means anything to both sides.
- **The kubeconfig that reaches the pod is not the one the harness publishes.**
  `publish()` rewrites the node's own endpoint to `127.0.0.1:<relayLocalPort>` for the
  *workstation's* port-forward tunnel (ADR 0007's existing boundary). A pod on the outer
  cluster's own pod network needs the opposite: the node's real address
  (`https://<node pod IP>:6443`), which a one-line `sed` restores from the published
  kubeconfig before copying it in.
- **Leader election's namespace is not `POD_NAMESPACE`.** The rotator's `SecretKey`
  namespace comes from `POD_NAMESPACE` (set to `default`, a namespace the fresh inner
  cluster already has); `ctrl.Options.LeaderElectionNamespace` is unset in `cmd/main.go`
  and auto-detects from the pod's own mounted service-account namespace file --
  `ichor-e2e`, since that is the *outer* pod's namespace, not a namespace on the inner
  cluster at all. `leaderelection.go: Error retrieving lease lock` against the inner
  apiserver, repeating forever, was the symptom. Fixed by creating an `ichor-e2e`
  namespace on the inner cluster too -- harmless, the inner cluster is fully disposable,
  and leader election stops caring once the namespace exists.
- **The webhook cert `Secret` must already exist, empty.** Confirmed by reading
  `config/webhook/secret.yaml` (ships metadata-only, matching `dist/chart/
  chart_test.go`'s own `checkCertSecretHasNoData`): `rotator.CertRotator` only ever
  `Get`s then `Update`s a Secret; it never creates one, so a cluster with no such Secret
  gets `Secret "ichor-webhook-server-cert" not found` forever. One
  `kubectl create secret generic ichor-webhook-server-cert` in the inner cluster's
  `default` namespace fixed it.
- **`open-policy-agent/cert-controller`'s `CertRotator` cannot produce an IP-SAN
  certificate, and never writes to disk at all.** Read the vendored source
  (`pkg/rotator/rotator.go`, v0.16.0) to confirm both: `generateCert`'s `x509.Certificate`
  template sets `DNSNames` only, never `IPAddresses` -- `ExtraDNSNames` exists but is
  still a DNS name, and Go's TLS client verifies an IP dial target against
  `IPAddresses`, not `DNSNames`, so a cert from this library can never satisfy a
  `https://<pod IP>` URL no matter how it's configured. And there is no `os.WriteFile`
  anywhere in the package -- `ensureCertsMounted` only *polls* `CertDir` for files to
  already exist; the real deployment's `manager.yaml` populates them by mounting the
  Secret as a volume, which this one-off pod has no way to do. Fix: read `ca.crt`/
  `ca.key` straight out of the Secret `rotator` already wrote, mint a new leaf for the
  pod's own IP signed by that same CA key (a ~70-line scratch Go program, not part of the
  repo), and `kubectl cp` `tls.crt`/`tls.key` into `/tmp/k8s-webhook-server/serving-certs/`
  myself. `controller-runtime`'s own `certwatcher` picked the files up and started
  serving within its normal poll interval -- no code in `cmd/main.go` needed to change;
  this cert never goes through the rotator at all; the rotator just happens to be the
  Secret's owner. Verified empirically before committing to this: a first `kubectl
  delete` attempt got `connection refused` on port 9443 (reachable, nothing listening
  yet -- a useful intermediate signal that the network path itself was already correct),
  and the webhook server started serving the moment the hand-placed cert files appeared.

### Cleanup, verified including the new pieces


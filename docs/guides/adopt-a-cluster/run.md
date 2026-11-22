---
sidebar_order: 5
---

# Run it

![The four phases of an adopt run. 1 Write: the bundle Secret, TalosCluster, held TalosMachines, patches and Ichor. 2 Cilium takeover, waiting for CNIHandover. 3 Release the holds, control planes first, each after a zero-drift check. 4 Turn sign-in on. A held machine carries the hold-apply annotation and the AdoptionHeld condition and nothing is applied to it.](../../images/adopt-run-phases.light.drawio.svg)

*Nothing is applied to a node until its hold is released after a zero-drift check.*

Run the plan's command without `--plan`, plus the flags the plan told you to add:

```sh
ichor adopt --cluster prod --node 10.0.0.11 --recover-secrets \
  --recover-from 10.0.0.11 --accept-cilium-diff --accept-cilium-changes
```

Adopt prints one line per step, the plan's findings as lines of their own (`!`
marks what needs your attention: a skipped machine, Ichor-owned settings, a
Cilium difference, the Helm record that will be deleted) and `Done in` and
`Next` at the end. It asks for consent before it writes anything (`--yes` skips
it). The patches are not printed; `-o yaml` (or `json`) prints them on stdout
while the `adopt: ...` lines stay on stderr. A cluster with no CNI that Ichor
has never seen looks like this (the `…` progress lines of each step are left out):

```text
Ichor adopt · prod
namespace ichor-system · discovering from 10.0.0.11
✓ Read cluster — Talos v1.14.5 · Kubernetes v1.36.6 · 2 read · no TalosCluster yet
  no CNI found: Cilium will be installed by adopt (Cilium 1.20.4, tunnel the default, kube-proxy enabled) as Ichor's own Helm release, then Ichor is installed, then the cluster is adopted with the managed cilium preset
✓ Inspect CNI — CNI: none (no CNI installed, every node NotReady)
! after the nodes are released adopt will turn Ichor's sign-in on: create the first administrator "admin" and switch ichor-ui to sign-in at https://localhost:8443, and set spec.authentication.ichor on the TalosCluster, which marks every control plane APIServerRestartRequired: kube-apiserver restarts on each control plane, one at a time, when you run `ichor rollout config` (until then it runs as it is; with a single control plane the Kubernetes API is unavailable while it restarts)
  node "uuid-10.0.0.10": role controlplane, install disk not stated in the node's configuration
  node "uuid-10.0.0.11": role worker, install disk not stated in the node's configuration
  node "uuid-10.0.0.10": already matches a fresh render, nothing to do
  node "uuid-10.0.0.11": already matches a fresh render, nothing to do
✓ Plan — no patch needed
✓ Secrets bundle — stored under ~/.ichor/clusters/prod/
✓ Install Cilium — Cilium is installed and the nodes are Ready
✓ Create resources — 5 created, 0 unchanged
✓ Cilium hand-over — Helm release owned by Ichor
✓ Release holds — 2 released
✓ Ichor users — sign-in: users are on in ichor-ui (issuer https://localhost:8443); first admin "admin" created
✓ Authentication — spec.authentication.ichor is set
Done in 4m12s
! the Talos and Kubernetes client certificates of the previous administrators, and the CA keys held by whoever created this cluster, remain valid; consider `ichor rotate ca`
  the nodes are now managed by Ichor: change their configuration through Ichor (a TalosConfigPatch, or `ichor rollout config`), not on the node
! spec.authentication.ichor is set; the controller renders the kube-apiserver authentication config next. Applying it restarts kube-apiserver on each control plane, so nodes report APIServerRestartRequired until you run `ichor rollout config` (or Apply in the Overview page's attention list), which rolls one control plane at a time
Password for admin (shown once, stored nowhere; they must change it at the first sign-in): ...
Next
  ichor get cluster prod
  ichor get nodes
  ichor ui
```

The reminder to back up a recovered secrets bundle is also a `!` line.

| Phase | What happens |
|---|---|
| 1 | Writes the bundle Secret, the `TalosCluster`, the `TalosMachine`s (held), the patches and, if Ichor is not running, its install manifest. With no CNI, Cilium is installed first. A held machine has the annotation `ichor.maesh.dev/hold-apply`; the controller reports its drift (condition `AdoptionHeld`) and applies nothing. |
| 2 | Takes over Cilium and waits for the `CNIHandover` condition. Skipped with `--cni-unmanaged`. |
| 3 | Releases the holds machine by machine, control planes first, each only after a live zero-drift check. The controller applies the first render. |
| 4 | Turns sign-in on once every hold is released (see [Sign-in](sign-in.md)): the first administrator, the switch of ichor-ui, then `spec.authentication.ichor`. |

Only approved machines are read; a pending one is skipped with a message.
Approve it and run adopt again. If adopt stops half-way, run it again: the
writes are idempotent, and a run where every machine already matches a fresh
render says so and exits.

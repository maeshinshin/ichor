---
sidebar_order: 5
---

# Run it

Run the same command without `--plan`, plus the flags the plan told you to add:

```sh
ichor adopt --cluster prod --node 10.0.0.11 --recover-secrets \
  --recover-from 10.0.0.11 --accept-cilium-diff --accept-cilium-changes
```

Adopt shows its steps like `ichor init` does: one line per step, the findings of
the plan as lines of their own (`!` for what needs your attention: a skipped
machine, Ichor-owned settings, a Cilium difference, the Helm record that will be
deleted), and a `Done in` and `Next` at the end. It asks for consent before it
writes anything (`--yes` skips the prompt). The patches themselves are not
printed; add `-o yaml` (or `json`) to print them on stdout, in which case adopt
keeps writing its `adopt: ...` lines on stderr as before. A run on a cluster with
no CNI that Ichor has never seen looks like this:

```text
Ichor adopt · prod
namespace ichor-system · discovering from 10.0.0.11
✓ Read cluster — Talos v1.14.5 · Kubernetes v1.36.6 · 2 read · no TalosCluster yet
  no CNI found: Cilium will be installed by adopt (Cilium 1.20.4, tunnel the default, kube-proxy enabled) as Ichor's own Helm release, then Ichor is installed, then the cluster is adopted with the managed cilium preset
✓ Inspect CNI — CNI: none (no CNI installed, every node NotReady)
  machine "uuid-10.0.0.10": already matches a fresh render, nothing to do
  machine "uuid-10.0.0.11": already matches a fresh render, nothing to do
✓ Plan — no patch needed
✓ Secrets bundle — stored under ~/.ichor/clusters/prod/
✓ Install Cilium — Cilium is installed and the nodes are Ready
✓ Create resources — 5 created, 0 unchanged
✓ Cilium hand-over — Helm release owned by Ichor
✓ Release holds — 2 released
✓ Ichor users — users are on in ichor-ui (issuer https://localhost:8443); first admin "admin" created
✓ Authentication — spec.authentication.ichor is set
Done in 4m12s
! the Talos and Kubernetes client certificates of the previous administrators, and the CA keys held by whoever created this cluster, remain valid; consider `ichor rotate ca`
! spec.authentication.ichor is set; the controller renders the kube-apiserver authentication config next. Applying it restarts kube-apiserver on each control plane, so nodes report APIServerRestartRequired until you run `ichor rollout config` (or Apply in the Overview page's attention list), which rolls one control plane at a time
Password for admin (shown once, stored nowhere; they must change it at the first sign-in): ...

Next
  ichor get cluster prod
  ichor get nodes
  ichor ui
```

The consent prompt and the reminder to back up a recovered secrets bundle stay
as they were (the reminder is a `!` line). Then it works in phases:

1. Writes the bundle Secret, the `TalosCluster`, the `TalosMachine`s (held),
   the patches and, if Ichor is not running yet, Ichor's install manifest. On a
   cluster with no CNI Cilium is installed before this, as above.
   A held machine has the annotation `ichor.maesh.dev/hold-apply`; the
   controller reports its drift (condition `AdoptionHeld`) and applies
   nothing.
2. Takes over Cilium and waits for the `CNIHandover` condition. Skipped with
   `--cni-unmanaged`.
3. Releases the hold machine by machine, control planes first, each only after
   a live zero-drift check. The controller applies the first render.
4. Turns sign-in on (see [Sign-in](sign-in.md)), once every hold is released: the
   first administrator, the switch of ichor-ui, then `spec.authentication.ichor`
   on the `TalosCluster`.

Only approved machines are read. A pending one is skipped with a message;
approve it and run adopt again.

If adopt stops half-way, run it again. The writes are idempotent. A run where
every machine already matches a fresh render says so and exits.

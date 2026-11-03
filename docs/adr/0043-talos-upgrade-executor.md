# 0043. The Talos upgrade executor is one per-node function behind ADR 0026's seam

Status: Accepted · Date: 2026-10-26 · Design section: Upgrades, Storage
Decided: Implement Talos upgrades as a single AdvanceMachineFunc (evacuation hook, Upgrade API, boot-ID-based reboot detection, version/readiness/etcd verification, A/B fallback is a failure) with all resumability held in TalosMachine conditions, and no Ichor-side cordon, drain or Preserve handling

## Context

ADR 0026 already supplies the lock, the status-held state machine,
pause-on-failure, `maxUnavailable`, hold annotations, label ordering and
one-control-plane-at-a-time. It leaves one seam, `AdvanceMachineFunc`. This ADR
is what fills it for `kind: Talos`.

## Decision

- Per call, state is re-derived from the node (`Version`, `BootID`) and two
  TalosMachine conditions: `TalosUpgrade` (True from the acknowledged Upgrade
  call; its message holds the boot ID read just before the call) and
  `StorageEvacuation` (True from the evacuation write; its transition time is
  the timeout clock). A restarted controller resumes from those.
- Not yet upgraded: check `pkg/upgrade` path and Kubernetes pairing, node
  Ready, then evacuate, then Upgrade API with the factory installer image.
  etcd health is not re-checked: Talos 1.14.3's `Server.Upgrade`
  (`internal/app/machined/internal/server/v1alpha1/v1alpha1_server.go`) calls
  `etcd.Client.ValidateForUpgrade` for non-worker nodes unless force or
  preserve is set, and we set neither.
- After the call: dial or RPC errors are "still rebooting". An unchanged boot ID
  means not rebooted yet. A changed boot ID with a version that is not the
  target is the A/B slot having fallen back: an error, which ADR 0026 turns
  into a paused operation. Otherwise wait for Ready and, on a control plane,
  etcd membership, then end evacuation and mark converged.
- Evacuation timeout is a rollout pause (error, scheduling re-enabled), never
  "proceed". No TalosStorage selecting the node means nothing to evacuate.
  On success scheduling is re-enabled only after the node is verified.
- No cordon, drain or uncordon: Talos's `Sequencer.Upgrade` does them.
  `UpgradeRequest.Preserve` is deprecated and ignored by the installer, so it
  is not handled.
- The version contract is never bumped automatically.
- Kubernetes and Ichor kinds are not wired; `cmd/main.go` returns an error for
  them rather than treating them as Talos upgrades.

## Consequences

`pkg/storage` has its first caller here. Node removal (`removal.go`) now runs
the same evacuation before its drain: the decision lives once in
`evacuateMachine` / `endMachineEvacuation` (`evacuation.go`), which both the
upgrade and removal call, with the same `StorageEvacuation` condition as the
resume marker and timeout clock. A removal-side timeout stops the removal
(scheduling re-enabled, Warning Event) and retries evacuation; it never drains.

## E2E observation (2026-10-26)

Run on the 3-control-plane KubeVirt cluster (Talos v1.14.3, Cilium, no storage),
`TalosCluster.spec.talosVersion` set to v1.14.4 (factory schematic
`376567988a...`, the harness's), then a `TalosOperation` `kind: talos`. The
controller ran in a pod on the outer cluster with webhooks off. The
`pkg/render` `vv` image bug (7dcc455) was not hit: the harness builds the node
config with the machinery generator directly, and the controller only
dry-ran it (`RebootRequired`, not applied).

- **First attempt failed, a real bug**: `Upgrade` ran under the 15 s
  `etcdDialTimeout` context. The RPC blocks while the node pulls and installs
  the installer image, so it returned `DeadlineExceeded`, the operation went
  Paused, and the node was untouched (still v1.14.3). Fixed with a separate
  10-minute bound for that call (`upgradeCallTimeout`).
- **Second attempt (created 14:02:53 JST, finished 14:12:13):**
  the operation went Paused at once with no machines (a cluster lock still held
  from the deleted first operation), then started at about 14:05. Order:
  ha-0 `TalosUpgrade=True/UpgradeRequested`, then Converged (about 14:06:35);
  ha-1 NotReady,SchedulingDisabled during the reboot, Converged (about
  14:09:54); ha-2, Converged; operation `Completed` at 14:12:13. Never two
  nodes at once.
- Afterwards `talosctl version` on all three: v1.14.4; `etcd members`: three
  voters, none learner; `get machinestatus`: running, ready; Kubernetes nodes
  Ready. No A/B fallback occurred.
- Rough edges seen: while a control plane rebooted the operation briefly went
  Paused (`clusterlock: held by "ichor-controller"` and API connection errors in
  the controller log, because the test controller's kubeconfig names one node's
  apiserver) and resumed on its own; the status showed `Paused` with
  `ha-1 InProgress` for about a minute. The evacuation hook was not exercised
  (no TalosStorage), only the pass-through path.
- Drain/cordon is Talos's own; the node showed SchedulingDisabled during
  its turn.

## Amendment (real hardware, Talos v1.14.3)

The drain assumption above holds for `Upgrade` and is re-checked against the source: `Sequencer.Upgrade`
(`internal/app/machined/pkg/runtime/v1alpha1/v1alpha1_sequencer.go:440-455`) runs the `drain` phase
(`CordonAndDrainNode`, which drains through `pkg/kubernetes/kubernetes.go:242`, a five-minute bound, DaemonSet and
mirror pods skipped) unless node registration is skipped, and the upgrading stage cordons the node
(`controllers/k8s/node_cordoned_spec.go:73-76`). It does not hold for `Reboot`, which ADR 0080 now drains itself. The
executor also no longer requires `spec.hostname`: the node name comes from the node's `Nodename` resource when the
field is unset (ADR 0080, "Observed on hardware").
- Amendment (ADR 0113): the target of a Talos upgrade is the release set's Talos, and the admission webhook and the operation's planning refuse any other. A Talos step of the cluster rollout also writes the release set's version contract in the same write. The executor is unchanged.

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

`pkg/storage` has its first caller here. Node removal (`removal.go`) still
performs no evacuation; that is a separate unit.

# 0076. Approved machines in maintenance mode are provisioned over the insecure API and followed to running

Status: Accepted · Date: 2026-10-26 · Design section: Nodes (Approval), Configuration reconcile

Decided: an approved, never-converged machine that the bundle's identity cannot read is probed on the maintenance API; if it answers there and is the recorded machine and the render names an install disk, the render is applied once insecurely, and the node is then followed by requeue until MachineStatus says running.

## Context

Architecture.md's Approval paragraphs say the controller "renders the config, applies it insecurely, and follows the
node through install, reboot and join". Nothing implemented it: the TalosMachine reconciler dials with the bundle's
os:admin identity, which a node in maintenance mode does not trust, so an approved worker sat at
`Ready=False/CurrentConfigUnavailable` (x509: certificate signed by unknown authority) forever.

## Decision

- **Detecting maintenance mode.** Not by parsing the TLS error. The reconciler first tries the authenticated
  `CurrentConfig`. Only if that fails, `status.appliedConfigHash` is empty (the machine never converged) and a
  `MaintenanceClient` is wired does it ask the insecure API for the node's identity. A node answers that only while it
  is in maintenance mode, so "the insecure read succeeded" is the signal. A machine that has converged once never
  takes this path: it has a configuration, and a node that has one refuses an insecure apply anyway.
- **Verify before applying.** The identity read over the maintenance API must match `spec.identity`: the UUID always,
  the serial and the MAC set wherever the spec records them (an operator-registered weak identity is compared on what
  it has). Otherwise `Ready=False/IdentityMismatch` and nothing is applied: someone else's machine is on that address.
- **Install disk.** The rendered bytes must name where to install: `UnattendedInstallConfig` (v1.14), or
  `machine.install.disk`/`diskSelector`. Otherwise `InstallDiskUnknown`, nothing applied. The installer image is not
  checked: Talos falls back to the running version's.
- **Apply.** `MaintenanceClient.ApplyConfiguration` (mode AUTO, what `talosctl apply-config --insecure` sends) with its
  own 60 s timeout. A control plane first takes the cluster lock through `acquireDisruptiveLock` with
  `completesTransition=true`: a join is what turns an even count (a replacement in progress) back into an odd one, so
  the transitioning refusal must not apply to it. The shutdown block, the lock and the over-cap hold (ADR 0054) and the
  CA-rotation hold (ADR 0063) still do. The lock is held only for the apply call.
- **Follow.** The RPC returning means "stored". `status.configStoredAt` records it and is what stops any further
  insecure apply. After that each reconcile requeues every 15 s: if the node answers insecurely it is still in
  maintenance (`AwaitingInstall`); if neither API answers it is `NodeRebooting`; once the authenticated client reads
  it, the stage comes from MachineStatus (`TalosClient.MachineStage`): `NodeInstalling`, `NodeRebooting`, `NodeStarting`, and on `running` the `Provisioned` condition goes
  True (`NodeJoined`) and the ordinary configuration reconcile takes over. `status.stage` holds the last stage;
  `installing` right after the store and `rebooting` while unreachable are inferred, `running` is observed.
- **Conditions.** `Ready` carries the same reason as `Provisioned` while following. Reasons: `ConfigStored`,
  `AwaitingInstall`, `NodeInstalling`, `NodeRebooting`, `NodeStarting`, `NodeJoined`, `IdentityMismatch`,
  `InstallDiskUnknown`; unchanged: `CurrentConfigUnavailable` when neither API answers before anything was stored.
- **Events.** `ConfigApplied` when stored, `NodeJoined` when the node runs.
- **Timeouts.** Every Talos call has its own: 20 s for dial/identity/stage/current-config reads, 60 s for the apply.
  The install is followed by requeue, never a long call.
- **Schema.** `TalosMachine.status` gains `stage` and `configStoredAt` (status only; no spec change).

## Follow-ups from the first hardware run

- **UI backend reads.** `GetNode`, `GetNodeTalosDetails` and the log stream dial an approved machine with the bundle
  identity, which a node in maintenance mode does not trust. `dialNode` now applies the same rule as the controller:
  for an approved machine with an empty `status.appliedConfigHash` it probes the authenticated connection (a hardware
  read) and, if that fails, reconnects over the maintenance API; `GetNodeTalosDetails.maintenance_mode` is true then.
  A machine that has converged never falls back.
- **Setting the install disk later.** A machine approved without `spec.install` sits at `InstallDiskUnknown`. The new
  `MachineService.SetMachineInstall` writes only `spec.install` (as the user, with the usual GitOps-tracked refusal,
  pull request and override handling) and is refused for a pending machine or one that already has an applied
  configuration. The machine page offers it, with the approval form's disk picker, while that reason is set.
- **Log level.** The failed authenticated read that hands a never-converged machine to the provisioning path is logged
  at V(1) without a stack; `ERROR` is kept for the case where nothing takes over.

## Consequences and open questions

- A node stuck in maintenance after the store is never re-applied automatically (conservative); recovery is to reset or
  re-register the machine. A deadline after which the reconciler would try again is left out.
- The first control plane of a new cluster also needs `etcd bootstrap`; this change joins nodes, it does not bootstrap.
- If the status write after a successful apply fails and the node still answers insecurely, the next reconcile applies
  the same bytes again; a node that already stored them rejects or ignores that.
- A spec that records a UUID but whose node reports none (a weak hardware identity) is refused as a mismatch.
- MachineStatus readability over the insecure API was not verified, so maintenance is inferred from the identity read
  succeeding rather than read as a stage.

- Addendum: `spec.hostname` is rendered at the machine layer, over the patches: the `HostnameConfig` document with `auto: off` from the v1.14 contract, `machine.network.hostname` before it; changing it stays reboot-required in `pkg/fieldmeta`. Dials use `DialEndpoint()` (ADR 0086).

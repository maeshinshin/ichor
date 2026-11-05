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

## Amendment 2026-11-05: a converged machine whose node fell back to maintenance mode is re-provisioned

Found on hardware (ADR 0123 run): an approved machine whose disk was wiped (`talosctl reset --wipe-mode all`) or replaced sat at `Ready=False CurrentConfigUnavailable` for good, because only a never-converged machine (`status.appliedConfigHash` empty) was probed on the maintenance API. The only way out was deleting it (`allow-delete`) and approving it again.

Decided: **automatic, decided by the user on 2026-11-05.** When the authenticated read of a converged machine fails, the controller probes the maintenance API like a first provisioning (with a 5 s bound, since a node that is merely down is probed every round). If the node answers there with the approved identity (`identityMatches`: UUID always, serial and MACs wherever the approval recorded them) the rendered configuration is sent again with the same install settings, and the machine is followed to running by the existing machinery.

- **Identity.** A mismatch keeps the old refusal (`IdentityMismatch`, nothing sent): another machine is at that address. A node that answers neither API is reported as before (`CurrentConfigUnavailable`, then `NodeUnreachable`).
- **Same gates as a join.** The endpoint guard, the SecureBoot Required check (ADR 0123), the install-disk check, and for a control plane the per-machine cluster lock (`acquireDisruptiveLock`, `completesTransition=true`) all apply. The over-cap and CA-rotation holds already run in Reconcile. `ichor.maesh.dev/hold-apply: "true"` (ADR 0103) stops it (`ReprovisionBlocked`); that is the only opt-out.
- **Once per wipe.** Sending clears `status.appliedConfigHash` and `observedInputsHash` and records `configStoredAt`, so the machine is then a stored, never-converged one: the "stored is stored" rule above prevents a second send while the node is still in maintenance mode, and the node is followed (`ConfigStored`, `AwaitingInstall`, `NodeInstalling` ..., `NodeJoined`). No new status field or annotation was needed. Because the reprovisioned state looks like a first provisioning, a node that is wiped again before it converged again is the documented limitation above (not re-applied).
- **Visible.** Warning Event `NodeReinstalled` and a Ready/Provisioned message beginning "Ichor re-installed this node", naming the hash that was sent.
- **Maintenance pin.** A wiped node has a new maintenance certificate, so the pin recorded at the first provisioning (ADR 0108) would refuse every legitimate case. For a converged machine the first handshake is trusted again and its fingerprint recorded, unless the `trust-maintenance-fingerprint` annotation holds one (then a different certificate gives `MaintenanceIdentityChanged`). This is the trust the first provisioning already had (a node's self-reported identity over an unauthenticated API); it relies on the separate provisioning network the design assumes. The residual is that an attacker on that network who answers on a down node's address with its UUID, serial and MACs now gets the configuration, CA keys included; the first provisioning had the same exposure at discovery.
- **Control planes and etcd.** A wiped control plane is still listed as an etcd member, and a node joining under that name does not replace it. Before sending, under the cluster lock, the member (by `spec.hostname`, else `status.nodeName`) is looked up through another approved control plane (`findEtcdSurvivor`, as the removal does) and, if listed, removed with `EtcdRemoveMemberByID` (Warning Event `StaleEtcdMemberRemoved`). This is automatic because the node has proven it cannot be that member any more (it answers in maintenance mode with the approved identity, so its etcd data is gone), which is stronger evidence than the unreachable member ADR 0106 removes; etcd needs a quorum to accept the removal, and the cluster is no less fault tolerant without a member that cannot answer. If the removal fails the machine stays at `StaleEtcdMember` with a message naming `ichor node remove NAME` (which removes the member and the machine, then the node is approved again); if the member name is unknown, etcd cannot be read, or there is no other control plane, it stays at `ReprovisionBlocked` and nothing is sent. With no other control plane the cluster's etcd data is gone: the message points to `ichor restore --recover-cluster`.
- **Rejected.** An opt-in annotation or `spec.reprovision: Manual|Automatic` with a `ichor machine reprovision` command (the first design, with a one-shot request time in `status.lastReprovision`): the user decided that reinstalling a node that is provably the approved machine and has no data left to lose needs no permission. A typed field can still be added later (append-only) if a per-machine "never" is wanted beyond `hold-apply`.
- **Not changed.** Node reset and Kubernetes Node deletion (`ichor node reset` leaves the Node object) are a separate change. The Kubernetes Node of a reinstalled machine is not touched here.
- **Tests** (`internal/controller/talosmachine_reprovision_test.go`): match sends once and follows to running; identity mismatch refuses; changed certificate under an accepted fingerprint refuses; neither API answering keeps the old report; hold-apply and a bad endpoint send nothing; control plane with a stale member (removed, then sent), with a member etcd will not drop, with etcd unreadable, with no other control plane, and with the lock held (nothing touched until it is free).

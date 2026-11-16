# 0025. The storage-system side of the node-operation evacuation hook

Status: Accepted, amended · Date: 2026-10-25 · Design section: 4.4 (Storage)

Decided: Layer 4's storage-system side: `EvacuationHook` (write side) plus a preset-independent `EvacuationStatus` function (read side), both verified against longhorn-manager v1.9.1's own controller source

## Context

ADR 0023 built layer 1 of the design's four-layer storage split (Talos-side preparation) and
explicitly left layer 4 — "coordination around node operations: evacuate before drain, remove or
upgrade" — for later, since there was nothing to evacuate yet and the exact Longhorn fields for it
were not established. The design's text for the hook is specific about shape and about one ordering
constraint, but names two things as still open: the interface ("the hook is an interface so other
integrations can use it") and the read side ("wait until no replica lives only there" needed a
field name). This ADR settles both, and is scoped to exactly the storage-system side of the
hook: what `pkg/storage` returns. Reading and writing Longhorn's own resources, and calling into
this package from a node-removal/upgrade/reboot workflow, is `internal/controller`'s job. Nothing here imports
a Kubernetes client or talks to a cluster.

## What `longhorn-manager`'s source says

Per `longhorn-manager`'s own source, tag `v1.9.1` (not its docs or the UI, per the design's own
standard for the write side):

- **The completion signal is not a status field.** `nodes.longhorn.io/v1beta2`'s `DiskStatus` does
  carry a `ScheduledReplica map[string]int64` field that looks like the obvious candidate, but
  per `scheduler/replica_scheduler.go` it is the replica scheduler's free-space
  bookkeeping (how much capacity is already committed on a disk) — read for scheduling decisions,
  not for residency, and with no guarantee of going to zero when an eviction finishes.
- **The real mechanism is in the volume controller, and it is a deletion, not a counter.**
  `controller/volume_controller.go`'s `cleanupEvictionRequestedReplicas` walks a volume's
  `Replica` objects; once a non-evicting healthy replica exists elsewhere for that volume, it
  calls `c.deleteReplica` on the one with `Spec.EvictionRequested == true`. So "no replica lives
  only [on this node/disk]" is exactly "no `replicas.longhorn.io` object remains whose
  `spec.nodeID` (whole-node eviction) or `spec.diskID` (one-disk eviction) still names it" — an
  absence of objects to list and count, not a field to read.
- **The order constraint is a hard rejection, not a preference, in the admission
  webhook.** `webhook/resources/node/validator.go` rejects any write that leaves
  `evictionRequested: true` while `allowScheduling: true`, for both `NodeSpec` and each entry
  under `DiskSpec`. `BeginEvacuation`'s doc comment cites this directly, not the general docs
  ADR 0023 already cited for the write side's field names (which this ADR does not revisit).

## Decision

- **`pkg/storage.EvacuationHook`** (`pkg/storage/evacuation.go`) has exactly two methods,
  `BeginEvacuation()` and `EndEvacuation()`, both returning `(allowScheduling, evictionRequested
  bool)`. This is deliberately thin: the only thing that varies by storage system is which two
  values to write and in what order, which is exactly what these two methods name. `longhornV1`
  (already the sole `Preset`, from ADR 0023) implements it too, so it ships with a real
  implementation from the start — the alternative this avoids is the empty-interface mistake
  `pkg/upgrade`'s own doc comment already warns about for this exact codebase. `NewEvacuationHook`
  mirrors `NewPreset`'s shape for discoverability, rather than requiring a caller that already has
  a `Preset` to know it can type-assert it to `EvacuationHook`.
- **`EvacuationStatus` is a function, not an interface method.** Given `replicasRemaining` (a
  count the caller already obtained by listing `replicas.longhorn.io` and filtering by
  `spec.nodeID`/`spec.diskID`, per the previous section) and `elapsed`/`timeout`
  (`time.Duration`s, the latter from `TalosStorage.spec.evacuationTimeout` once the caller resolves
  its default), it returns one of `EvacuationWaiting`, `EvacuationDone`, `EvacuationPaused`. This
  decision — zero remaining means done; otherwise, timeout reached means paused, not done — does
  not vary by storage system, so it is not part of `EvacuationHook`; putting it there would just
  mean every future preset re-implementing the same three-way comparison. `EvacuationPaused` is
  deliberately a distinct value from `EvacuationDone`: the design requires pausing the rollout on
  timeout rather than proceeding to drain, and a caller cannot get that wrong by collapsing the two
  into one "stop waiting" signal.
- **No cluster call, no Longhorn-typed dependency.** Like `Preset`, `EvacuationHook` only returns
  plain data; `internal/controller` owns reading and writing `replicas.longhorn.io`/
  `nodes.longhorn.io` with whatever client it already has (dynamic or typed) — adding
  `longhorn-manager`'s own API types as a dependency here, just to name two bool fields this ADR
  already names in prose, was considered and rejected as exactly the dependency-for-a-few-lines
  that is not worth adding.

## Consequences

- `internal/controller`'s node-operation workflow (removal, upgrade, reboot-requiring config
  changes) can call `NewEvacuationHook`, write
  `BeginEvacuation`'s two values in order, list `replicas.longhorn.io` itself, call
  `EvacuationStatus` each time it checks, and write `EndEvacuation`'s values once it is done —
  with every Longhorn-specific fact it needs cited here.
- A second preset that cannot evacuate live (e.g. `filesystem`, from the design's roadmap) is a new
  `EvacuationHook` implementation; nothing about the interface assumes Longhorn's specific
  mechanism, only that some storage system has the general shape of "disable scheduling, request
  eviction, eventually undo both."
- This package drains no node, lists no real `Replica` object, and is not wired into
  `internal/controller`; that wiring is the controller's job.

## Alternatives considered

Folding `BeginEvacuation`/`EndEvacuation`/`EvacuationStatus` into the existing `Preset` interface
instead of a second one. Rejected: `Preset` is layer 1's extension point (Talos-side config,
verified once per preset, no notion of time or in-progress state); `EvacuationHook` is layer 4's,
called repeatedly during a live operation with a timeout. The two interfaces happen to share one
implementation today, not one identity.

Making `EvacuationTarget` (whole node vs. one disk) a parameter of the hook's methods. Rejected:
the returned values never differ by target for any plausible implementation —
what differs is which spec path the caller writes the two bools to (`spec.allowScheduling` vs.
`spec.disks[name].allowScheduling`), which the caller already knows from its own choice of
whole-node or per-disk eviction, not something `EvacuationHook` needs to be told.

## Amendment (2026-10-27): End writes in the reverse order, and absent Longhorn is nothing to evacuate

Longhorn 1.12.1's node webhook refuses any write that leaves `allowScheduling` and `evictionRequested` both true ("need to disable scheduling on node X for node eviction, or cancel eviction to enable scheduling on this node"). Begin's order (scheduling off, then eviction on) is right, but End in the same order is always refused while the eviction is still requested, which would leave every rebooted Longhorn node paused. **End now writes `evictionRequested=false` first, then `allowScheduling=true`**, as two updates (a field already holding its value is not rewritten). Two updates rather than one: each intermediate state is valid on its own, so the result does not depend on how any Longhorn version validates a combined write.

A `TalosStorage` must exist before Longhorn is installed (the disk comes first), and while longhorn-manager crash-loops (e.g. the schematic still lacks iscsi-tools) no Longhorn `Node` objects exist. If the Longhorn CRD is absent (`meta.IsNoMatchError`) or the node's `nodes.longhorn.io` object is NotFound, Begin, End and the replica count treat it as nothing to evacuate and proceed (zero replicas), logging once per node at info level. Any other error still fails the step.

Every node-operation path that begins an evacuation also ends it when it pauses: the node actions (ADR 0085) now do so on a failed await and on timeout as well as on a failed begin step, matching the config rollout and the Talos upgrade.

## Amendment (2026-11-16): the reason for not reading `ScheduledReplica`, and what the webhook rule constrains

The decision above stands: the hook does not read `DiskStatus.ScheduledReplica`, and it counts `replicas.longhorn.io` objects by `spec.nodeID`. The reason given in Context, that `ScheduledReplica` is "the replica scheduler's free-space bookkeeping", is wrong, and the correct reason is stronger.

- **`ScheduledReplica` mirrors the replica objects on each disk.** The node controller lists a disk's replica objects (`ListReplicasByDiskUUID`) and rebuilds the map from them on each node sync: `diskStatus.ScheduledReplica = scheduledReplica` in `controller/node_controller.go`, line 902 in longhorn-manager v1.9.1 and line 1014 in v1.13.0.
- **It is reset to empty when the disk is not Ready.** The same function sets `diskStatus.ScheduledReplica = map[string]int64{}` in that branch: line 847 in v1.9.1 and line 947 in v1.13.0. An empty map therefore means either the eviction finished or the disk went away, and the map does not say which. Longhorn's graceful-removal guide for 1.12.1 (https://longhorn.io/docs/1.12.1/nodes-and-volumes/nodes/graceful-node-removal/) and the 1.13.0 page of the same name state the rule as "Eviction is complete only when every disk reports `scheduledReplicas: 0` and `scheduledBackingImages: 0`". That rule can read zero for a disk that is not Ready. Counting replica objects does not depend on disk readiness, which is why the hook keeps counting them.

The Longhorn node webhook's rule constrains the resulting object, not the number of writes that produced it. In `Update` of `webhook/resources/node/validator.go`, the node-level check is `if newNode.Spec.EvictionRequested && newNode.Spec.AllowScheduling` (line 105 in v1.9.1, 106 in v1.11.0, 112 in v1.12.1, 113 in v1.13.0), and the disk-level check applies the same condition to each `DiskSpec` entry. Any object that leaves `evictionRequested` and `allowScheduling` both true is refused, whichever writes produced it. Longhorn's 1.12.1 and 1.13.0 graceful-removal guides also show disabling scheduling and requesting eviction in one `kubectl patch --type=merge` command. Whether a single merge patch is judged on its merged object is an admission-semantics question this amendment does not settle.

Ichor still writes the two values as two separate updates, in the order the Decision and the code give, and should keep doing so. No intermediate state then has both `allowScheduling` and `evictionRequested` true, on any of the four validator versions cited above, whatever the webhook does with a combined write. The reason to split is therefore not that combining is refused. Splitting cannot produce a refused state under any of these versions. The 2026-10-27 amendment's order rule is the same point: writing `allowScheduling=true` while `evictionRequested` is still true is an intermediate state that is refused.

## Amendment (2026-11-16): per-disk eviction is not built

The Context and the Decision name a second target, `spec.diskID` ("whole-node eviction (`spec.nodeID`) or `spec.diskID` (one-disk eviction)"). Only the whole-node form exists. `ReplicasRemaining` in `internal/controller/evacuation.go` counts only replicas whose `spec.nodeID` names the node, and its doc comment says "Whole-node eviction only, never per disk (spec.diskID)". No code in `internal/` or `pkg/` writes a Longhorn disk entry (`spec.disks[<name>]` on a `nodes.longhorn.io` object). `EvacuationHook` returns two booleans for a node and has no disk target, so the per-disk path is absent from `pkg/storage` as well.

Per-disk eviction will be built when a preset needs it. It is not promised here or in the design.

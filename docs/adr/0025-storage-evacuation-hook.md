# 0025. The storage-system side of the node-operation evacuation hook

Status: Accepted · Date: 2026-10-25 · Design section: 4.4 (Storage)

## Context

ADR 0023 built layer 1 of the design's four-layer storage split (Talos-side preparation) and
explicitly left layer 4 — "coordination around node operations: evacuate before drain, remove or
upgrade" — for later, since there was nothing to evacuate yet and the exact Longhorn fields for it
were unverified. The design's text for the hook is specific about shape and about one ordering
constraint, but names two things as still open: the interface ("the hook is an interface so other
integrations can use it") and the read side ("wait until no replica lives only there" needed a
field name nobody had looked up yet). This ADR settles both, and is scoped to exactly the
storage-system side of the hook: what `pkg/storage` returns. Reading and writing Longhorn's own
resources, and calling into this package from a node-removal/upgrade/reboot workflow, is
`internal/controller`'s job — a different, already-in-progress unit of work. Nothing here imports
a Kubernetes client or talks to a cluster.

## What was verified, and against what

Checked directly against `longhorn-manager`'s own source, tag `v1.9.1` (not its docs, and not the
UI, per the design's own standard for the write side):

- **The completion signal is not a status field.** `nodes.longhorn.io/v1beta2`'s `DiskStatus` does
  carry a `ScheduledReplica map[string]int64` field that looks like the obvious candidate, but
  reading `scheduler/replica_scheduler.go` shows it is the replica scheduler's free-space
  bookkeeping (how much capacity is already committed on a disk) — read for scheduling decisions,
  not for residency, and with no guarantee of going to zero when an eviction finishes.
- **The real mechanism is in the volume controller, and it is a deletion, not a counter.**
  `controller/volume_controller.go`'s `cleanupEvictionRequestedReplicas` walks a volume's
  `Replica` objects; once a non-evicting healthy replica exists elsewhere for that volume, it
  calls `c.deleteReplica` on the one with `Spec.EvictionRequested == true`. So "no replica lives
  only [on this node/disk]" is exactly "no `replicas.longhorn.io` object remains whose
  `spec.nodeID` (whole-node eviction) or `spec.diskID` (one-disk eviction) still names it" — an
  absence of objects to list and count, not a field to read.
- **The order constraint is a hard rejection, not a preference, confirmed in the admission
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
  ponytail mode rules out.

## Consequences

- `internal/controller`'s node-operation workflow (removal, upgrade, reboot-requiring config
  changes — a separate, concurrent unit of work) can call `NewEvacuationHook`, write
  `BeginEvacuation`'s two values in order, list `replicas.longhorn.io` itself, call
  `EvacuationStatus` each time it checks, and write `EndEvacuation`'s values once it is done —
  with every Longhorn-specific fact it needs already verified and cited here rather than
  guessed.
- A second preset that cannot evacuate live (e.g. `filesystem`, from the design's roadmap) is a new
  `EvacuationHook` implementation; nothing about the interface assumes Longhorn's specific
  mechanism, only that some storage system has the general shape of "disable scheduling, request
  eviction, eventually undo both."
- Nothing in this change drains a node, lists a real `Replica` object, or is wired into
  `internal/controller`. That wiring is explicitly the other agent's work; this package is ready
  to be called once it lands.

## Alternatives considered

Folding `BeginEvacuation`/`EndEvacuation`/`EvacuationStatus` into the existing `Preset` interface
instead of a second one. Rejected: `Preset` is layer 1's extension point (Talos-side config,
verified once per preset, no notion of time or in-progress state); `EvacuationHook` is layer 4's,
called repeatedly during a live operation with a timeout. The two interfaces happen to share one
implementation today, not one identity.

Making `EvacuationTarget` (whole node vs. one disk) a parameter of the hook's methods. Rejected
once it became clear the returned values never differ by target for any plausible implementation —
what differs is which spec path the caller writes the two bools to (`spec.allowScheduling` vs.
`spec.disks[name].allowScheduling`), which the caller already knows from its own choice of
whole-node or per-disk eviction, not something `EvacuationHook` needs to be told.

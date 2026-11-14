# 0042. Reset is opt-in per removal, named partitions only, and idempotent by recording after the ack

Status: Accepted · Date: 2026-10-26 · Design section: Nodes (removal), Roadmap phase 3
Decided: Add an opt-in `ichor.maesh.dev/reset-on-delete` annotation that, after a removal's etcd member is gone, wipes only STATE and EPHEMERAL by label and reboots the node to maintenance mode -- holding the finalizer if Reset fails, and recording the request only after Talos has acknowledged it, so a resumed reconcile never asks twice

## Context

ADR 0032 completed `TalosMachine` removal as far as etcd: drain, leave
etcd, clear the finalizer. What it left unresolved, and said so out loud
with a `Warning` Event (`NodeStillHasClusterCredentials`): the physical
node was never reset, so it still holds the secrets bundle's PKI even
after Ichor has forgotten about it entirely. That gap is this ADR's
reason to exist.

Architecture.md's "Nodes" section already investigated Talos's `Reset`
call and recorded two facts, not guesses:

> The reset must name the partitions to wipe. With its default mode and
> no partitions named, Reset wipes the entire system disk, boot and EFI
> included, which means a reinstall rather than a return to maintenance
> mode. Passing STATE and EPHEMERAL explicitly is what leaves the
> installed system in place and brings the node back up with no
> configuration.

> Reset is fire-and-forget. It returns an actor id immediately and the
> sequence runs asynchronously, so status has to record that a reset was
> requested before the controller can crash -- otherwise a resumed
> reconcile fires a second concurrent reset at the same node.

Both are design-level constraints this ADR has to satisfy, not implement
around.

## Decision

### Reset is irreversible, so it is opt-in per removal, not automatic

A new annotation, `ichor.maesh.dev/reset-on-delete` (value `"true"`),
gates whether `reconcileRemoval` ever calls Reset at all -- the same
shape `deleteguard.AllowDeleteAnnotation` already uses: a deliberate,
per-object decision rather than a default, because an operator may
legitimately want the disk left alone to investigate a failure, or intact
to reuse for something else. Its absence changes nothing from ADR 0032:
etcd member removed, finalizer cleared, `NodeStillHasClusterCredentials`
fired exactly as before.

### `TalosClient` grows one more method, adapting machinery v1.14.3's `ResetGeneric`

`Reset(ctx context.Context) error` adapts `*client.Client.ResetGeneric`
with a fixed, never-caller-configurable request:

```go
&machineapi.ResetRequest{
	Graceful: false,
	Reboot:   true,
	SystemPartitionsToWipe: []*machineapi.ResetPartitionSpec{
		{Label: constants.StatePartitionLabel, Wipe: true},
		{Label: constants.EphemeralPartitionLabel, Wipe: true},
	},
}
```

- `SystemPartitionsToWipe` is never empty and never caller-supplied:
  `ResetPartitionSpec`'s own doc comment says plainly that an empty list
  means "all the partitions are erased," and the machined server
  (`internal/app/machined/internal/server/v1alpha1/v1alpha1_server.go`)
  rejects `Mode: USER_DISKS` together with a non-empty
  `SystemPartitionsToWipe` -- confirming `STATE`+`EPHEMERAL` by label,
  `Mode` at its `ALL` zero value, is exactly `talosctl reset
  --system-labels-to-wipe STATE,EPHEMERAL`'s own usage, not a novel
  combination. `constants.StatePartitionLabel`/`EphemeralPartitionLabel`
  are used rather than the literal strings, so a future machinery bump
  renaming either constant breaks this at compile time instead of silently
  wiping the wrong (or no) partition.
- `Graceful: false`: by the time this is ever called, `removeEtcdMember`
  has already drained the node and left etcd itself (ADR 0032). Talos's
  own graceful pre-reset routine exists to do exactly that when nothing
  else has, which is never true here -- architecture.md's own "belt
  rather than trousers" framing for Ichor's drain/leave applies the other
  way round to this flag: Ichor's own sequence is the trousers here,
  Talos's graceful-reset is the belt this call has no use for.
- `Reboot: true`: required for the "machine reappears as pending and can
  be reused" story architecture.md's removal section describes -- a
  halted node needs a human to power it back on before discovery can see
  it again, which defeats the point of resetting it automatically.

No second interface: this is the same `TalosClient` ADR 0032 widened,
reusing the same `r.TalosClient` factory function a third time (self,
survivor, and now self again for Reset).

### Order: etcd leave, then Reset, then the finalizer -- Reset failure holds it

`resetNode` runs after `removeEtcdMember` succeeds and before
`completeRemoval`. A failed Reset records `reasonResetFailed` on the
`Removal` condition and returns without clearing the finalizer -- the
same "stay, don't disappear" shape every other refusal in this file
already has, and the one that matters most here: a `TalosMachine` that
opted into Reset but whose Reset request never actually reached the node
must not be allowed to vanish from Ichor while the physical hardware is
untouched and still credentialed.

### Idempotency: recorded after the RPC's own ack, not before

Reset is fire-and-forget -- the RPC returns once Talos has accepted the
request and started the wipe-and-reboot sequence in a background
goroutine (`v1alpha1_server.go`'s `Reset` handler:
`go func() { s.Controller.Run(resetCtx, runtime.SequenceReset, &opts) }()`
runs after the response is already built), not once the sequence
finishes. `resetNode` therefore checks `conditionRemoval`'s own status
*before* calling `Reset`, and writes `reasonResetRequested` on it
*immediately after* `Reset`'s RPC returns without error -- the earliest
point at which "a reset was requested" is actually true, and
architecture.md's own fix for "a resumed reconcile fires a second
concurrent reset": a crash after this write means the next reconcile
sees the condition already `True` and returns without calling `Reset`
again at all.

Two facts from Talos's own source bear on how dangerous a
near-simultaneous double-call would be if this check were ever bypassed: `Controller.Run` takes a `priorityLock` before
running any sequence, so a second `SequenceReset` arriving while the
first is still in flight blocks (up to a minute) rather than racing the
first on the same disk; and once a first Reset actually completes with
`Reboot: true`, the node reboots into fresh maintenance mode, where the
admin certificate any second attempt would authenticate with is no
longer trusted at all -- the second call fails closed rather than
repeating the wipe. Neither of these is why this ADR's own check exists
(the architecture doc's instruction is unconditional, and this project
does not rely on a dependency's internal locking as its own safety net).

### The Warning Event is skipped once Talos has accepted the Reset

`completeRemoval` takes a `reset bool`. When true, the success message says
that Talos accepted the Reset, and `NodeStillHasClusterCredentials` is not
emitted, because the Reset Talos accepted is the one that wipes the `STATE`
partition those credentials lived on. The Event fires exactly when it did
before this ADR, in exactly the case this ADR did not change.

"The command succeeded" and "the disk was wiped" are different claims, and the
success message makes only the first. The Reset RPC returns once the server has
accepted the request and started the sequence in a goroutine (Talos v1.14.5,
`internal/app/machined/internal/server/v1alpha1/v1alpha1_server.go`, around line
633). A wipe that fails afterwards appears only in the node's log, as
`reset failed:`. A request the server refuses up front, such as an invalid
partition list, fails the RPC itself. `talosctl reset --reboot`'s post-check
(`cmd/talosctl/cmd/talos/reset.go`, lines 131-155) passes when the node answers
in maintenance mode or its boot ID has changed, so a reboot alone satisfies it,
which is no evidence of a wipe either. The check that is: ask the node for its
volumes and confirm `STATE` and `EPHEMERAL` are gone, with only `META` left.
`META` matters for a second claim: `Installed()` is true only when `META` is
Ready (`internal/app/machined/pkg/runtime/v1alpha1/v1alpha1_state.go`, lines
226-263), and the install sequence runs when it is false, so "the reset
succeeded" and "the node will install again" are different claims too.

## Consequences

- Removal has a real way to close the gap ADR 0032's own Warning Event
  named, but only when an operator explicitly asks for it per machine --
  the default for every existing and future removal stays unchanged.
- `resetNode` reuses `conditionRemoval` rather than introducing a fourth
  condition type: `IsStatusConditionTrue` on it, checked before any
  destructive call in this file, now means "something terminal-shaped
  already happened here, do not redo it" uniformly across
  removeEtcdMember's sibling check, resetNode's own check, and
  completeRemoval's final write.
- A removal that opts into Reset and succeeds takes one more Talos RPC
  than one that does not, on the same node it was already dialing for
  `removeEtcdMember`'s self-leave attempt.
- Nothing here touches `forceRemove` (reset on a node that cannot be
  dialed at all): opting into Reset when the node is unreachable simply
  fails at the dial, same as any other Talos call in this file, and holds
  the finalizer exactly like any other failed step.

## Amendment 2026-11-05: an optional full wipe

The default stays as decided here: STATE and EPHEMERAL by label, nothing else. A node that has to be installed afresh (a SecureBoot move, ADR 0123) cannot use it, because the installed boot partition survives and the node boots its installed system again. `TalosOperation.spec.node.wipeMode` (`system-disk` or `all`, only with the `reset` action; `ichor node reset --wipe-mode`) therefore asks for the other shape of `ResetRequest`: `Mode` SYSTEM_DISK or ALL and no `systemPartitionsToWipe`, which Talos reads as the whole system disk (and, for ALL, every user disk). The mode travels to the removal flow in the machine annotation `ichor.maesh.dev/reset-wipe-mode`, next to `reset-on-delete`; `reclaim.ResetRequestWipe` is the one place that builds the request, and `MachineReclaim` is unchanged (it always wipes STATE and EPHEMERAL). Needs the same override and typed name as any reset. Talos reference: `talosctl reset --wipe-mode all|system-disk|user-disks` (docs.siderolabs.com/talos/v1.14).

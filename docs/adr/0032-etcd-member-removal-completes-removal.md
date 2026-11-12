# 0032. Etcd member removal completes the removal sequence; Reset stays a separate unit

Status: Accepted · Date: 2026-10-26 · Design section: Nodes (removal)
Decided: Complete a TalosMachine deletion by removing its etcd member -- self-leave first, a survivor removing it by ID as fallback -- clearing the finalizer only after that succeeds, and warning that the node keeps the cluster's credentials until it is reset

## Context

ADR 0010 covers two of the removal sequence's three refusals (quorum,
last-control-plane); `pkg/deleteguard.CheckControlPlaneCount` added the
third (declared count). All three ran inside `reconcileRemoval`, but the
step they gate — "remove the etcd member if it's a control plane" — was
never implemented: the package comment on `internal/controller/removal.go`
said plainly that etcd member removal and the Talos reset itself were
"deliberately not implemented here," and a removal that passed every check
and finished draining still held its finalizer forever. A `TalosMachine`
whose deletion is admission-allowed and controller-approved never actually
disappears, and `deleteguard.CountApprovedControlPlanes`'s List therefore
never stops counting it either, since nothing ever clears its finalizer.
That is not a "mostly working" removal path — it is a machine that drains
and then gets permanently stuck.

Finishing it needs a real Talos API call. `TalosClient`'s doc comment calls
itself "the one interface rule 0 allows," so the fix widens that one
interface rather than adding a second — a second Talos-facing interface
would double the number of test doubles for no reason the design
asks for.

## Decision

### `TalosClient` grows three methods, adapting machinery v1.14.3's `client.Client`

Machinery v1.14.3's `client.go` and `api/machine/machine.pb.go` define the
calls these adapt:

- `EtcdMemberList(ctx) ([]EtcdMember, error)` — adapts `*client.Client.EtcdMemberList`
  (`QueryLocal: true`), flattening `EtcdMemberListResponse.Messages[].Members[]`
  down to `{ID uint64, Hostname string}`: enough to find a member by the
  Node hostname etcd itself reports, and the numeric ID
  `EtcdRemoveMemberByID` needs.
- `EtcdLeaveCluster(ctx) error` — adapts `*client.Client.EtcdLeaveCluster`,
  called on the *departing* machine's own client. `talosctl`'s own
  `etcd leave` command doc says plainly to "always prefer etcd leave over"
  removing a member by ID.
- `EtcdRemoveMemberByID(ctx, memberID uint64) error` — adapts
  `*client.Client.EtcdRemoveMemberByID`, called on a *surviving* sibling's
  client. `talosctl`'s own `etcd remove-member` command doc: "Use this
  command only if you want to remove a member which is in broken state. If
  there is no access to the node, or the node can't access etcd to call
  etcd leave."

No second interface, no new field on `TalosMachineReconciler` beyond reusing
the existing `TalosClient` factory function a second time — once for the
departing machine, and (only on the fallback path) once more for a
surviving sibling found by listing approved control-plane `TalosMachine`s
in the same cluster via `r.APIReader` (the same direct-read requirement
`CountApprovedControlPlanes` already has, for the same reason: a cached
read could still show a sibling that started its own removal moments ago).

### `removeEtcdMember`'s order: self-leave, then survivor-remove, then hold

1. Dial the departing machine itself. If reachable, list members. If the
   departing machine's hostname is not present, it is already gone (the
   resumption case below) — done, no further Talos call.
2. If present, call `EtcdLeaveCluster` on that same connection. Success —
   done.
3. Otherwise (unreachable, or leave itself failed) fall back to a surviving
   sibling: list members there, and if the hostname is still present, call
   `EtcdRemoveMemberByID` with its numeric ID.
4. If no survivor can be dialed either, return an error. `reconcileRemoval`
   records it as the `Removal` condition's `EtcdMemberRemovalFailed` reason
   and holds the finalizer — the same "stay, don't disappear" shape every
   other refusal in this file already has.

Only after this returns successfully does `completeRemoval` clear
`machineRemovalFinalizer` — in that order, deliberately: clearing the
finalizer first and removing the etcd member second would let the object
vanish while etcd still has a stale member nobody will ever ask about
again, exactly the ordering bug this ADR exists to not ship.

### Idempotent by construction, not by string-matching an error

Architecture.md already names the invariant: "removing an already-removed
etcd member reports not-found, which counts as success." Rather than
parsing Talos's error text for that, `removeEtcdMember` checks the live
member list *before* acting, on every call, and returns immediately if the
departing hostname is already absent. That is what makes resumption after
a crash correct in both directions:

- **Killed before the etcd call ever lands:** the finalizer is still there,
  nothing changed in etcd, the next reconcile re-lists, finds the hostname
  still present, and retries `EtcdLeaveCluster`/`EtcdRemoveMemberByID` from
  scratch.
- **Killed after a successful removal but before the finalizer cleared:**
  the next reconcile re-lists, finds the hostname already gone, and skips
  straight to `completeRemoval` — it never re-asks etcd to remove something
  that is not there, and never trusts any local memory of "did I already
  do this" the way a status flag written *before* the call would have to.

A control plane that has just left etcd can stop answering on its own Talos
API (Talos no longer runs the control-plane stack on a node that is not an
etcd member). So a reconcile resumed after a successful leave may find the
departing machine unreachable; it then takes the survivor path, finds the
hostname already absent from the survivor's member list, and goes straight to
`completeRemoval` without calling `EtcdLeaveCluster` or
`EtcdRemoveMemberByID` again.

This is also why `EtcdLeaveCluster`/`EtcdRemoveMemberByID` need no
fire-and-forget "record intent before acting" status write the way Reset
does (next section): both return their result synchronously in the RPC,
so there is nothing in flight to lose track of across a crash that the
live member list wouldn't already answer on the next read.

### Every Talos call in the removal is bounded

`removeEtcdMember` and `removeEtcdMemberFromSurvivor` run each attempt (the
self-leave in `trySelfLeave` and the survivor fallback) under a context
timeout, `etcdDialTimeout` (15s). Without one, dialling a control plane that
has just left etcd and gone silent hangs with no deadline and wedges the
controller's one `talosmachine` worker, and with it every other
`TalosMachine` queued behind.

### The decoded secrets bundle gets a clock

`secrets.Bundle.Clock` carries `yaml:"-"`, so it is excluded from the
serialization the bundle Secret decoder reads back, and only `populate()`
(run when building a brand-new bundle) refills it. Generating a Talos API
client certificate from a decoded bundle would therefore call a nil
`Clock.Now()`. `internal/secretsbundle.Load` fills `Clock` with
`secrets.NewClock()` when it is nil after decoding, the same fallback
`populate()` uses; because the decode is centralized there (ADR 0033),
`internal/uiapi`'s read-only Talos client gets it too.

### Reset stays out of this interface entirely, and the roadmap still says so

Architecture.md: with no partitions named, `Reset`'s default mode wipes the
*entire system disk* rather than returning the node to maintenance mode —
a reinstall, not the reuse story the design wants — and the call itself is
fire-and-forget, needing its request recorded before a crash so a resumed
reconcile cannot fire a second concurrent reset at the same node. Both are
a materially different kind of work than everything else `TalosClient`
does (every other method here is synchronous and side-effect-idempotent),
and deciding the right partition-naming argument shape is a real design
question this ADR is not going to answer as a side effect of fixing
finalizers. Reset is left for whoever builds it, same as ADR 0010 left the
declared-count refusal for whoever built that.

`TestTalosClientHasNoResetMethod` (`internal/controller/etcd_removal_test.go`)
guards this with a `reflect`-based check over `TalosClient`'s method set,
failing loudly if a future change adds anything Reset-shaped without also
bringing back the fire-and-forget handling that would need.

### The consequence of deferring Reset: a removed machine still holds cluster PKI

Once `completeRemoval` clears the finalizer, Ichor has no record of the
machine at all — but the physical node was never reset, so it still holds
the secrets bundle's PKI (its Talos API client certificate, trusted by the
cluster's CA). It cannot rejoin etcd (it is not a member anymore and has no
path back in without being re-approved from scratch), but it is not safe to
repurpose or discard either: whoever finds it still has valid credentials
for this cluster's Talos API.

The `TalosMachine` this would normally live on disappears in the same
reconcile that discovers it, so a status condition is not durable enough —
the object carrying it is gone moments later. `completeRemoval` instead
emits a `Warning` Kubernetes `Event` (`NodeStillHasClusterCredentials`)
naming the machine before clearing the finalizer: Events are their own
object with their own lifetime, independent of the `TalosMachine`'s, which
is exactly the durability this caveat needs and a condition on a
soon-to-be-deleted object cannot provide. This is not silently accepted —
it is the one thing this ADR requires getting said out loud every time a
control plane leaves, until Reset exists to actually close it.

### `CheckControlPlaneCount`'s refusal message now names both ways out

The message previously said only "approve a replacement control plane
before removing this one." That is correct but incomplete: an operator who
actually wants to shrink the cluster, not replace a member, has no way to
read that out of the message. It now also says "or lower
spec.controlPlane.count first (to 1, 3 or 5) if you mean to shrink the
cluster" — deliberately not naming a specific target number, since the enum
only allows 1, 3 or 5 and the arithmetically "obvious" next value
(`current - 1`) is frequently not one of them.

### The four failure modes

Architecture.md's "Nodes" section names four ways to get this fatally
wrong. Two are Reset's alone (partition naming; fire-and-forget double-reset)
and still cannot be stepped into, because no code in this package calls
Reset at all. The other two are exercised directly: Ichor's refusals happen
before any Talos call, and not-found counts as success for idempotency.

## Consequences

- A `TalosMachine` removal that passes every check now actually finishes:
  the object disappears, not just its finalizer's reason-for-staying.
- Two Talos dials instead of one for a removal that needs the survivor
  fallback (list on self, then list-and-remove on a survivor) — accepted,
  since the alternative is never completing a removal of an unreachable
  node at all.
- `reconcileRemoval` now needs a loaded secrets bundle to get past drain,
  where it previously needed none at all. A cluster without a working
  bundle Secret holds the finalizer at `EtcdMemberRemovalFailed` instead of
  reaching `RemovalApproved` — the same "refuse loudly, don't guess" shape
  `loadBundle`'s callers already use elsewhere.
- Operators now see a `Warning` Event, not nothing, telling them a
  decommissioned node still carries this cluster's credentials until it is
  reset. Reset itself remains unbuilt; this ADR only makes the gap visible
  instead of silent.

## Later changes

Update: removal now evacuates storage before the drain (shared with the Talos
upgrade, see ADR 0043), so the order is evacuation, drain, etcd member, reset.

- Addendum: removal resolves the node name with `resolveNodeName` (an empty `spec.hostname` no longer skips the drain, the evacuation or the etcd member); a control plane whose name cannot be resolved is refused with `NodeNameUnresolved` and retried, and the credentials event says "removed from etcd" only for a control plane.

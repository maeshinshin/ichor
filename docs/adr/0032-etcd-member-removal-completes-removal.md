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
and finished draining still held its finalizer forever. Confirmed for real
against a live 3-node cluster (docs/adr/0007's appendix): a `TalosMachine`
whose deletion is admission-allowed and controller-approved never actually
disappears, and `deleteguard.CountApprovedControlPlanes`'s List therefore
never stops counting it either, since nothing ever clears its finalizer.
That is not a "mostly working" removal path — it is a machine that drains
and then gets permanently stuck, which Ponytail's "no half-working thing
left behind" rule treats as a real problem, not a phase boundary to leave
alone.

Finishing it needs a real Talos API call. `TalosClient`'s doc comment calls
itself "the one interface rule 0 allows," so the fix widens that one
interface rather than adding a second — a second Talos-facing interface
would double unit rule 2's fake-test-double burden for no reason the design
asks for.

## Decision

### `TalosClient` grows three methods, verified against machinery v1.14.3's own client.go and api/machine/machine.pb.go

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

This is also why `EtcdLeaveCluster`/`EtcdRemoveMemberByID` need no
fire-and-forget "record intent before acting" status write the way Reset
does (next section): both return their result synchronously in the RPC,
so there is nothing in flight to lose track of across a crash that the
live member list wouldn't already answer on the next read.

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

### The four failure modes, re-checked now that two of them are no longer hypothetical

Architecture.md's "Nodes" section names four ways to get this fatally
wrong. Two are Reset's alone (partition naming; fire-and-forget double-reset)
and still cannot be stepped into, because no code in this package calls
Reset at all — `TestTalosClientHasNoResetMethod` is the guard that stays
true. The other two are what this ADR's own tests exercise directly:
Ichor's refusals happening before any Talos call (`removal_test.go`'s "the
four failure modes" `Describe` block — unchanged in spirit, updated in
wording now that a passing removal really does call Talos), and the
not-found-counts-as-success idempotency (`etcd_removal_test.go`'s four
`removeEtcdMember` specs: self-leave success, survivor fallback, already-
absent on both paths, and held-finalizer-on-total-failure).

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

## Real-hardware verification

**Two real, previously-latent bugs found and fixed, neither anticipated
going in:**

1. `secrets.Bundle.Clock` carries `yaml:"-"` -- deliberately excluded from
   the exact serialization the bundle Secret decoder reads back. Nothing
   refills it on decode except `populate()`, which only runs while
   building a brand-new bundle. The first ever call from real production
   code to `GenerateTalosAPIClientCertificate` on a bundle loaded this way
   panicked on a nil-interface `Clock.Now()` -- `NewMachineryTalosClient`
   dialing with the fixture's real bundle, recovered by controller-runtime
   but looping forever on retry. No test anywhere had exercised this
   combination before: unit tests fake `TalosClient` entirely, and every
   previous real-hardware pass either had no bundle at all or never got a
   working one. Fixed in `internal/secretsbundle.Load` (ADR 0033 had
   already centralized the decode this ADR's own fix first landed in):
   fill `Clock` with `secrets.NewClock()` if nil after decode, the same
   fallback `populate()` itself uses. Centralizing it there means
   `internal/uiapi`'s own read-only Talos client gets the same fix for
   free, not just this package's.
2. No context timeout anywhere in `removeEtcdMember`/
   `removeEtcdMemberFromSurvivor`. Confirmed for real: the control plane
   that had just left etcd (`ichor-e2e-ha-0`) went unreachable on its own
   Talos API moments later -- its local kube-apiserver stopped answering
   too, consistent with Talos no longer running the control-plane stack on
   a node that is not an etcd member -- and the next reconcile's self-dial
   attempt hung with no deadline, wedging this controller's one
   `talosmachine` worker on every other `TalosMachine` behind it for as
   long as the process stayed up. Fixed with `etcdDialTimeout` (15s),
   wrapping both the self-attempt (`trySelfLeave`) and the
   survivor-fallback attempt.

**The removal itself, with etcd's own before/after member list:**

```text
# before, queried from ichor-e2e-ha-0 itself (10.244.1.40):
NODE          ID                 HOSTNAME         ...
10.244.1.40   221caadec20f5672   ichor-e2e-ha-2
10.244.1.40   5085f640db370298   ichor-e2e-ha-1
10.244.1.40   74b4db31286a41e2   ichor-e2e-ha-0
```
Lowered `TalosCluster.spec.controlPlane.count` from 3 to 1 (the operator
action `CheckControlPlaneCount`'s own refusal message now names), annotated
`ichor-e2e-ha-0`'s `TalosMachine` with `allow-delete=true` and deleted it.
```text
# after, queried from a survivor (10.244.1.208, since ha-0's own endpoint
# was no longer answering by this point -- see bug 2 above):
NODE           ID                 HOSTNAME
10.244.1.208   221caadec20f5672   ichor-e2e-ha-2
10.244.1.208   5085f640db370298   ichor-e2e-ha-1
```
Two members, both voters, `ichor-e2e-ha-0` gone -- a real graceful
`EtcdLeaveCluster`, not the quorum math being taken on faith.

**Idempotency, both sides of the one real crash:**

- *Killed before the Talos call lands*: annotated and deleted
  `ichor-e2e-ha-0`'s `TalosMachine`, then `kill -9`'d the manager process
  immediately after the delete request returned. Confirmed before
  restarting: finalizer still present, no `Removal` condition written at
  all, etcd still the same 3 members as the "before" snapshot above. On
  restart, the manager re-acquired its cluster lock under its own stable
  identity (no `HeldError`) and retried from scratch.
- *Interrupted after the etcd call succeeds, before the finalizer clears*:
  this is bug 2 above, and it is also the "after" case -- `ichor-e2e-ha-0`
  left etcd for real, then went unreachable before `completeRemoval`'s
  status write could land (its own apiserver had just stopped answering).
  Once the controller's own client was pointed at a surviving node instead
  and restarted, the resumed reconcile's self-dial to `ichor-e2e-ha-0`
  timed out (bug 2's fix), fell back to the survivor path, found the
  hostname already absent from that survivor's own member list, and went
  straight to `completeRemoval` -- confirmed by the `NodeStillHasClusterCredentials`
  `Warning` Event firing and the `TalosMachine` object actually
  disappearing, with **no second `EtcdLeaveCluster` or `EtcdRemoveMemberByID`
  call attempted** (nothing to call it on: the member was already gone).
  This is the scenario this ADR calls "the most fragile point," found not
  by engineering a precise kill-timing race but by the environment
  providing the exact same window on its own -- which is arguably stronger
  evidence than a deliberately timed kill would have been, since nothing
  about it was staged.

**Deleting an unreachable node: attempted, not cleanly completed, and the
reason is itself informative.** The delete request itself never reached the inner
apiserver: with only one of two etcd members still reachable, etcd on the
inner cluster lost its leader entirely (`etcdserver: no leader`, confirmed
directly) -- exactly what architecture.md's "Topology" section already
says about two-member clusters ("with two members etcd needs both to be
up, so it is strictly less available than one") and exactly why
`controlPlane.count`'s enum rejects 2, now observed rather than taken on
faith. `removeEtcdMemberFromSurvivor`'s `EtcdRemoveMemberByID` path (as
opposed to the `EtcdMemberList` calls already exercised repeatedly above)
was consequently never exercised against a real, still-a-member,
unreachable node this pass -- reaching that scenario cleanly needs a
fourth real control plane (so removing one unreachable member still has a
reachable quorum left to ask), which this pass's two remaining real nodes
could not provide without engineering a second full HA boot. Left for a
future pass; the machinery-client call itself is the same verified
`client.Client` wrapper `EtcdLeaveCluster` already proved out, and
`etcd_removal_test.go`'s fake-backed test already covers the control flow.

Update: removal now evacuates storage before the drain (shared with the Talos
upgrade, see ADR 0043), so the order is evacuation, drain, etcd member, reset.

- Addendum: removal resolves the node name with `resolveNodeName` (an empty `spec.hostname` no longer skips the drain, the evacuation or the etcd member); a control plane whose name cannot be resolved is refused with `NodeNameUnresolved` and retried, and the credentials event says "removed from etcd" only for a control plane.

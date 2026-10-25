# 0010. The quorum and last-control-plane delete guards are stronger than `allow-delete`

Status: Accepted · Date: 2026-10-25 · Design section: Nodes (removal), Roadmap phase 3

## Context

Phase 1 of the deletion guard (`pkg/deleteguard.CheckDelete`) only checks the
`ichor.maesh.dev/allow-delete` annotation. The design's removal sequence
(architecture.md, "Nodes") says the etcd-member-removal step is "refused if
quorum would break, if the count would drop below `spec.controlPlane.count`,
or if it's the last one," and calls this out as Ichor's own responsibility:
"Talos's member-removal call requires only that a leader exists right now; it
does not ask whether the remaining members can still reach quorum." The
roadmap makes the acceptance condition explicit: "an explicit test has to show
that the annotation alone does **not** let the last control plane go." This
ADR covers two of those three refusals — quorum and last-control-plane; the
third (`spec.controlPlane.count`) belongs to the replace/topology-change flow
that doesn't exist yet, and is left for whoever builds that.

Querying Talos for the live etcd member list is out of scope here: the only
state available is the `TalosMachine` resources in the namespace, so the
check has to be computed from those.

### How to count

`TalosMachineSpec` already carries `clusterRef.name` and `role` (enum
`controlplane`/`worker`), and `approved` (bool). A control plane only ever
becomes an etcd member once approved — the design is explicit that approval
is what lets the controller render and apply configuration and start the
machine joining the cluster; an unapproved `controlplane`-role `TalosMachine`
has never been a member. So "control planes right now" is: `TalosMachine`s in
the same namespace, with the same `clusterRef.name`, `role: controlplane`,
`approved: true` — including the one being deleted, since the webhook runs
before it is actually removed. No new CRD field was needed.

### Quorum math, and n=1

etcd quorum for `n` members is `(n/2)+1` (integer division). Deleting one
member leaves `n-1`. Working the formula for every `n` that can occur (control
planes are 1, 3 or 5, with even counts only transient during a change):

| n (before) | quorum(n) | n-1 (after) | holds? |
|---|---|---|---|
| 1 | 1 | 0 | no |
| 2 | 2 | 1 | no |
| 3 | 2 | 2 | yes |
| 4 | 3 | 3 | yes |
| 5 | 3 | 4 | yes |

Single-member removal only ever fails quorum at `n` = 1 or 2; from `n` = 3
up, `n-1` always meets `(n/2)+1`. That's not a coincidence worth hand-waving
away: for `n >= 3`, `(n/2)+1 <= n-1` always holds, so removing exactly one
member can never be the thing that breaks quorum once there are three or
more.

`n=1` was considered for a carve-out — refusing to delete a single-node
cluster's only control plane could be seen as overreach, since there's
nothing left to protect once it's gone anyway. Weighed against that: the
design's removal-sequence refusals are written as unconditional ("or if it's
the last one"), with no such carve-out, and that sequence exists specifically
to walk a *running* node through drain and etcd-member removal before reset
— work that is pointless, and actively harmful (draining a node whose
workloads have nowhere else to go), for a cluster that's being torn down
entirely rather than shrunk. Tearing down a whole cluster already has its own
path: delete the `TalosCluster`. Per-`TalosMachine` deletion is "remove this
member from a cluster that keeps running," and a cluster cannot keep running
on zero control planes. So `n=1` gets no special case: `CheckControlPlaneQuorum`
treats "would leave zero" as the same refusal at every `n`, which is also
simpler than carrying a second rule that only fires once.

### Why this overrides the annotation

`allow-delete` exists to stop a GitOps prune from cascading into node resets
(architecture.md, "Validation") — it's a "did a human mean this" gate, not a
safety check on the cluster's ability to keep running. A human setting it
cannot make etcd quorum math come out differently, so the roadmap requires
this guard to refuse regardless: `CheckControlPlaneQuorum` is called
independently of, and in addition to, `CheckDelete`. There is no way to
override it through the webhook; the only path to removing the last control
plane is deleting the `TalosCluster`, which answers a different question
("tear down the whole cluster," not "shrink it by one").

## Decision

- `pkg/deleteguard.CheckControlPlaneQuorum(currentControlPlanes int) error`
  is added alongside `CheckDelete`, staying a pure function so it can be
  called from the webhook today and a UI backend later without either owning
  the counting logic (design principle 10). It refuses when removing one
  member would leave zero control planes ("last control plane") or would
  drop below `(currentControlPlanes/2)+1` ("quorum"), and both error
  messages say the before and after counts so `kubectl delete`'s error
  explains itself.
- `TalosMachineCustomValidator.ValidateDelete` (the only place that can
  trigger this for a control plane, since the webhook is DELETE-only) runs
  `CheckDelete` first, then, only for an approved `controlplane`-role
  machine, lists sibling `TalosMachine`s in the same namespace and cluster to
  count current control planes and calls `CheckControlPlaneQuorum`. Neither
  check can be skipped by satisfying only the other.
- `TalosCluster` deletion is unchanged (`CheckDelete` only): deleting the
  whole cluster is not "the last control plane leaving a running cluster,"
  it's the cluster going away, which `allow-delete` alone continues to gate.
- No new CRD field. `ClusterRef`, `Role` and `Approved` already said
  everything the check needed.

## Consequences

- `pkg/deleteguard` stays at 100% test coverage; `CheckControlPlaneQuorum` is
  tested at every boundary `n` in {1, 2, 3, 4, 5}, and a dedicated test
  (`TestAllowDeleteDoesNotOverrideQuorum`) shows the annotation does not
  change its answer, which is the roadmap's named acceptance condition.
  `internal/webhook/v1alpha1`'s envtest suite adds the same cases end to end,
  including the sequential case the task called out by name: three approved
  control planes, delete one (3 -> 2, allowed), then delete another
  (2 -> 1, refused for quorum).
- `TalosMachineCustomValidator` now needs a `client.Client` to list siblings;
  `SetupTalosMachineWebhookWithManager` supplies `mgr.GetClient()`. No RBAC
  change was needed -- the manager's role already has `list`/`watch` on
  `talosmachines`.
- Pending (unapproved) control-plane `TalosMachine`s are not counted and can
  always be deleted through this path once `allow-delete` is set: they were
  never etcd members, so nothing protects them. This is also why a lone
  unapproved control plane does not hit the "last one" refusal.
- The third removal-sequence refusal -- dropping below
  `spec.controlPlane.count` -- is not implemented. It's a topology/replace
  concern (phase 3's "replace add-then-remove" flow), not a bare quorum or
  last-member check, and doesn't exist to retrofit onto yet.
- Real etcd member health is still never queried; this guard is optimistic
  about every counted `TalosMachine` actually being a healthy voter. If that
  turns out to be wrong in practice (a control plane counted as live but
  actually down), the fix is a live etcd member query before removal, which
  is explicitly out of scope here and would need its own design pass.
- The count-then-decide check is not serialized: two concurrent `DELETE`s for
  different control planes in the same cluster can both list the same
  pre-deletion count and both be admitted, even though applied together they
  break quorum (marked `shortcut:` at the list call in
  `talosmachine_webhook.go`). Closing that needs the two admissions to take
  `pkg/clusterlock` around the count-and-decide, which is more than this
  phase's scope; acceptable for now because deletions are expected to be an
  operator acting one at a time, not a controller firing several removals at
  once.

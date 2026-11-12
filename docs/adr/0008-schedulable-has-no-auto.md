# 0008. `controlPlane.schedulable` drops `auto`

Status: Accepted · Date: 2026-10-25 · Design section: 5.3.4 (topology)

Decided: `controlPlane.schedulable` takes a required `true`/`false`; dropped `auto`

## Context

The design gives `TalosCluster.spec.controlPlane.schedulable` three values —
`auto`, `true`, `false` — and says `auto` "resolves at creation: schedulable if
there are no workers, tainted otherwise, and then stays fixed", so that adding
workers later does not silently move a user's pods.

`auto` cannot be resolved:

- **At creation there are never any workers.** `TalosCluster` is created before
  any `TalosMachine`, so a defaulting webhook counting workers would always see
  zero and always resolve `auto` to `true`. For the `standard` preset — three
  control planes plus workers, control planes *not* schedulable — that is the
  wrong answer every time.
- **The topology preset is not on the CR.** `--preset standard` is a CLI flag.
  The intent that would make `auto` resolvable lives with whoever is creating
  the cluster, not in the object.
- **Resolving it later is explicitly ruled out** by the same design paragraph:
  the whole point of fixing it at creation is that approving a worker must not
  retaint the control planes underneath running pods.
- Leaving it unresolved and picking a safe side breaks the other way. Treating
  anything other than `true` as tainted taints the only node of a
  single-node cluster and leaves nothing schedulable, and single-node is a
  supported configuration, not a corner case.

## Decision

`schedulable` takes `true` or `false`, and is required. `auto` is gone.

Whoever creates the cluster states the intent, which is the only place the
intent exists: `ichor init` sets it from the chosen topology preset — `true`
for `single` and `compact-ha`, `false` for `standard` and `large` — and a
hand-written `TalosCluster` has to say which it wants.

`pkg/render` rejects any other value rather than guessing. A renderer that
silently picks a side writes a machine configuration the user did not ask for,
which is the most expensive class of bug this project can produce.

## Consequences

- One fewer value, and no unresolvable state in the API. The webhook that would
  have resolved `auto` does not need to exist.
- A hand-written CR must now be explicit. That is the right trade: the field
  decides whether anything can schedule on a single-node cluster, and a default
  that is wrong half the time is worse than no default.
- The design doc's 5.3.4 is updated to match. Its other topology rules stand
  unchanged: counts of 1, 3 or 5; `schedulable: false` with zero workers
  rejected; even counts only transiently during a change.
- `ichor init`'s preset table gains a column, which it was going to need
  anyway — the presets already differ on exactly this.

# 0067. Whole-cluster shutdown is a TalosOperation kind the controller runs, with a derived mark that refuses other disruptive work

Status: Accepted · Date: 2026-10-26 · Design section: Upgrades
Decided: a `TalosOperation` of kind `shutdown` stops a cluster: workers first (drained), then every control plane and the controller's own node in one forced burst; while any shutdown operation has not `Completed`, `acquireDisruptiveLock` (both of them) refuses everything else; the operation completes by itself once every machine answers on a new boot; no CLI command, no etcd snapshot taken by the shutdown

## Context

Nothing stopped a whole cluster in a planned way. Bringing it back up already works without help: the machine
controller re-checks an unreachable node every 30 seconds (ADR 0060) and does not mistake a booting node for drift. What
is missing is stopping, in an order that does no harm, and keeping Ichor itself from "repairing" the cluster while it is
being stopped or started.

Facts from the Talos v1.14.3 source (`internal/app/machined/...`):

- `Server.Shutdown` (`v1alpha1_server.go`) starts `SequenceShutdown` in a goroutine and returns at once: the RPC
  acknowledges the request, not the stop.
- `Sequencer.Shutdown` (`runtime/v1alpha1/v1alpha1_sequencer.go`) runs the `drain` phase (`CordonAndDrainNode`: wait
  for the node to be cordoned, then evict through the kubelet's own kubeconfig) only when `!in.GetForce()`; then stops
  pods and services. The `ShutdownRequest.force` comment (`api/machine/machine.proto`) is "without first cordoning and
  draining". The drain needs the Kubernetes API.
- Shutdown does not remove the etcd member. `LeaveEtcd` appears only in the `Reset` sequence (graceful, not worker).
  etcd is stopped as a reverse dependency of `cri` (`StopServicesEphemeral`). So the member list keeps every control
  plane, quorum is a majority of the registered members, and powering the machines on restores it with no data lost. The
  danger is only in the order: the API is gone once quorum is.

## Decision 1: where it runs -- a TalosOperation kind, the controller runs it; no CLI exception

A new `TalosOperationSpec.kind` value, `shutdown`. The user (or UI, or `kubectl`) writes the CR; the controller is the
only writer to the nodes. This keeps the design's principle whole; `init`, `adopt` and full-loss recovery are CLI
exceptions only because the cluster, or the controller, does not exist yet, and neither is true here.

The self-managed case decides the shape. A controller that runs inside the cluster it stops dies as soon as its node
goes down, so it cannot be the one to do something after the control planes stop. The answer is not to let the CLI do
the last stage but to make the last stage not need the controller afterwards: all the requests of the final stage are
sent in one burst (Decision 2), and the controller's own node is asked last in it (`NODE_NAME`, from the downward API,
matched with `TalosMachine.spec.hostname`; empty or unmatched when the controller runs elsewhere). The controller needs
to be alive only until it has sent the requests, which are acknowledged in milliseconds and take a node seconds to act
on. Everything the controller needs afterwards is in the CR.

`TalosOperationReconciler` dispatches this kind to its own state machine (`shutdown.go`) instead of
`AdvanceMachineFunc`, which ADR 0026 left as the one place that branches by kind. It is not a rollout (workers before
control planes, a mark to hold, a wait for a human), and forcing it through `Advance` would have needed the opposite
tier order and a different "done". It still takes the cluster lock, honours `spec.paused`, and keeps its state only
in `status`.

A CLI command was not added. No operation kind has one (upgrades are written as CRs by hand or by the installer); a
`shutdown cluster` verb needs an ichor-ui RPC and its proto, which is a separate piece of work. The CR is
```yaml
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosOperation
metadata: {generateName: shutdown-, namespace: <ns>}
spec: {clusterRef: {name: <cluster>}, kind: shutdown}
```

## Decision 2: order

1. **Workers, except the controller's own node, in parallel, `Shutdown(force=false)`.** Talos cordons and drains each
   before stopping it (above), and the API is still up for that. Each is recorded in `status.machines` with its boot
   ID before the request is sent. The operation waits until every one has stopped answering (or answers on another
   boot).
2. **Everything else in one burst, `Shutdown(force=true)`:** every control plane, then the controller's own node last.
   Forced, because the drain needs an API that is about to go and the workers are already down; with control planes
   schedulable (a small cluster) their pods are killed by Talos stopping them, which is the point of a shutdown. No
   leader-last order: a sequence with quorum lost half way needs a controller alive after the API is gone, and a
   shutdown stops etcd cleanly on every member anyway (nothing to transfer). Quorum is lost in this stage and only in
   it. The order in which Talos finishes stopping is not the order the requests were sent in, so the
   controller's own node, asked last, need not be the last to stop.

A machine that does not answer when first looked at is recorded and not asked (it is already down). Upgrade holds are
ignored: they exclude a machine from a rollout, not from stopping the cluster. Only approved machines are stopped.

Recording before sending makes the request at most once per machine per operation: a controller killed between
the two leaves a record and never sends twice, which matters because the controller can come back up (the cluster
has been powered on) while the operation still exists. A request that fails synchronously removes its record, and
those not yet sent, so the next reconcile retries. If the controller dies in the milliseconds between recording and
the request, that machine stays up with its boot ID unchanged; delete the operation and apply a new one.

## Decision 3: the mark, and the lock

The mark is derived, not stored: a `TalosOperation` of kind `shutdown` on the cluster whose phase is not `Completed`.
`shutdownBlocker` lists operations through the API reader and `acquireDisruptiveLock` -- both, the machine
controller's (reboot-needing change, removal, including the removal that completes a 3-4-3 replacement) and the
operation controller's (every upgrade kind) -- calls it before anything else and returns a `shuttingDownError`. The
callers report `ClusterShuttingDown` (a machine's `Ready` or `Removal` condition; an upgrade operation goes `Paused`
and retries every 30 seconds, so it resumes by itself when the mark goes). 

The operation-in-flight check of ADR 0068 also refuses to start another operation while a shutdown is not finished.

Derived because nothing can leave it stale and deleting the operation is the escape hatch for a shutdown that cannot
finish (a machine that never comes back). A shutdown is blocked only by another shutdown that has already recorded a
machine, so two created together do not block each other forever. Drift correction that needs no reboot is not gated
by the lock today and is left alone: it reads the node, and a node that cannot be read is not drifted.

## Decision 4: starting up

Ichor cannot power machines on. The operation sits in phase `ShutDown` (new), holding no lock, and every 30 seconds
(when a controller is running) probes the machines it asked to stop. A machine is back when it answers with a boot
ID other than the recorded one, or, for one recorded as already down, when it answers at all. When all are back the
phase becomes `Completed`, the condition `Shutdown` goes `False`, and the mark is gone: upgrades paused by it resume,
removals proceed, the machine controller works as before. Nobody has to remove the mark; the operator only powers on.
A machine deleted in the meantime is not waited for. A controller in the stopped cluster resumes this on its own boot.

## Decision 5: etcd backup

No snapshot is taken by the shutdown. A shutdown loses no etcd data, `TalosEtcdBackup` has no run-now trigger to
call, and taking a snapshot before the workers are stopped delays a stop the operator asked for. If a fresh snapshot is
wanted, wait for the next scheduled one before applying the operation. The "stale" condition may show while the
cluster is off; the next successful snapshot after power-on clears it, so it was left alone.

## Consequences

- `TalosClient` gained `Shutdown(ctx, force)`; `TalosOperationMachineStatus` gained `bootID`; phase `ShutDown` and kind
  `shutdown` were added to the CRD; the controller Deployment gets `NODE_NAME`.
- Not covered: a node reported unreachable by a network fault is treated as stopped and not asked; a worker whose
  drain is stuck behind a PodDisruptionBudget keeps stage 1 waiting (Talos's own drain timeout applies; delete the
  operation to give up).
- A cluster whose nodes get new addresses on power-on cannot be brought back by a shutdown: every address recorded
  in a shut-down cluster (etcd peer URLs, the cluster endpoint, the machine endpoints, the certificate SANs) is the
  old address, so a multi-member etcd cannot re-form quorum after a power cycle on such a network. Shutdown and
  power-on need node addresses that survive a stop and start.


- `ichor shutdown cluster` and the UI button landed in ADR 0068.

## Amendment (the drain, Talos v1.14.3)

`Shutdown(force=false)` drains: `Sequencer.Shutdown`
(`internal/app/machined/pkg/runtime/v1alpha1/v1alpha1_sequencer.go:363-381`) appends `CordonAndDrainNode` when the
request is not forced and node registration is not skipped, and the shutting-down stage cordons the node
(`controllers/k8s/node_cordoned_spec.go:73-76`).  `Reboot` does not drain the same
way (ADR 0080 fixes that assumption): Ichor drains before its own reboots.

- Addendum (ADR 0086): the wait for a machine to return dials `DialEndpoint()`, so a node that came back at a new address is seen once the address is recorded in status.

- Amendment: a worker counts as stopped only after it stayed unreachable for 90 seconds (a brief network loss does not count; the clock is in memory, so a restarted controller starts it again), and when stage 1 has waited 15 minutes the operation pauses with a reason that names the PodDisruptionBudgets allowing no disruption (cluster-wide, not narrowed to the waiting nodes).

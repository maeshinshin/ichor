# 0135. A remove powers the machine off and deletes its Kubernetes Node

Status: Accepted · Date: 2026-11-06 · Design section: Nodes · Amends: 0085 (the Kubernetes Node stays after a remove) · Builds on: 0032, 0042, 0067

Decided: when a machine is removed without a reset (`ichor node remove`, `RemoveMachine`, or a control plane deleted directly), the removal flow, after the drain and the etcd member removal, asks the node to shut down through the Talos API (graceful `Shutdown`, force, the client the controller already uses for node shutdown, ADR 0067), waits a bounded time for the Kubernetes Node to go NotReady, deletes the Node, and then clears the finalizer. A reset removal is unchanged: it wipes and reboots into maintenance, and its Node is kept and annotated (ADR 0042). If the Node does not go NotReady within the wait, it is not deleted and a Warning Event names it with the manual step. The disk is not wiped.

## Context

ADR 0085 left the Node of a removed machine in place: the node keeps running with the cluster's credentials, and its kubelet would register a deleted Node again. The cost was a NotReady Node and Pending DaemonSet pods until someone ran `kubectl delete node`. The user decided that Remove should delete the Node. That is only safe once the kubelet cannot register it again, which means the machine is off.

## Decision

1. **Where.** `reconcileRemoval`, after `removeEtcdMember`, for a removal that did not ask for a reset. The cluster lock is held as for the rest of the removal.
2. **Shutdown.** `TalosClient.Shutdown(ctx, force=true)`: the drain and the etcd leave already happened, and Talos's own drain on a non-forced shutdown would only wait on pods again. The call is bounded by `etcdDialTimeout`. It is sent once: the Kubernetes Node is annotated `ichor.maesh.dev/removal-shutdown-requested` with the time it was sent, and a resumed reconcile that sees the annotation skips the evacuation, the drain, the etcd step and the shutdown, and goes straight to the wait. The annotation is a marker on the Node object, not state on the TalosMachine `spec`, which the controller never writes.
3. **Wait.** The Node is polled (requeue every 10 s, like a blocked drain) until its Ready condition is not True, for at most 5 minutes after the shutdown was sent. Kubernetes marks a silent kubelet NotReady after the 40 s node-monitor grace period, so the normal wait is under a minute. Then the Node is deleted (an already deleted Node is fine) and the finalizer is cleared.
4. **Unreachable node.** If the node cannot be dialed or the shutdown cannot be sent, the node is down or unreachable. The Node is deleted once it is NotReady (or missing), and the Removal condition and the Event say that the shutdown could not be sent. A node that cannot be reached but whose Node is still Ready is waited for like any other, for the same bound.
5. **Timeout.** If the Node is still Ready after the bound, it is not deleted, the removal finishes (the behaviour of ADR 0085), and the Warning Event `NodeStillHasClusterCredentials` names the stale Node and says `kubectl delete node <name>` once the machine is off.
6. **Event.** `NodeStillHasClusterCredentials` stays: after a remove the disk still holds the cluster's PKI. Its text now says whether the machine was powered off and the Node deleted, and that the disk is not wiped. A reset removal still emits no Event.
7. **Plan text** (UI dialog, CLI plan, `ichor node remove` after it succeeds): the remove drains, removes from etcd (control plane), powers the machine off and deletes the Kubernetes Node; the disk is not wiped. To move the machine to another cluster or add it again from scratch, use Reset instead. To bring it back to the same cluster later, power it on and run `ichor adopt --cluster NAME --add-machine ADDRESS`.
8. **RBAC.** The manager ClusterRole gains `delete` on `nodes` (it had get, list, watch, patch). Kubernetes RBAC cannot restrict a delete to the Nodes of removed machines; the controller deletes only the Node named by the machine it is removing.

## Alternatives rejected

- Deleting the Node without a shutdown: the kubelet registers it again.
- Wiping the node: a destructive step the user did not ask for on Remove; Reset is that.
- Recording removed Nodes on a new object and deleting them later (the ADR 0085 idea): new state; the annotation on the Node is enough.
- Blocking the worker for the wait: the removal would hold a controller worker for up to minutes; a requeue does not.

## Risks

- A Shutdown that Talos accepts but the machine does not complete (hardware) leaves the Node Ready; the Warning covers it.
- A powered-off machine whose kubelet was only briefly NotReady cannot be mistaken for an off one: the Node is deleted only after a shutdown was sent, or after it was unreachable and NotReady.
- Not verified on hardware: the Talos `Shutdown` of a node that just left etcd, and the time the Node takes to go NotReady.

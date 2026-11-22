# Planned shutdown of the whole cluster, and bringing it back

Ichor stops a cluster in an order that keeps the Kubernetes API up as long as it
can, and takes the work of "repairing" a stopped cluster away from the
controller. It cannot power machines on: you do that, and the operation
completes by itself when they answer again.

![Four steps from left to right: workers are drained and stopped, every control plane and the controller's own node are stopped in one forced burst, the operation rests in ShutDown while the nodes are off, and it completes when every node answers on a new boot.](../images/shutdown-cluster-order.light.drawio.svg)

*A cluster shutdown: workers first, then everything else in one burst.*

## Which command

| Goal | Command | Operation |
|---|---|---|
| Stop every node of a cluster | `ichor shutdown cluster NAME` | `kind: shutdown` |
| Stop one node | `ichor shutdown node NAME` | `kind: node`, action `shutdown` |

Both show the plan and ask for confirmation unless `--yes`. `--follow` follows
the operation; for a cluster it ends at `ShutDown`, when every node is down. The
commands only write the `TalosOperation`; the controller does the work.

```sh
ichor shutdown cluster lab --follow
```

Without the CLI, create the operation yourself:

```yaml
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosOperation
metadata: {generateName: shutdown-, namespace: ichor-system}
spec: {clusterRef: {name: lab}, kind: shutdown}
```

## Order of steps

1. **Workers.** Every approved worker except the node the controller runs on is
   asked to stop with Talos's cordon and drain, in parallel, while the API is
   still up. A worker counts as stopped after it has been unreachable for 90
   seconds.
2. **One burst.** Every control plane, then the controller's own node last, is
   asked to stop without a drain: the API is about to go. etcd loses quorum
   here and only here. The controller needs to be alive only until it has sent
   the requests.
3. **`ShutDown`.** The phase the operation rests in. It holds no cluster lock.
   Every 30 seconds, when a controller is running, it probes the machines it
   stopped.

A machine that does not answer when first looked at is recorded and not asked. A
request is sent at most once per machine per operation.

## While it has not completed

| Effect | Detail |
|---|---|
| Other disruptive work is refused | Upgrades pause with `ClusterShuttingDown` and resume by themselves when the operation completes. Machine removal waits. |
| A new operation is refused | The plan names the unfinished one. |
| An unfinished stage 1 | After 15 minutes the operation pauses and names the PodDisruptionBudgets that allow no disruption. |
| A stuck shutdown | Delete the operation: that removes the refusal. |

## What survives

| Item | State |
|---|---|
| etcd members and data | Kept. A shutdown does not remove a member; powering on restores quorum. |
| Approved machines, `TalosMachine` objects | Kept. Upgrade holds are ignored by a shutdown. |
| An etcd snapshot | Not taken. If you want a fresh one, wait for the next scheduled `TalosEtcdBackup` before you start. |
| Node addresses | Must survive a stop and start. The recorded etcd peers, endpoint and certificate SANs are the old addresses. |

## Bring it back

1. Power on the machines. The machine
   controller re-checks an unreachable node every 30 seconds and does not read a
   booting node as drift.
2. Wait. A machine is back when it answers with a boot ID other than the one
   recorded, or, if it was already down when the operation started, when it
   answers at all. A node that came back at a new address is seen once the
   address is in its status.
3. When every machine is back, the phase becomes `Completed`, the `Shutdown`
   condition goes `False` and the refusal is gone. Nothing needs removing.

If the controller ran in the stopped cluster, it resumes the operation on its own
boot. A machine deleted in the meantime is not waited for.

## One node

`ichor shutdown node NAME` asks Talos to stop one node, which drains unless the
API is gone, and then rests in `ShutDown`. It completes when the node is powered
on.

| Refused | Why |
|---|---|
| A control plane of a two-control-plane cluster | One etcd member would be left, below quorum. |
| The last worker | Needs `--override`. |

## If the shutdown pauses

| Reason | Meaning and way out |
|---|---|
| `OwnNodeUnread` | The controller knows its own node name (`NODE_NAME`) but cannot tell which machine runs it, because a machine's node name could not be read. The operation retries every 30 seconds and resumes when the machine answers or its Node appears. For a control plane the way out is to delete the operation. |
| `ClusterLockHeld` | Another operation holds the cluster lock. It retries. |

A node operation is parked while the control-plane count is even; see
[Replace a control plane](replace-a-control-plane.md).

See the [reference](../reference/cli/ichor_shutdown_cluster.md) for every flag, and
[Operations](../concepts/operations.md) for the phases.

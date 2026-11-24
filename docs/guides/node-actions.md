# Node actions: reboot, restart a service, etcd defrag, forfeit leadership

A node action is a `TalosOperation` of `kind: node`: one action on one node. The
CLI and the UI only write it; the controller does the work, one operation at a
time per cluster. A plan is refused while another operation of the cluster is
unfinished.

![The steps of each node action. A reboot checks guards, moves storage replicas off, cordons and drains, reboots, waits for a new boot, Ready and the etcd member, then uncordons. A service restart checks the allowlist, restarts, and waits for Ready and a running service. A defrag checks the node is a control plane and not the leader, checks every etcd member is present, defragments and checks no alarm is left. A leadership handover checks the node leads, checks every member is present, hands over and waits for a different leader.](../images/node-actions-steps.light.drawio.svg)

*Amber boxes are the checks that can refuse.*

## The commands

| Action | Command | Runs on |
|---|---|---|
| Reboot | `ichor reboot node NAME` | Any approved node |
| Restart a service | `ichor restart service NODE SERVICE` | Any approved node |
| Defragment etcd | `ichor defrag etcd --node CP [--allow-leader]` | A control plane |
| Hand etcd leadership over | `ichor forfeit-leadership etcd --node CP` | The control plane that leads |

Each command shows the plan and asks to confirm; `--yes` skips the question and
`--follow` watches the operation until it finishes. The same actions are in the
Actions menu of the node page ([The web UI](the-web-ui.md#the-node-page)).
Shutdown and reset are also node actions, with their own start verbs; this page
does not cover them.

## What each does

| Action | To the node | Drain | Timeouts |
|---|---|---|---|
| Reboot | Graceful reboot, then waits for a new boot ID, Ready and, on a control plane, its etcd member. Pods leave and start elsewhere; if Ichor's controller runs there it restarts and carries on. | Ichor cordons and evicts pods, respecting PodDisruptionBudgets, after moving storage replicas off. It uncordons at the end. | Drain 10 min, then the operation pauses and uncordons. Reboot call 2 min. Wait for the node 20 min. |
| Restart service | Restarts `kubelet`, `containerd` or `cri`. `etcd`, `apid`, `trustd` and `machined` are refused. Pods keep running. | None | Waits 15 s, then for Ready and the service Running and healthy; 20 min in all. |
| Defrag etcd | Defragments that member; it is slow or unavailable meanwhile. Run it on one node at a time. | None | 10 min. |
| Forfeit leadership | The member hands leadership over; writes pause for a moment. | None | A different leader within 20 min. |

## What refuses or fails

| Action | Refused when |
|---|---|
| Reboot | The node is not Ready. On a control plane: the cluster has exactly two (a stop leaves one member, below quorum), or etcd does not list every control plane. On the last schedulable Ready worker, unless `--override` (needs the `override` verb on the node). |
| Restart service | The service is not on the allowlist. |
| Defrag etcd | The node is a worker; or it leads a multi-member etcd and `--allow-leader` is not set (writes pause for the whole cluster); or a member is missing; or etcd still reports an alarm afterwards. |
| Forfeit leadership | The node is a worker, etcd has one member, the node does not lead, or a member is missing. |

The only control plane can be rebooted: the Kubernetes API is unavailable until
it is back, and the plan says so. With no other Ready, schedulable node the
drain is skipped (a `DrainSkipped` Event on the Node) and the pods restart with
the node. A drain that times out names the pods and budgets that held it.

## Failure, retry and approval

1. A failed step pauses the operation and, for a reboot, undoes the cordon and
   the storage move.
2. If the failure is transient (a node or the API briefly unreachable) and the
   action was not yet sent, the controller retries by itself: up to 5 times,
   after 1, 2, 4, 8 and 16 minutes. The pause message names the attempt.
3. It never retries on its own once the action was sent, so no reboot or
   restart goes out twice. A wait that runs out is such a case.
4. Otherwise, or when the retries are used up, fix the cause and run
   `ichor resume operation NAME`; the failed step runs again.

There is no approval step for these four actions: the confirmation at the
command is the gate, and Kubernetes RBAC decides who may start them (create on
`talosoperations`, held by `operator` and `admin`). Only an operation created
paused and marked as awaiting approval, such as a storage preset's upgrade,
waits for `ichor approve operation NAME`.

See also [Operations](../concepts/operations.md) and the reference pages
[`ichor reboot node`](../reference/cli/ichor_reboot_node.md),
[`ichor restart service`](../reference/cli/ichor_restart_service.md),
[`ichor defrag etcd`](../reference/cli/ichor_defrag_etcd.md) and
[`ichor forfeit-leadership etcd`](../reference/cli/ichor_forfeit-leadership_etcd.md).

---
title: "Operations"
description: "TalosOperation: the record of an upgrade, rotation or node action, how it takes the cluster lock, and how to pause, resume and retry it."
sidebar_group: "Changes over time"
sidebar_order: 20
---

# Operations

An action rather than a desired state (upgrade, rotation, reboot, shutdown,
restore) is a `TalosOperation`: the record of the action in progress, which the
controller carries out. The CLI (`ichor upgrade talos ...`), the UI and
`kubectl` all create the same thing ([field
reference](../reference/crds/talosoperation.md)). State lives in `status`
(`phase`, a per-machine list), so a restarted controller or new leader resumes
where it was.

## Phases

`Pending` until it holds the cluster lock, `InProgress` while it advances
machines, `Completed` at the end. `Paused` is a stop for one of three reasons.
`ShutDown` is the resting phase of a `shutdown` operation: every machine has
been asked to stop, and it becomes `Completed` once every machine answers on a
new boot.

![The phases of a TalosOperation. Pending becomes InProgress when the operation takes the cluster lock, or Paused while another operation holds it. InProgress ends as Completed when all machines are done, or as ShutDown for a shutdown operation, which becomes Completed once every machine answers again. Paused has three causes: waiting for the cluster lock, which lifts by itself; a person pausing it, which releases the lock and ends with ichor resume; and a failed step, which retries by itself up to 5 times on a transient error and otherwise runs again on ichor resume.](../images/operation-phases.light.drawio.svg)

*The phases of a TalosOperation, and the three causes of Paused.*

## Kinds

Each `spec.kind` has its own executor; all share the lock and state machine.
Fields besides `spec.clusterRef`, `spec.paused` and `spec.maxUnavailable`:

| Kind | What it does | Own fields |
|---|---|---|
| `talos` | Upgrades Talos (and the installer schematic) one node at a time, control planes first | the target is in the `TalosCluster` spec (`ichor upgrade talos`) |
| `kubernetes` | Upgrades Kubernetes in three stages: control-plane components, kube-proxy, kubelets | the target is in the `TalosCluster` spec (`ichor upgrade kubernetes [VERSION]`; `--list` shows the versions the cluster can move to, `--max-unavailable`, `--follow`) |
| `ichor` | Upgrades Ichor itself through an installer Job | |
| `cni` | Moves Cilium to the chart this Ichor embeds, or switches the kube-proxy mode | `spec.cni.action`, `spec.cni.chartVersion` |
| `config` | Rolls out configuration changes that need a reboot, a node at a time | |
| `node` | One action on one machine | `spec.node.machineRef`, `action` (`reboot`, `shutdown`, `restart-service`, `etcd-defrag`, `etcd-forfeit-leadership`, `reset`), `service`, `allowLeader`, `confirm`, `override`, `powerOff`, `wipeMode` |
| `shutdown` | Stops the whole cluster for planned downtime | |
| `ca-rotation` | [CA rotation](ca-and-key-rotation.md) | `spec.caRotation.targets` |
| `key-rotation` | [Key rotation](ca-and-key-rotation.md) | `spec.keyRotation.targets`, `tokenSettle`, `ignoreLegacyTokens` |
| `etcd-restore` | Rewinds the cluster to a stored etcd snapshot | `spec.etcdRestore.backupRef`, `snapshot`, `confirm` (the cluster name, typed) |

Upgrades are checked first: the target must be a final version newer than the
running one, within the Talos and Kubernetes skew rules, and the Talos,
Kubernetes and Cilium combination must be in Ichor's compatibility table. Only
the override verb bypasses a refusal, and it leaves a Warning Event.

**Order.** Control planes first, strictly one at a time with an etcd health
check before each. Workers follow, up to `spec.maxUnavailable` at once (default
1). The annotation `ichor.maesh.dev/upgrade-hold` skips a machine; the label
`ichor.maesh.dev/upgrade-order` sorts machines within a tier.

## The cluster lock

Disruptive work never overlaps, whoever created it, so two operations written
with `kubectl` or GitOps run one after the other. Each operation locks the
cluster as `ichor-controller/op/<namespace>/<name>`; a control-plane join or
removal locks as the machine; the Cilium Helm Job locks too.

- A waiting operation is `Paused` with reason `ClusterLockHeld`, naming the
  holder, and starts when it finishes. A lock whose holder was deleted or
  completed is taken over.
- An operation paused for a person (failed step, `spec.paused`) releases the
  lock and takes it again on resume, waiting if another holds it by then. A
  pause that lifts by itself, such as a rotation blocked on a step, keeps it
  (ADR 0162).

## Why an operation is Paused

Read the reason from the `Paused` condition (`ichor describe operation NAME`).

| Reason | Meaning | What to do |
|---|---|---|
| `AwaitingApproval` | Created paused with the annotation `ichor.maesh.dev/awaits-approval`, such as the upgrade a storage preset creates. No step has run | `ichor approve operation NAME` (Approve in the UI). It refuses any other operation |
| `ClusterLockHeld` | Another operation, a control-plane join or a Cilium Job holds the lock | Nothing; it starts when the holder finishes |
| `ClusterTransitioning` | The cluster has an even number of approved control planes, a replacement is under way | Finish or undo the replacement; it starts by itself when the count is odd |
| `ClusterShuttingDown`, `ClusterCARotating`, `ClusterKeyRotating` | A shutdown or rotation is under way | Nothing; it starts when that finishes |
| `OperatorPaused` | `spec.paused`, set by `ichor pause operation NAME`. Releases the lock | `ichor resume operation NAME` takes the lock again |
| `StepFailed` | A step failed; `status.failureReason` says how. Nothing is rolled back across the cluster; the lock is released | Retried automatically on a transient error (below); otherwise fix the cause, then `ichor resume operation NAME` (Resume in the UI) |
| `NoExecutor` | The controller has no Talos client (or node executor) for this kind | A controller setup problem, not the operation; check the controller's logs |
| `OwnNodeUnread` | The controller cannot tell which machine it runs on. No step failed | Nothing to resume; the pause lifts when a machine answers, its Node appears or the machine is removed |

`FellBack` and `VerifyTimedOut` are not pause reasons. They are the `False`
reasons of a machine's `TalosUpgrade` or `KubernetesUpgrade` condition,
recorded just before the operation pauses with `StepFailed`:

| Reason | Meaning | What to do |
|---|---|---|
| `FellBack` | The node rebooted but did not run the target version or schematic: it fell back to its previous A/B slot | Check the installer image and schematic, then resume, which sends the upgrade again |
| `VerifyTimedOut` | The node did not come back within the verify window (20 minutes for a Talos upgrade) | Bring the node back, then resume; a node that came back late is accepted |

**Automatic retry** (ADR 0174). Operations of kind `talos`, `kubernetes`,
`config` and `node` whose step failed on a transient error (a node or the API
unreachable, an etcd timeout) are retried after 1, 2, 4, 8 and 16 minutes, five
attempts per step. The message says "Retrying automatically at TIME (attempt N
of 5)", and `status.autoRetry` holds the count. Never retried automatically: a
step whose request may already have reached the node, a node reset, a person's
pause, and the other kinds. After the fifth attempt it waits for a person.

A person's retry request is an annotation, never a change to `spec`:

```sh
kubectl annotate talosoperation NAME ichor.maesh.dev/retry=true
```

The controller acts on it once: records `status.lastRetry`, clears the failure,
runs the step and removes the annotation. It takes precedence over the timer and
resets the count. Any other value is ignored. An operation you paused with
`spec.paused` stays paused until you resume it.

## Who may start which

A kind that can take a cluster down needs its own verb, `start` on a
subresource such as `talosoperations/ca-rotation`, held only by `ichor-admin`
([Security model](security.md)).

Related: [Node actions](../guides/node-actions.md), [Shutdown and power](../guides/shutdown-and-power.md), [`ichor get operations`](../reference/cli/ichor_get_operations.md),
[`ichor upgrade`](../reference/cli/ichor_upgrade.md),
[`ichor reboot node`](../reference/cli/ichor_reboot_node.md).

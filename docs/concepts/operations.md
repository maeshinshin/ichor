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

![The phases of a TalosOperation. Pending becomes InProgress when the operation takes the cluster lock, or Paused while another operation holds it. InProgress ends as Completed when all machines are done, or as ShutDown for a shutdown operation, which becomes Completed once every machine answers again. Paused has three causes: waiting for the cluster lock, which lifts by itself; a person pausing it, which releases the lock and ends with ichor resume, which takes it again; and a failed step, which is never retried by itself and runs again on ichor resume, through the retry annotation.](../images/operation-phases.light.drawio.svg)

*The phases of a TalosOperation, and the three causes of Paused.*

## Kinds

Each `spec.kind` has its own executor; all share the lock and state machine.

| Kind | What it does |
|---|---|
| `talos` | Upgrades Talos (and the installer schematic) one node at a time, control planes first |
| `kubernetes` | Upgrades Kubernetes in three stages: control-plane components, kube-proxy, kubelets |
| `ichor` | Upgrades Ichor itself through an installer Job |
| `cni` | Moves Cilium to the chart this Ichor embeds, or switches the kube-proxy mode |
| `config` | Rolls out configuration changes that need a reboot, a node at a time |
| `node` | One action on one machine: reboot, shutdown, restart a service, etcd defrag, forfeit etcd leadership, reset |
| `shutdown` | Stops the whole cluster for planned downtime |
| `ca-rotation`, `key-rotation` | [CA and key rotation](ca-and-key-rotation.md) |
| `etcd-restore` | Rewinds the cluster to a stored etcd snapshot |

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

## Pause, resume and retry

`status.failureReason` and the `Paused` condition's reason tell the causes apart.

| Cause | Reason | Way out |
|---|---|---|
| Never started: created paused and approval-gated, such as the upgrade a storage preset creates | `AwaitingApproval` | `ichor approve operation NAME` (Approve in the UI); refused on any other operation |
| You paused it: `ichor pause operation NAME` sets `spec.paused`, stops advancing and releases the lock | | `ichor resume operation NAME` takes the lock again |
| A step failed: never retried by itself, nothing rolled back across the cluster, lock released | `failureReason` | fix the cause, then `ichor resume operation NAME` (Resume in the UI) runs the failed step again |

The retry request is an annotation, never a change to `spec`:

```sh
kubectl annotate talosoperation NAME ichor.maesh.dev/retry=true
```

The controller acts on it once: records `status.lastRetry`, clears the failure,
runs the step and removes the annotation. Any other value is ignored. An
operation you paused with `spec.paused` stays paused until you resume it.

## Who may start which

A kind that can take a cluster down needs its own verb, `start` on a
subresource such as `talosoperations/ca-rotation`, held only by `ichor-admin`
([Security model](security.md)).

Related: [`ichor get operations`](../reference/cli/ichor_get_operations.md),
[`ichor upgrade`](../reference/cli/ichor_upgrade.md),
[`ichor reboot node`](../reference/cli/ichor_reboot_node.md).

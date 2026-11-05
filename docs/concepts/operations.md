---
title: "Operations"
description: "TalosOperation: the record of an upgrade, rotation or node action, how it takes the cluster lock, and how to pause, resume and retry it."
sidebar_order: 20
---

# Operations

Anything that is an action rather than a desired state is a `TalosOperation`: an
upgrade, a rotation, a reboot, a shutdown, a restore. The resource is the
record of the action in progress, and the controller carries it out. Creating
one from the CLI (`ichor upgrade talos ...`), from the UI or with `kubectl`
is the same thing; see the [field reference](../reference/crds/talosoperation.md).

The state machine lives in `status` (`phase`, and a per-machine list), never in
the controller's memory, so a restarted controller or a new leader picks the
operation up where it was.

## Kinds

`spec.kind` names the action. Each kind has its own executor; they share the
lock, the state machine and the rules below.

| Kind | What it does | More |
|---|---|---|
| `talos` | Upgrades Talos (and the installer schematic) one node at a time, control planes first | |
| `kubernetes` | Upgrades Kubernetes in three stages: control-plane components, kube-proxy, kubelets | |
| `ichor` | Upgrades Ichor itself through an installer Job | |
| `cni` | Moves Cilium to the chart this Ichor embeds, or switches the kube-proxy mode | |
| `config` | Rolls out configuration changes that need a reboot, a node at a time | |
| `node` | One action on one machine: reboot, shutdown, restart a service, etcd defrag, forfeit etcd leadership, reset | |
| `shutdown` | Stops the whole cluster for planned downtime | |
| `ca-rotation` | Replaces the cluster CAs | [CA and key rotation](ca-and-key-rotation.md) |
| `key-rotation` | Replaces the service-account signing key and the secretbox key | [CA and key rotation](ca-and-key-rotation.md) |
| `etcd-restore` | Rewinds the cluster to a stored etcd snapshot | |

Upgrades are checked before anything runs: the target must be a final version
newer than the running one, within the Talos and Kubernetes skew rules, and
the Talos, Kubernetes and Cilium combination must be in Ichor's compatibility
table. A refusal can be bypassed only with the override verb, which leaves a
Warning Event.

## Order and parallelism

Control planes go first and strictly one at a time, with an etcd health check
before each. Workers follow, up to `spec.maxUnavailable` at once (default 1).
A machine carrying the annotation `ichor.maesh.dev/upgrade-hold` is skipped, and
the label `ichor.maesh.dev/upgrade-order` sorts machines within their tier.

## The cluster lock

Disruptive work never overlaps. Each operation takes a lock on the cluster
under its own identity (`ichor-controller/op/<namespace>/<name>`), and a
control-plane join or removal takes it under the machine's. The Cilium Helm Job
takes it too. This holds whoever created the operations, so two written with
`kubectl` or by GitOps run one after the other.

An operation that has to wait is `Paused` with the reason `ClusterLockHeld`,
which names the holder, and starts when the holder finishes. A lock whose
holder was deleted, completed, or paused on a failed step is taken over. An
operation that an operator paused keeps its lock.

## Pause, resume and retry

There are two reasons an operation is `Paused`, and they are told apart by
`status.failureReason`.

- **You paused it.** `ichor operation pause NAME` sets `spec.paused`. The
  operation stops advancing but keeps the cluster lock, so nothing else
  disruptive starts in the middle of it. `ichor operation resume NAME` lifts it.
- **A step failed.** The operation pauses with a `failureReason` and is never
  retried by itself, and nothing is rolled back across the cluster. Fix the
  cause, then run `ichor operation resume NAME` (or press Resume in the UI): on
  an operation paused by a failure it asks the controller to run the failed
  step again.

The request to run the step again is an annotation, not a change to `spec`.
With `kubectl` it is:

```
kubectl annotate talosoperation NAME ichor.maesh.dev/retry=true
```

The controller acts on it once, records the time in `status.lastRetry`, clears
the failure, runs the step again and removes the annotation, so the next
`retry=true` is a new request. Any other value is ignored. An operation that
you paused with `spec.paused` stays paused until you resume it.

## Who may start which

Starting a kind that can take a cluster down needs a verb of its own, `start`
on a subresource such as `talosoperations/ca-rotation`, and only `ichor-admin`
holds those. See [Security model](security.md).

Related: [`ichor operation`](../reference/cli/ichor_operation.md),
[`ichor upgrade`](../reference/cli/ichor_upgrade.md),
[`ichor node`](../reference/cli/ichor_node.md).

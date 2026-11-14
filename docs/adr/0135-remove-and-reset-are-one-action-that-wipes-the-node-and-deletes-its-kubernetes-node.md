# 0135. Remove and reset are one action that wipes the node and deletes its Kubernetes Node

Status: Accepted · Date: 2026-11-07 · Design section: Nodes · Supersedes: the remove/reset split of 0085 and its amendment "the Kubernetes Node stays after a remove" · Builds on: 0032, 0042, 0065, 0130

Decided: "Remove node" is the only way to take a node out of a cluster in the UI, and `ichor delete node NODE [--reboot]` in the CLI. It runs the existing removal flow (lock, quorum and count checks, storage evacuation, drain, etcd member removal for a control plane), then a graceful-as-today Talos Reset of STATE and EPHEMERAL, which ends with a power off (default) or a reboot into maintenance mode (`--reboot`, "After wiping" in the UI), then deletes the Kubernetes Node, then clears the finalizer. The wipe is no longer optional. It needs the typed machine name and the override permission, with no reason. The `reset` action, `RemoveMachine` and every proto and CRD field stay (`ichor node reset` was removed on 2026-11-07, see the amendments below); `RemoveMachine` and a `reset` operation both run this flow.

## Context

ADR 0085 had two ways out: `reset` (wipe, reboot into maintenance, Node kept and annotated) and `remove` (delete the machine without a wipe; the node kept running with the cluster's credentials, and its Node stayed because the kubelet would register it again). Talos's own scale-down is `talosctl reset` (cordon, drain, leave etcd, wipe, power off, leave discovery) followed by `kubectl delete node` (Talos v1.14, "Scaling down"). The user decided that the unwiped remove should go: it left a live node holding the cluster's PKI and a NotReady Node and Pending DaemonSet pods until someone ran `kubectl delete node`.

## Decision

1. **One flow.** `RemoveMachine` sets `allow-delete`, `reset-on-delete` (ADR 0042) and, unless `reboot` is asked, `reset-power-off` on the machine, adds the removal finalizer and deletes it. A `reset` TalosOperation hands the machine to the same flow as before, with the new optional `spec.node.powerOff` mapped to `reset-power-off`; unset it reboots, which is what a reset always did, so installed operations behave as before.
2. **Power off or reboot.** `TalosClient.Reset(ctx, wipeMode, powerOff)` builds the request with `reclaim.ResetRequestWipe` and sets `Reboot = !powerOff`. Talos powers the node off after a Reset without `reboot` (`talosctl reset --reboot=false`, Talos v1.14 API `ResetRequest.reboot`). The Reset stays non-graceful from the node's side because the drain and the etcd leave already ran.
3. **The Kubernetes Node.** After Talos accepted the Reset the controller waits (requeue every 10 s, at most 5 minutes counted from the moment the Removal condition became `ResetRequested`) until the Node is not Ready, deletes it, and clears the finalizer. A wiped machine has no credentials left, so its kubelet cannot register the Node again; the wait only covers the seconds between the request and the wipe, during which a running kubelet could. A resumed reconcile sees `ResetRequested` and goes straight to the wait, without draining a node that is wiping. If the Node is still Ready after the bound it is not deleted, is annotated as reset (so discovery stops counting its address, ADR 0042), the removal finishes, and a Warning Event `KubernetesNodeNotDeleted` names it with `kubectl delete node NAME`.
4. **A node that does not answer.** If the Reset cannot be sent and the Node is missing or not Ready, the node is down: the removal goes on without the wipe, the Node is deleted, the Removal condition and a Warning Event `NodeStillHasClusterCredentials` say it was not wiped and its disk still holds the cluster's PKI. If the Reset fails on a node whose Node is Ready, the removal is held (`ResetFailed`) as before: a live node that will not reset is not dropped. A control plane below the declared count is still removable when its etcd member is dead (ADR 0106).
5. **A hand-deleted machine.** Deleting a TalosMachine without the annotations (a control plane with `kubectl delete`, ADR 0032) behaves as before: etcd member removed, no wipe, no Node deletion, the `NodeStillHasClusterCredentials` Event. Only the UI, the CLI and the reset operation ask for the wipe.
6. **Rights.** The remove needs the typed name and the override (checked against the `override` verb on the machine, ADR 0065; no reason, ADR 0130), on top of the caller's own update and delete rights on the machine, which the backend uses. `RemoveMachineRequest` gains `reboot` and `override`, `NodeActionRequest` gains `reboot` (remove only), `TalosOperationNodeSpec` gains `powerOff` (reset only; a CEL rule says so). The UI offers "Remove node" to a role that holds update, delete, the reset start verb and the override on machines (the merge of `startNodeReset` and `deleteMachines`; the proto fields stay) and drops the separate Reset item.
7. **RBAC.** The manager ClusterRole gains `delete` on `nodes`. Kubernetes RBAC cannot limit it to the Nodes of machines being removed; the controller deletes only the Node named by the machine it removes.
8. **Plan text.** The steps are: move storage replicas, cordon and drain, remove the etcd member (control plane), wipe STATE and EPHEMERAL and power off or reboot, delete the Kubernetes Node, delete the machine. The warning says it cannot be undone, that STATE and EPHEMERAL are wiped and user disks are not touched, and what the node does next; the UI dialog also has the "After wiping" choice (a Select) and the override checkbox, and the CLI prints the same plan and, afterwards, how to add the machine again (a wiped machine shows up as a new pending machine).

## Alternatives rejected

- Shutting the node down without a wipe and then deleting the Node (the first design): the node would keep the cluster's PKI and could be powered on into a cluster that no longer lists it.
- Removing the `reset` action, `RemoveMachine` or their fields: CRD and proto fields are append-only and installed builds carry them.
- Deleting the Node as soon as the Reset is accepted: a kubelet that is still running for a few seconds could register it again.
- A new object to record removed Nodes: the Removal condition's transition time already says when the Reset was accepted.

## Risks

- `delete` on every Node is a broader RBAC verb than the controller had.
- Machines removed before this change were not wiped; the adopt guide says how to add them again (`ichor adopt --add-machine`).

## Amendment (2026-11-07): machines removed before this change

The risk "machines removed before this change were not wiped; the adopt guide says how to add them again (`ichor adopt --add-machine`)" held only for a machine whose configuration adopt could represent; one whose TalosMachine had a hostname and link choice was blocked by adopt and refused by reclaim. ADR 0136 lets `ichor reclaim node ADDRESS --cluster NAME` (or the UI Reset and add) wipe such a machine with the cluster's own credentials and delete its Kubernetes Node.

## Amendment (2026-11-07): `ichor node reset` is removed

2026-11-07: `ichor node reset` is removed; use `ichor delete node --reboot`. The command, its `--wipe-mode` flag and the CLI's reset helpers go with it. `RemoveMachine`, the `reset` action, `spec.node.wipeMode`, `spec.node.powerOff` and every proto and CRD field stay, so the API and the UI keep the reset operation.

## Amendment (2026-11-07): a remove chooses what it wipes

`ichor node reset --wipe-mode` went with the command, which left the SecureBoot reinstall (ADR 0123, `docs/guides/hardening.md`) without a CLI or UI way to erase the whole system disk: it needed a hand-written `reset` TalosOperation. `RemoveMachineRequest` gains `wipe_mode` (field 5; `NodeActionRequest.wipe_mode` now also goes with remove, for the plan), with the values of `TalosOperation.spec.node.wipeMode`: empty wipes STATE and EPHEMERAL only (today's remove), `system-disk` wipes the whole system disk (boot and EFI included, so the node boots its boot image again), `all` also wipes every user disk. `RemoveMachine` records a non-empty mode in the annotation `ichor.maesh.dev/reset-wipe-mode` (`deleteguard.ResetWipeModeAnnotation`, the one a reset already used), the removal flow passes it to `TalosClient.Reset` as before, and the plan steps and warning name what is erased. The CLI is `ichor delete node NODE [--reboot] [--wipe-mode system-partitions|system-disk|all]`, where `system-partitions` is the default and sends nothing; the UI's Remove node dialog has a "What to wipe" Select with a destructive note for the two larger modes. Nothing is removed or renamed; the override and the typed name still apply. A bigger wipe leaves nothing to boot from on a power off, so combine `system-disk` or `all` with `--reboot` when the node should reinstall from an attached ISO or PXE.

## Amendment (2026-11-15): the wipe is confirmed only by a reboot

Decision 3 is still how a power-off removal completes: the wait is for the Node to go NotReady, 10 s requeues, at most five minutes from `ResetRequested`. A powered-off machine cannot answer in maintenance mode, so the removal does not wait for that answer, and it completes on the Node going NotReady with a Normal Event `WipeNotConfirmedPoweredOff` that says the wipe is not confirmed because the machine was powered off.

With `--reboot` (`reset-power-off` unset) the removal also waits, for at most the same five minutes counted from `ResetRequested`, for the machine to answer in maintenance mode with its UUID (`resetMaintenanceProbe`). The Node is deleted and the finalizer cleared only after that answer, or after the bound with a Warning Event `NodeStillHasClusterCredentials` that says the wipe is not confirmed. Decisions 1, 2, 4 and 5 are unchanged.

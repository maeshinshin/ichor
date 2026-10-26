# 0080. Reboot-requiring configuration changes roll out through an explicit `config` operation

Status: Accepted · Date: 2026-10-26 · Design section: Configuration reconcile (step 4), Upgrades

Decided: a new `TalosOperation` kind `config` applies the pending reboot-requiring changes of the approved machines, one at a time (control planes first with an etcd membership check before each, then workers within `maxUnavailable`, the controller's own node last), staged and then rebooted; the machine reconciler only reports them, now naming the fields, and never reboots a node

## Context

Seen live: when a rendered configuration differed from a node in a way that needs a reboot, `refuseReboot` took the
cluster lock, released it and set `Ready=False/RebootRequired` with "this phase does not reboot nodes". Nothing
could ever apply such a change, so a patch touching a network interface, or any field `pkg/fieldmeta` does not list
as safe, stayed pending forever. The message did not say which fields.

The design says (Configuration reconcile, step 4): "Reboot-requiring changes take the cluster lock and go one node
at a time through the operation hook and drain, control planes with etcd checks in between." ADR 0009 adds that the
reboot necessity is predicted from `pkg/fieldmeta` because Talos will not answer it, and that this has to be known
*before* the apply, since the hook and the drain precede a reboot-causing apply.

Nothing in the design or the ADRs says a reboot-requiring change should roll out by itself once a patch exists or a
machine is approved. "Approved" gates a machine joining the cluster (ADR 0010), not every later reboot. ADR 0068
puts every disruptive, machine-by-machine action behind an operation that a person starts after seeing the plan.

## Decision 1: the trigger is an operation someone starts

I followed the shape of ADR 0068 rather than a reconciler that reboots on its own. A reboot of a control plane or of
a storage node is the most disruptive thing a patch edit can cause, and a patch edit is the most casual write Ichor
has; the machine reconciler also runs on every machine independently, so it has no cluster-wide view to sequence a
rollout with. So:

- `OperationKind` / `spec.kind` gains `config` (`OPERATION_KIND_CONFIG`). It takes no target and writes no spec
  (the spec was already changed, by the patch or the cluster edit, which is the reason the machines are waiting).
- `ichor config apply [--cluster NAME] [--max-unavailable N] [--follow] [--yes]` and a "Apply config (reboots)"
  button on the cluster page start it through `OperationService`, with ADR 0068's Plan/Create pattern: the plan lists
  the machines that wait for a reboot, the field paths that need it per machine, the order, and says they reboot. The
  UI also asks for the cluster name to be typed, as for a shutdown and a CA rotation. `CreateOperation` is refused
  (`blocked`) when no machine is waiting, and while another operation on the cluster is unfinished.
- The machine reconciler never calls the apply or reboot RPCs for a reboot-requiring change. A test pins this.

## Decision 2: what the machine reconciler reports

`TalosMachine.status.rebootPaths` lists the field paths that need the reboot (`fieldmeta.Impact.RebootPaths`, the
paths that are neither safe nor install-time-only), set with `Ready=RebootRequired` and cleared on convergence. The
Ready message names the first five ("fields: a, b, and 3 more") and says to run `ichor config apply` or use the
cluster page. The plan reads `rebootPaths`; only machines that have it are listed.

`refuseReboot` takes no cluster lock: it only records `rebootPaths` and sets RebootRequired. Its earlier probe (under
`ichor-controller/machine`, which replaced an `ichor-controller` probe that could free a running operation's lock) only
changed which refusal reason was shown, and protected nothing, since the `config` operation takes the lock when it works.

## Decision 3: what the executor does to one machine

`internal/controller/config_rollout.go`, an `AdvanceMachineFunc` like ADR 0043's and 0057's, so the lock, pause on
failure, `spec.paused`, `maxUnavailable`, the `ichor.maesh.dev/upgrade-hold` annotation, the order label, the
`ClusterShuttingDown` / CA-rotation refusals and one-control-plane-at-a-time are the backbone's (ADR 0026, 0067).
Per machine, every decision re-derived from the node and two markers (a `ConfigRollout` condition on the machine and
the existing `StorageEvacuation` one), so a restarted controller resumes:

1. The node already runs the rendered configuration: done, nothing called (a rollout over machines that do not need
   it touches nothing).
2. Otherwise re-assess the diff with `pkg/fieldmeta`. Unsupported or install-time-only paths fail the step. If no
   path needs a reboot, apply with no reboot and finish.
3. Dry run (ADR 0009), node `Ready` or fail, for a control plane the node's etcd must list every approved control
   plane (a reboot takes one member down; with another already missing quorum would be lost), the storage
   evacuation hook (a timeout pauses; scheduling re-enabled).
4. Mode: **`STAGED`**, then the **Reboot** RPC. Never `AUTO` (an alias for `NO_REBOOT`, ADR 0009), and not the
   deprecated `REBOOT` mode: staging writes the configuration to disk without restarting anything, which lets the
   boot ID be recorded in the marker before the reboot is asked for, so a controller killed between the two resumes
   into "wait for the boot ID to change" instead of rebooting the node a second time. Each RPC has its own
   two-minute deadline, not what is left of the 15 s dial (the bug ADR 0043 and 0057 each fixed once).
5. The reboot is the graceful one, not forced, so Talos cordons and drains the node before it stops services, as for
   `Shutdown` and upgrade; Ichor does no cordon or drain of its own (design, Upgrades).
6. After the reboot: dial or read failures mean "still rebooting" until 20 minutes after the marker was set, then the
   step fails. A boot ID that changed with a node not running the rendered configuration fails. Otherwise wait for
   `Ready` and, for a control plane, the node's return to etcd, then end evacuation and clear the marker. Any failure
   after the reboot request re-enables scheduling and records itself on the marker.

Order and concurrency: control planes one at a time, then workers up to `maxUnavailable`, each tier by the order label
(`upgrade.SortByOrder`, the same function the plan uses, so the order shown is the order run). For this kind the
machine whose Kubernetes node is the controller's own (`NODE_NAME`) is taken out of its tier and goes after the
workers, so the reboot that stops the controller is the last step and nothing is left to wait for. The etcd check
before each control plane is what "etcd checks in between" means: the previous one has to be back in etcd (its own
verify step) and every other member has to be there before the next one goes.

## Consequences

- A reboot-requiring patch is no longer stuck, and nothing reboots until a person runs the command or presses the
  button. Until then the machine says which fields are waiting.
- The rollout applies the machine's whole rendered configuration, not only the reboot paths: any other pending
  safe change lands in the same staged apply. The plan lists the reboot fields because those are what need
  consent.
- A single-control-plane cluster can be rolled; the API server is down while it reboots. The plan does not special-case
  it beyond the typed cluster name in the UI.
- Drift on a node that Ichor did not change (`driftPolicy`) is also a reboot-requiring diff when it touches those
  fields, and also waits for the operation: drift is never auto-rebooted.

## Not verified

- Against a real node. The controller tests run the executor over a fake Talos client (the calls, their order,
  the deadlines, the resume points); no node was staged or rebooted.
- That Talos 1.14.3's `Reboot` sequence drains the node the way its `Shutdown` and `Upgrade` sequences are read to
  (`drain` phase unless forced). The executor relies on it and does not drain by itself.
- That a staged configuration is activated on the next boot for every field in `pkg/fieldmeta`'s reboot default. The
  step fails (rather than succeeding) when the node comes back not running the rendered configuration.
- Rolling the node the controller runs on, last: that the resumed controller finishes the step was reasoned from the
  marker, not run.
- The lock's five-minute duration against a 20-minute verify: the backbone renews it on every reconcile (30 s), as
  for upgrades.

## Not done

- The `ichor config apply` command and the button do not open a pull request for a GitOps-tracked spec: they write no
  spec at all.
- The same-identity lock hazard exists in other machine-reconciler paths (`removal.go` takes the lock as
  `ichor-controller` too); only `refuseReboot` was changed here.
- A per-machine apply (one named machine) is not offered; hold the others with `upgrade-hold`.

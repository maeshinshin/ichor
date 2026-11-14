# 0080. Reboot-requiring configuration changes roll out through an explicit `config` operation

Status: Accepted · Date: 2026-10-26 · Design section: Configuration reconcile (step 4), Upgrades

Decided: a new `TalosOperation` kind `config` applies the pending reboot-requiring changes of the approved machines, one at a time (control planes first with an etcd membership check before each, then workers within `maxUnavailable`, the controller's own node last), staged and then rebooted; the machine reconciler only reports them, now naming the fields, and never reboots a node

## Context

When a rendered configuration differed from a node in a way that needs a reboot, `refuseReboot` took the
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

The trigger is an operation, not a reconciler that reboots on its own, as in ADR 0068. A reboot of a control plane or of
a storage node is the most disruptive thing a patch edit can cause, and a patch edit is the most casual write Ichor
has; the machine reconciler also runs on every machine independently, so it has no cluster-wide view to sequence a
rollout with. So:

- `OperationKind` / `spec.kind` gains `config` (`OPERATION_KIND_CONFIG`). It takes no target and writes no spec
  (the spec was already changed, by the patch or the cluster edit, which is the reason the machines are waiting).
- `ichor rollout config [--cluster NAME] [--max-unavailable N] [--follow] [--yes]` and a "Apply config (reboots)"
  button on the cluster page start it through `OperationService`, with ADR 0068's Plan/Create pattern: the plan lists
  the machines that wait for a reboot, the field paths that need it per machine, the order, and says they reboot. The
  UI also asks for the cluster name to be typed, as for a shutdown and a CA rotation. `CreateOperation` is refused
  (`blocked`) when no machine is waiting, and while another operation on the cluster is unfinished.
- The machine reconciler never calls the apply or reboot RPCs for a reboot-requiring change.

## Decision 2: what the machine reconciler reports

`TalosMachine.status.rebootPaths` lists the field paths that need the reboot (`fieldmeta.Impact.RebootPaths`, the
paths that are neither safe nor install-time-only), set with `Ready=RebootRequired` and cleared on convergence. The
Ready message names the first five ("fields: a, b, and 3 more") and says to run `ichor rollout config` or use the
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
5. **Ichor cordons and drains the node itself, then reboots** (see "Draining and node names"). The reboot is the graceful one, not forced, but Talos 1.14.3's `Reboot` does not drain.
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
- A single-control-plane cluster can be rolled; the API server is down while it reboots. The plan says so (below), and
  the step reads the Node live and treats an unreachable API server after the reboot as "still waiting".
- Drift on a node that Ichor did not change (`driftPolicy`) is also a reboot-requiring diff when it touches those
  fields, and also waits for the operation: drift is never auto-rebooted.
- A staged configuration is assumed to be activated on the next boot for every field in `pkg/fieldmeta`'s reboot
  default; if the node comes back not running the rendered configuration, the step fails rather than succeeding.
- The node the controller runs on goes last; a restarted controller finishes its step from the marker.
- The lock lasts five minutes while the verify step can last 20 minutes; the backbone renews the lock on every
  reconcile (30 s), as for upgrades.

## Draining and node names

- **A missing hostname does not pause a rollout that has nothing to do.** The convergence check ("already runs the
  rendered configuration") comes before anything that needs a name, because `ichor init` leaves `spec.hostname` empty
  on the first control plane. `spec.hostname` is an optional override: the Kubernetes node name and etcd member name come from the node's own `Nodename` resource (`TalosClient.NodeName`), or, when the node cannot be asked
  (mid-reboot), from the Kubernetes Node whose address is the machine's Talos endpoint (`resolveNodeName`). The same
  applies to the Talos (ADR 0043) and Kubernetes (ADR 0057) upgrade executors. A control plane's etcd check names
  siblings that have a `spec.hostname` and counts members against approved control planes for the rest.
- **Ichor drains.** Talos 1.14.3 does not drain on a reboot:
  `Sequencer.Reboot` (`internal/app/machined/pkg/runtime/v1alpha1/v1alpha1_sequencer.go:257-271`) has only
  `StopAllPods` and the service stops, no drain phase, while `Shutdown` (`:363-381`, drain when not forced) and
  `Upgrade` (`:440-455`, drain unless node registration is skipped) have one; the controller that cordons reacts only
  to the shutting-down, upgrading and resetting stages (`controllers/k8s/node_cordoned_spec.go:73-76`), not to
  rebooting. `talosctl reboot` drains client-side behind `--drain` (`cmd/talosctl/cmd/talos/reboot.go`). The executor
  therefore cordons and drains before staging (`drain.go`, the kubectl drain
  helper `removal.go` already used, now shared): evictions through the Eviction API so PodDisruptionBudgets hold,
  DaemonSet and mirror pods left alone, pods owned by no controller refused rather than lost. Each reconcile drains for
  at most 90 s (the cluster lock is renewed once per reconcile and lasts five minutes) and resumes on the next; after
  10 minutes in total the step fails, the node is uncordoned and nothing is rebooted. Two Node annotations carry the
  state, `ichor.maesh.dev/drain-started-at` and `ichor.maesh.dev/cordoned-by-ichor`, so a restarted controller
  resumes and a node an administrator had cordoned is not uncordoned. The node is uncordoned once it runs the
  configuration, is Ready and (control plane) is back in etcd, and on any failure. Readiness is read live from the API
  server, not from the cache, and an unreachable API server after the reboot is "still waiting": on one control plane
  it is down for the length of the reboot, which the plan now says (`PlanOperationResponse.kubernetes_api_unavailable`,
  shown by the CLI and the dialog). `Upgrade` and `Shutdown` need nothing from Ichor (ADR 0043, 0067).
- **A whole document is reported as the document.** A document that is added or removed whole is reported as the
  document, `KernelModuleConfig br_netfilter (added)`, in `status.rebootPaths` and the `RebootRequired` message
  (`collapseDocumentPaths`); classification still runs on the field paths.

## Not done

- The `ichor rollout config` command and the button do not open a pull request for a GitOps-tracked spec: they write no
  spec at all.
- The same-identity lock hazard exists in other machine-reconciler paths (`removal.go` takes the lock as
  `ichor-controller` too); only `refuseReboot` was changed here.
- A per-machine apply (one named machine) is not offered; hold the others with `upgrade-hold`.

- Addendum: the config rollout, Talos upgrade, evacuation and removal paths write machine conditions on a fresh copy with retry on conflict, as the Kubernetes upgrade does (ADR 0057).

## Amendment: changes that restart the API server without a reboot

Decided: `pkg/fieldmeta` gets a third impact class, `APIServerRestart`: no reboot, but applying it re-renders the kube-apiserver static pod and restarts kube-apiserver (tens of seconds without `/readyz` per node on Talos 1.14.3, ADR 0094). The machine controller never applies it to a control plane (it would restart every control plane's API server at about the same time, a full API outage on an HA cluster). It reports `Ready=False/APIServerRestartRequired` with the fields in the new `status.apiServerRestartPaths`, and the `config` operation applies it.

**Audit, Talos v1.14.3 `internal/app/machined/pkg/controllers/k8s`.** The pod is rebuilt by `k8stemplates.APIServerPod(config, secretsVersion, configVersion)`, and the kubelet recreates it on any difference.

| Path | Evidence | Class |
|---|---|---|
| `cluster.apiServer.*` (extraArgs, certSANs, admissionControl, auditPolicy, env, resources, extraVolumes, image) | `control_plane.go` `ControlPlaneAPIServerController` copies image, extraArgs, extraVolumes, env, resources, startupProbes into `k8s.APIServerConfig`, which is the pod spec; `secrets/root.go` copies `certSANs` into the Kubernetes secrets root, whose version is the `AnnotationStaticPodSecretsVersion` annotation | APIServerRestart (was safe) |
| `KubeAPIServerConfig.*` | same controller (`K8sAPIServerConfig()` covers both forms); `.image` keeps its own reboot-required carve-out for the Kubernetes upgrade executor (ADR 0057) | APIServerRestart (was safe) |
| `KubeAuthenticationConfig` | `ControlPlaneAuthenticationController`; `render_config_static_pods.go` writes `authentication-config.yaml` and sums the resource version into `ConfigStatus.Version`, carried as `AnnotationStaticPodConfigFileVersion` | APIServerRestart (was safe) |
| `KubeAdmissionControlConfig`, `KubeAuditPolicyConfig`, `KubeAuthorizerConfig` | `ControlPlaneAdmissionControlController`, `ControlPlaneAuditPolicyController`, `ControlPlaneAuthorizationController`, same config-file version | APIServerRestart (had no rule: reboot-required) |
| `cluster.controllerManager.*`, `cluster.scheduler.*` and their documents | their pods restart too, but they are leader-elected on every control plane, so a gap there does not take the Kubernetes API down | stays safe |

A worker has no kube-apiserver, so on a worker the class is applied like a safe change. The Kubernetes upgrade executor (ADR 0057) still accepts a pending API-server change alongside the image: it already restarts the API server one node at a time.

**Rollout.** `config` selects a machine with `status.rebootPaths` or `status.apiServerRestartPaths`. For a control plane whose changes need only the restart, the step is: dry run, record the marker (`ConfigRollout` True, reason `APIServerRestarting`, message = the running kube-apiserver container ID read from Talos's `StaticPodStatus`), apply with no reboot, no drain, no evacuation and no etcd check (etcd is untouched). It finishes when the container ID differs from the recorded one (or a minute passed, for a change that left the pod alone) and the node's own `https://<node>:6443/readyz` answers 200, verified against the cluster's Kubernetes CA, anonymously. The operation's one-at-a-time order then starts the next control plane. Past three minutes the step records `APIServerNotReady`, the operation pauses with "kube-apiserver on X did not answer /readyz within 3m0s ...", and ADR 0095's retry waits again with a fresh clock instead of calling the node done. A change that needs a reboot as well takes the reboot path, which already covers it.

**Resumable.** The marker is written before the apply, so a controller killed in between sees the old configuration and applies again; killed after, it sees the new one and only waits. While the marker is set, any failure to read the cluster or the node is waiting (bounded by the same three minutes), not a pause: the controller may run on a control plane whose own API server is the one restarting. The node the controller runs on is last, as for reboots.

**Plan.** `PlanOperation` for `config` is `RESTART_COMPONENTS` when no selected machine needs a reboot and `REBOOT` otherwise, and sets `kubernetes_api_unavailable` for a single control plane. The CLI and the UI say "Restarts the Kubernetes API server on each control plane, one at a time; no reboot."

Known limits: the probe assumes the local API port 6443. The container-ID signal is the kubelet's view and can lag; the one-minute fallback covers a change that never alters the pod.


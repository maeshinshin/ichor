---
sidebar_group: "Cluster lifecycle"
sidebar_order: 17
---

# Upgrades

![How upgrades run. A Talos upgrade pre-checks, then upgrades control planes one at a time, each through the hook, the Upgrade API, a reboot and a verification, then workers within maxUnavailable. A Kubernetes upgrade runs three stages, control-plane components, kube-proxy and bootstrap manifests, then kubelets, each finished on every machine first. A failed step pauses the operation and releases the cluster lock; a transient error is retried after 1, 2, 4, 8 and 16 minutes, and anything else waits for a person to resume.](../../images/upgrades.light.drawio.svg)

*Talos, Kubernetes and Ichor are upgraded by separate operations; a failed step pauses, and only a transient one retries itself.*

## The release set

Each build embeds `pkg/release.Pinned`: the Talos release and version contract,
the Kubernetes release and the Cilium chart. It decides a first install (`ichor
init`, CLI defaults, the UI's proposed values) and never bounds an upgrade:
Talos, Kubernetes and Cilium each upgrade on their own, and an Ichor upgrade does
not move the cluster.

## Valid targets

A final semantic version newer than the running one, and:

| Component | Rule |
|---|---|
| Talos | `upgrade.TalosPath` accepts the step: the target's minor sets a minimum running version and denies listed ones; minors may be skipped, though adjacent is recommended. Each machine reboots. |
| Kubernetes | At most one minor up (`upgrade.KubernetesSkew`); the cluster's Talos supports it (`upgrade.KubernetesPairing`). |
| Pairs | Talos with Kubernetes, and Kubernetes with the installed Cilium, against `pkg/release/compat.go`. An incompatible pair is refused; only the override verb passes, recorded as a Warning Event. |
| Cilium | Only to the chart embedded in the running Ichor; no image-only patch. A daily CI run opens a pull request for a new patch of the pinned minor. |

The upgrade dialog and `ichor upgrade talos|kubernetes --list` group candidates as
"Recommended" (the set's version, when newer), "Patch" and "Next minor", newest
first, defaulting to the newest patch of the current minor. A typed version is
validated the same way.

## Shared by every operation

- **Cluster lock**, under the operation's identity
  (`ichor-controller/op/<namespace>/<name>`); a control-plane join or removal under
  the machine's; the Cilium Helm Job under `ichor-controller/cni-helm`. A waiting
  operation pauses with `ClusterLockHeld` naming the holder; a lease whose holder
  is gone or completed is taken over. A paused operation releases its lease and
  retakes it on resume (ADR 0162).
- A status-held state machine, `paused`, `maxUnavailable` for workers, a
  per-machine hold annotation, label-based ordering, a progress view, and no
  automatic cluster-wide rollback.
- Outstanding hand-overs are flagged before a Kubernetes upgrade (bootstrap
  manifests are re-applied).

### A failed step

The operation pauses with a `failureReason` and releases the lock.
`ichor resume operation` (or Resume) re-runs the step through an annotation, not
`spec`. A transient error (a node or the API unreachable, `etcdserver: request
timed out`; list in `internal/transient`) is retried by the controller (ADR 0174):

| | |
|---|---|
| Kinds | `talos`, `kubernetes`, `config`, `node` |
| Schedule | after 1, 2, 4, 8, 16 minutes; five attempts per step; then a person |
| State | `status.autoRetry`: step, attempts, `nextAt`; shown in the Paused message |
| Never retried | a Talos `Upgrade` call whose send status is unknown; a node reset; shutdown, rotation, etcd restore and Ichor kinds |
| Precedence | `spec.paused` is never lifted; a person's retry annotation beats the timer and resets the count |
| Auto-patch | a waiting retry is Paused, so `AutoPatchHalted` fires, and clears when it succeeds |

### Approval

An operation created paused so a person starts it carries
`ichor.maesh.dev/awaits-approval` and, while it has run no step, the Paused reason
`AwaitingApproval`. `ichor approve operation NAME` (or Approve in the UI) clears
`spec.paused`. One a person paused is resumed, not approved. A storage preset's
extension upgrade is created this way ([Storage](storage.md)).

## Talos

Change `spec.talosVersion` or the schematic; a machine upgrades when its running
version or schematic (its `ImageFactorySchematic` resource) differs. Pre-checks:
etcd health, node readiness, upgrade path, Kubernetes pairing. Control planes go
first, one at a time; workers within `maxUnavailable`. Per node: hook, Upgrade
API, reboot, verify version, readiness and etcd membership. Falling back to the
previous A/B slot is a failure.

Talos cordons, drains and uncordons in its own Upgrade sequence
(`Sequencer.Upgrade`, Talos 1.14.3), so Ichor does only the storage-evacuation
hook. `UpgradeRequest.Preserve` is deprecated and a plain upgrade never touches
etcd data. The controller never bumps the version contract; a user-started
operation's spec update does.

## Kubernetes

Change `spec.kubernetesVersion`: one minor at most, supported by the Talos and
Cilium installed. Warn about removed APIs, pre-pull images, then three stages,
each finished on every machine first (no kubelet newer than an API server):

1. Control-plane components, node by node, through machine config.
2. kube-proxy and the bootstrap manifests (rollout check skipped when kube-proxy
   is replaced).
3. Kubelets, control planes first.

Intermediate configurations are renders with kubelet and kube-proxy pinned to the
running version, never a write to `spec`. Several control planes behind one
control plane's own address warn that the endpoint is one apiserver.

## Ichor

The controller only launches the installer Job; it never upgrades itself. The Job
runs with the replaced release's RBAC, so the installer applies its own first
(cluster-scoped, plus a Role in Ichor's namespace). It snapshots the manifests,
updates CRDs (additive, never downgraded), the controller, then the UI, applies
network policies when Cilium's CRD exists, runs post-checks, and rolls back from
the snapshot on failure.

- **No tag.** The controller resolves `installerRepository:version` to a digest (or
  takes `spec.installerDigest`), verifies the release's keyless cosign signature
  offline against an embedded Sigstore root, and runs `repository@digest`. A
  failure pauses the operation. The install manifest names the controller and
  ichor-ui by digest too.
- **The only bypass** is `spec.installerSignature: skip`
  (`--skip-signature-check`), for builds nobody signs. It stays until an upgrade
  without the flag writes `required`; the Cilium Helm Job honours it meanwhile.
- **`InstallerImageUnverified`** turns True the first time an installer image (the
  upgrade's Job or the Cilium Helm Job) runs unchecked. It is sticky: only a
  person clears it, by removing it from `status.conditions`; its
  `lastTransitionTime` is the first unchecked run.
- The CLI refuses writes more than one minor from the server. ichor-ui sends
  `X-Ichor-Version`; the SPA prompts a reload when stale
  (`ui/src/lib/connect.ts`), and the top bar shows the version.

## Shutdown

A `shutdown` operation stops the cluster for planned downtime: workers first, in
parallel, drained by Talos's `Shutdown`; then every control plane and the
controller's own node last, in one forced burst, so quorum is lost only then. etcd
members are not removed, so power-on loses no data. The cluster lock refuses
other disruptive operations until it completes. Ichor cannot power machines on;
the operation completes once every machine answers on a new boot.

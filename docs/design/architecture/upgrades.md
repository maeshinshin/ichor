---
sidebar_group: "Cluster lifecycle"
sidebar_order: 17
---

# Upgrades

![How upgrades run. A Talos upgrade pre-checks, then upgrades control planes one at a time, each through the hook, the Upgrade API, a reboot and a verification, then workers within maxUnavailable. A Kubernetes upgrade runs three stages, control-plane components, kube-proxy and bootstrap manifests, then kubelets, each finished on every machine first. A failed step pauses the operation and releases the cluster lock; a transient error is retried after 1, 2, 4, 8 and 16 minutes, and anything else waits for a person to resume.](../../images/upgrades.light.drawio.svg)

*Talos, Kubernetes and Ichor are upgraded by separate operations; a failed step pauses, and only a transient one retries itself.*

## The release set

Each Ichor build embeds one set of versions, `pkg/release.Pinned`: the Talos
release and its version contract, the Kubernetes release and the Cilium chart.
It decides the versions of a first install (`ichor init`, the CLI defaults, the
UI's proposed values) and does not bound an upgrade. Talos, Kubernetes and Cilium
each upgrade on their own, to any version the rules below accept. The Versions
card shows what each runs, with no state against the set. An Ichor upgrade does
not move the cluster.

## Valid targets

A target is a final semantic version newer than the running one, and:

- **Talos**: `upgrade.TalosPath` accepts the step (machinery's rule: the target's
  minor sets a minimum running version and denies listed versions; within those
  bounds Talos may skip minors, though its docs recommend adjacent ones). Each
  machine reboots.
- **Kubernetes**: at most one minor up in the same major
  (`upgrade.KubernetesSkew`), and the cluster's Talos supports it
  (`upgrade.KubernetesPairing`).
- **Compatibility**: the Talos and Kubernetes pair, and Kubernetes and the
  installed Cilium, are checked against `pkg/release/compat.go`. An incompatible
  pair is refused; only the override verb gets past, and it is recorded as a
  Warning Event on the cluster.
- **Cilium** moves only to the chart embedded in the running Ichor. There is no
  image-only patch; a daily CI run opens a pull request when Cilium publishes a
  new patch of the pinned minor, so a patch reaches clusters with an Ichor release.

The upgrade dialog lists the controller's candidates (patches of the current
minor and the next minor) as "Recommended" (the set's version, when newer),
"Patch" and "Next minor", newest first, defaulting to the newest patch of the
current minor. A typed version is validated the same way. `ichor upgrade
talos|kubernetes --list` prints the groups.

## Shared by every operation

- The cluster lock, taken under the operation's own identity
  (`ichor-controller/op/<namespace>/<name>`); a control-plane join or removal
  uses the machine's, the Cilium Helm Job `ichor-controller/cni-helm`. Disruptive
  work never overlaps, whoever created it. A waiting one pauses with
  `ClusterLockHeld` naming the holder; a lease whose holder is gone or completed
  is taken over. An operation paused for a person releases its lease when it
  pauses and retakes it on resume (ADR 0162).
- A status-held state machine, `paused`, `maxUnavailable` for workers, a
  per-machine hold annotation, label-based ordering, a progress view, and no
  automatic cluster-wide rollback.
- Outstanding hand-overs are flagged before a Kubernetes upgrade, because the
  bootstrap manifests are re-applied.

### A failed step

The operation pauses with a `failureReason`, releases the lock, and re-runs the
step on `ichor resume operation` (or Resume in the UI), through an annotation, not
`spec`.

A transient error (a node or the API unreachable, `etcdserver: request timed out`;
the one list is `internal/transient`) is retried by the controller (ADR 0174):

- Kinds `talos`, `kubernetes`, `config` and `node`; after 1, 2, 4, 8 and 16
  minutes, five attempts per step, then a person's. `status.autoRetry` holds the
  step, attempts and `nextAt`, and the Paused message shows them.
- Never retried: a Talos `Upgrade` call whose send status is unknown, a node
  reset, and the shutdown, rotation, etcd restore and Ichor kinds.
- `spec.paused` is never lifted by a retry, and a person's retry annotation
  beats the timer and resets the count.
- A waiting retry is phase Paused, so `AutoPatchHalted` fires as for any paused
  automatic operation, and clears when the retry succeeds.

### Approval

An operation created paused so that a person starts it carries the annotation
`ichor.maesh.dev/awaits-approval` and, while it has run no step, the Paused reason
`AwaitingApproval`. `ichor approve operation NAME` (or Approve in the UI) clears
`spec.paused`. An operation a person paused is resumed, not approved. A storage
preset's extension upgrade is created this way ([Storage](storage.md)).

## Talos

Change `spec.talosVersion` or the schematic: a machine is upgraded when its
running version or schematic (from its `ImageFactorySchematic` resource) differs
from the target. Pre-checks cover etcd health, node readiness, the upgrade path
and the Kubernetes pairing. Control planes go first, one at a time; workers
follow within `maxUnavailable`. Per node: hook, Upgrade API, reboot, verify
version, readiness and etcd membership. A node that falls back to the previous
A/B slot is a failure.

Talos cordons, drains (the eviction API through the kubelet's kubeconfig) and
uncordons in its own Upgrade sequence (Talos 1.14.3: `Sequencer.Upgrade`'s
`"drain"` phase, `CordonAndDrainNode`), so Ichor does not repeat it; it does only
the storage-evacuation hook beforehand. `UpgradeRequest.Preserve` is deprecated
and the installer ignores a system-disk wipe on upgrade, so a plain upgrade
reboots in place and never touches the etcd data. The controller never bumps the
version contract itself; it changes only through the spec write of an operation
the user starts.

## Kubernetes

Change `spec.kubernetesVersion`: one minor up at most, supported by the cluster's
Talos and installed Cilium. Warn about removed APIs in use, pre-pull images, then
run three stages, each finished on every machine before the next (no kubelet is
ever newer than an API server):

1. Switch the control-plane components node by node through machine config.
2. Update kube-proxy and sync the bootstrap manifests (the rollout check is
   skipped when kube-proxy is replaced).
3. Roll the kubelets, control planes first.

The intermediate configurations are renders with the kubelet and kube-proxy
pinned to the version the node runs, never a write to `spec`. A minor upgrade of
several control planes whose endpoint is one control plane's own address warns
that the endpoint is one apiserver. This is `upgrade-k8s`'s order on Ichor's own
machinery.

## Ichor

The controller only launches the installer Job for the target version and never
upgrades itself. The Job runs with the RBAC of the release being replaced, so the
installer applies its own RBAC first (cluster-scoped, plus a Role in Ichor's
namespace). It then snapshots the current manifests, updates CRDs (additive,
never downgraded), the controller, then the UI, applies the network policies when
Cilium's CRD exists, runs post-checks, and rolls back from the snapshot on failure.

The installer image never runs by tag. Before the Job exists the controller
resolves `installerRepository:version` to a digest (or takes
`spec.installerDigest`), verifies the release workflow's keyless cosign signature
offline against an embedded Sigstore root, and runs the Job as
`repository@digest`. A failure pauses the operation. The install manifest inside
an installer names the controller and ichor-ui by digest too.

The only bypass is a recorded `spec.installerSignature: skip`
(`--skip-signature-check`) for builds nobody signs. It stays until an upgrade
without the flag writes `required` again, and the Cilium Helm Job honours it
meanwhile. The condition `InstallerImageUnverified` becomes True the first time an
installer image (the Ichor upgrade's Job or the Cilium Helm Job) runs unchecked
and is sticky: a later verified run or an upgrade that clears the skip never
removes it, its `lastTransitionTime` is the first unchecked run, and only a person
clears it, by removing it from `status.conditions`.

The CLI refuses writes when more than one minor away from the server. ichor-ui
sends `X-Ichor-Version` on every response and the SPA prompts a reload when it is
stale (`ui/src/lib/connect.ts`); the top bar shows the installed version as a
badge (`ui/src/lib/ichorVersion.ts`).

## Shutdown

A `TalosOperation` of kind `shutdown` stops the whole cluster for planned
downtime. Workers go first, in parallel, drained by Talos's own `Shutdown`
sequence; then every control plane and the controller's own node last, in one
forced burst, so quorum is lost only in that last stage and a self-managed
controller needs to live only until the requests are sent. etcd members are not
removed, so power-on loses no data. Until it completes, the cluster lock refuses
every other disruptive operation. Ichor cannot power machines on: the operation
waits and completes by itself once every machine answers on a new boot.

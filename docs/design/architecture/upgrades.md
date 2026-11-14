---
sidebar_group: "Cluster lifecycle"
sidebar_order: 17
---

# Upgrades

The release set and the upgrades (amending 0113). Each Ichor build embeds one set of versions,
`pkg/release.Pinned`: the Talos release and its version contract, the Kubernetes release, and the Cilium
chart. It decides the versions of a first install (`ichor init`, the CLI defaults and the UI's proposed
values). It does not bound an upgrade: Talos, Kubernetes and Cilium each upgrade on their own, to any
version the rules below accept, and the set is not a limit on them. The Versions card shows what each
component runs; it carries no state against the set.

An upgrade target is valid when it is a final semantic version newer than the running one, and:

- Talos: `upgrade.TalosPath` accepts the step (machinery's compatibility rule: the target's minor sets a
  minimum running version and denies listed versions; within those bounds Talos may skip minors, though its
  docs recommend adjacent minors; Talos upgrades reboot each machine).
- Kubernetes: at most one minor up within the same major (`upgrade.KubernetesSkew`, the version skew policy),
  and the cluster's Talos supports the target (`upgrade.KubernetesPairing`).
- Compatibility: the Talos and Kubernetes pair, and the Kubernetes and the installed Cilium, are checked
  against the one table `pkg/release/compat.go`. An incompatible pair is refused, and the override verb is the only way past the refusal; the override is recorded as a Warning Event on the cluster.
  Cilium moves only to the chart embedded in the running Ichor: there is no
  image-only patch, and a daily CI run opens a pull request when Cilium publishes a new patch of
  the pinned minor, so a patch reaches clusters with an Ichor release.

The upgrade dialog offers the Talos and Kubernetes targets as a searchable list of the candidates the
controller found: the patches of the current minor and the next minor, grouped "Recommended" (the set's
version, when newer), "Patch" and "Next minor", newest first, with the newest patch of the current minor as
the default. A version typed in and not in the list can still be chosen; the server validates it the same
way. `ichor upgrade talos|kubernetes --list` prints the same groups as a table (recommended, patch, next minor, the default marked). An Ichor upgrade does not move the
cluster; each component is upgraded by its own operation.

A step that fails pauses its operation with a `failureReason` and is never
retried by itself; `ichor resume operation` (or Resume in the UI) on such an
operation re-runs the failed step through an annotation, not through `spec`.

Every operation takes the cluster lock under its own identity
(`ichor-controller/op/<namespace>/<name>`), a control-plane join or removal under the
machine's own, and the Cilium Helm Job under `ichor-controller/cni-helm`, so two disruptive pieces of
work never run together, whoever created them; the one that waits pauses with
`ClusterLockHeld` naming the holder, and a lease whose holder is gone, completed or
paused on a failed step is taken over.

All three upgrade kinds share the lock, the status-held state machine, pause-
on-failure (no automatic cluster-wide rollback), `paused`, `maxUnavailable`
for workers, a per-machine hold annotation, label-based ordering, and a
progress view. Outstanding hand-overs are flagged before a Kubernetes upgrade
because of the bootstrap-manifest re-apply.

Talos: change `spec.talosVersion` or the schematic (a machine is upgraded when its running version or
running schematic, read from its `ImageFactorySchematic` resource, differs from the target). Pre-checks cover etcd
health, node readiness, the supported upgrade path and the Kubernetes pairing.
Control planes go first, one at a time; workers follow within
`maxUnavailable`. Per node: hook → Upgrade API → reboot → verify version,
readiness and etcd membership. Cordon and drain are not Ichor's to do here:
Talos's own Upgrade sequence already cordons (a controller does it reactively
once the upgrade actor starts) and drains (via the kubelet's own kubeconfig,
the standard eviction API) before it stops services and reboots, and
uncordons the same way once the upgrade clears — confirmed in Talos 1.14.3's
source (`Sequencer.Upgrade`'s `"drain"` phase, `CordonAndDrainNode`, and
`NodeApplyController.ApplyCordoned`). Doing it again on Ichor's side would
only be redundant, not more correct, and would blur which side's cordon
"owns" the uncordon afterwards; the one thing still Ichor's to do beforehand
is the storage-evacuation hook, which Talos has no notion of. No
etcd-preserving flag handling is needed either: `UpgradeRequest.Preserve` is
explicitly deprecated in that same source ("only used for legacy upgrade
path, to be removed in Talos 1.18"), and the installer ignores it in upgrade
mode ("system disk wipe on upgrade is not supported anymore, option
ignored") — a plain upgrade reboots in place and never touches the etcd data
directory, for one member or many. A node that falls back to the previous
A/B slot counts as a failure. The version contract is never bumped by the
controller on its own: it changes only through the spec write of an operation the user starts.

Kubernetes: change `spec.kubernetesVersion` to a version one minor up at most, supported by the Talos
the cluster runs and by the installed Cilium. Warn about removed APIs in use, pre-pull images, then in three
stages, each finished on every machine before the next (no kubelet is ever
newer than an API server): switch the control-plane components node by node
through machine config; update kube-proxy and sync the bootstrap manifests
(the rollout check is skipped when kube-proxy is replaced); roll the
kubelets, control planes first. The intermediate configurations are renders
with the kubelet and kube-proxy pinned to the version the node runs, never
a write to `spec`. A minor upgrade of several control planes behind an
endpoint of type `node` warns that the endpoint is one apiserver. This is
`upgrade-k8s`'s order, run on Ichor's own machinery.

Ichor: the controller only launches the installer Job for the target version
and never upgrades itself. The Job runs with the RBAC of the release being
replaced, so the installer applies its own RBAC first; its rights are
cluster-scoped, plus a Role in Ichor's namespace. It then snapshots the current
manifests, updates CRDs (additive, never downgraded), the controller, then the
UI, applies the network policies when Cilium's CRD exists, runs post-checks, and
rolls back from the snapshot on failure. The installer image never runs by tag:
before the Job exists the controller resolves `installerRepository:version` to a digest
(or takes the pin in `spec.installerDigest`), verifies the release workflow's keyless
cosign signature offline against an embedded Sigstore root, and runs the Job as
`repository@digest`; a failure pauses the operation, and the only bypass is a
recorded `spec.installerSignature: skip` (`--skip-signature-check`) for
builds nobody signs. The install manifest inside a release's installer names the
controller and ichor-ui by digest too. The CLI refuses writes when more than one minor
away from the server. A UI backend response header (`X-Ichor-Version`) that
would make a stale SPA prompt a reload is not built; the
top bar shows the installed Ichor version as a badge instead
(`ui/src/lib/ichorVersion.ts`).

Shutdown: a `TalosOperation` of kind `shutdown` stops the whole cluster for
planned downtime. Workers go first, in parallel and drained by Talos's
own `Shutdown` sequence; then every control plane, and the controller's own node
last, in one forced burst, so quorum is lost only in that last stage and a
self-managed controller needs to live only until the requests are sent. A
shutdown does not remove etcd members, so power-on loses no data. While the
operation has not completed, the cluster lock refuses every other disruptive
operation. Ichor cannot power machines on: the operation waits, and completes by
itself once every machine answers on a new boot, which lifts the refusal.

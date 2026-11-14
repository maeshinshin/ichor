# 0066. The kube-proxy switch is a TalosOperation of kind cni; Ichor changes the Talos side and removes kube-proxy, and the CNI's owner changes the CNI

Status: Accepted · Date: 2026-10-26 · Design section: CNI

Decided: Add `TalosOperation` kind `cni` whose per-machine step applies the Talos-side kube-proxy mode (rendered by `pkg/render` from `status.cni.kubeProxy`, falling back to `spec.cni.kubeProxy`); replacing waits for Cilium to serve Services before touching any node and deletes the kube-proxy objects last, enabling brings kube-proxy up and verifies it before asking Cilium to step aside; Ichor never writes the CNI's configuration (hand-over or not) and leaves node-level kube-proxy rules to the next reboot

## Context

The design (CNI) requires the `spec.cni.kubeProxy` switch to run new side up, verify, old side down, and
says Ichor must delete kube-proxy itself. ADR 0013 added the admission guard and noted that nothing executed the
sequence. ADR 0026 is the backbone (lock, status-held state machine, pause-on-failure), ADR 0057 the
Kubernetes executor, ADR 0024 the hand-over.

## Decision 1: what Talos does (Talos v1.14.3 source)

- `internal/app/machined/pkg/controllers/k8s/control_plane.go` (around line 447) copies
  `K8sProxyConfig().Enabled()` into `BootstrapManifestsConfig.ProxyEnabled`; `manifest.go` (line 238) renders the
  `10-kube-proxy` manifest only `if cfg.ProxyEnabled`: the ConfigMap `kube-proxy-<checksum>` (labels
  `k8s-app=kube-proxy`, `tier=node`, only when a proxy config is set), the DaemonSet, ServiceAccount and
  ClusterRoleBinding, all named `kube-proxy` (`k8stemplates/kube-proxy.go`). It is the same manifest path the
  Kubernetes upgrade syncs (ADR 0057).
- `manifest_apply.go` has no delete (or prune) call at all. Enabling therefore creates the DaemonSet; disabling
  only stops Talos managing it. 
- At the `v1.14` contract the setting is the `KubeProxyConfig` document's `enabled` field (default mode
  `nftables`), and the legacy `cluster.proxy` block cannot coexist with that document (validation error
  "can't be used with KubeProxyConfig document"). `pkg/typedconfig` has no `KubeProxyConfig` kind, and the Cilium
  preset's `TalosPatches` emits the legacy block, so a replaced cluster at the v1.14 contract failed to render.
  Nothing in Talos clears the rules kube-proxy programmed.

## Decision 2: what represents the switch

A `TalosOperation` of kind `cni` (enum value added). It needs the cluster lock because the Service data path
can drop during a switch, and a restart must resume from status: both are the backbone's. The per-machine step
is "apply the rendered configuration without a reboot", exactly the shape of ADR 0057, so the control planes
go one at a time and then the workers.

`pkg/render` now owns the Talos side. `status.cni.kubeProxy` (new, written only by the executor) wins over
`spec.cni.kubeProxy`; replaced renders disabled, and enabled renders an explicit flag only once a switch set the
status, so a cluster that never switched keeps its configuration hash. From the moment Ichor owns the value (the
cluster replaces kube-proxy, or a switch set the status) `cluster.proxy.disabled` from patches is dropped (as the
hand-over drops the inline manifest); an enabled cluster that never switched renders exactly as before, at the contract-appropriate form: the `KubeProxyConfig`
document from v1.14, the legacy block before. `fieldmeta` classifies the change as reboot-required, so the
machine controller refuses it on every node and only the operation applies it, one node at a time.

## Decision 3: the order

Replacing (`enabled` to `replaced`):

1. Cilium's live `kube-proxy-replacement` is true and its DaemonSet is fully rolled out. Until then no node is
   touched and no status changes.
2. `status.cni.kubeProxy` is set to replaced; each machine is switched (`enabled: false`), control planes first.
3. After every machine of the operation is done: delete the DaemonSet, ServiceAccount, ClusterRoleBinding and
      every `kube-proxy` or `kube-proxy-<checksum>` ConfigMap, by name (through the admin kubeconfig, like ADR 0057),
   then confirm the DaemonSet is gone. This cleanup always runs through a control plane's Talos client, because an
   admin kubeconfig is only available on control planes and the last machine to finish in a cluster with workers is
   a worker.
   A control plane excluded by the hold annotation fails the step instead (it would re-apply what is deleted).

Enabling (`replaced` to `enabled`):

1. `status.cni.kubeProxy` is set to enabled and the control planes are switched; Talos creates the DaemonSet.
2. When all machines are done, the kube-proxy DaemonSet must exist and be fully ready.
3. Only then does the operation wait for Cilium to be switched off (`kube-proxy-replacement` not true, agents
   rolled out). Cilium stays the owner of that change.

## Decision 4: Ichor never writes the CNI's configuration

Before the hand-over Cilium's values are an entry in a user-owned `TalosConfigPatch`, after it they are in Git
(ADR 0024); the controller never mutates `spec`. So Ichor does not edit either. The operation sets
`AwaitingCNIChange` on itself with the exact values and where they go (the inline-manifest entry before the
hand-over, the Application's Helm values in Git after), and waits up to 24 hours (a pull request may be in
review). The UI and CLI open that change (PR mode, ADR 0056); that is their work, not this executor's.

## Decision 5: how "the new side works" is judged

Replacing: `cilium-config`'s `kube-proxy-replacement` through `Preset.CheckConsistency`, plus the `cilium`
DaemonSet with `observedGeneration` current and every pod updated and ready. Enabling: the kube-proxy DaemonSet
exists with every pod updated and ready. Each wait is `VerifyingNewSide` (30 minutes); past its clock the step
fails and the operation pauses (ADR 0026), never retried automatically. A preset whose live state Ichor cannot
read (`custom`) is refused before anything changes.

`shortcut:` no probe of an actual Service connection from a pod, and no per-agent `cilium status`; the agents
rolling out on the changed ConfigMap is the proxy for it. A synthetic probe needs a workload Ichor does not run, so add one only if Ichor ever runs such a workload.

## Decision 6: node rules

Left alone. kube-proxy's rules (nftables by default) remain until the node reboots, which clears them on Talos
(ephemeral); Cilium's documented manual cleanup is `iptables-save | grep -v KUBE | iptables-restore` on each node,
which Talos has no shell for. `kube-proxy --cleanup` would need a privileged per-node Job. `shortcut:`
the stale rules are left, since Cilium's socket load balancing bypasses them; clean them up if a case appears where they are not bypassed (noted in `RemoveKubeProxy`).
Amended: after the removal the executor connects to the `kubernetes` Service's cluster IP from the controller
(`KUBERNETES_SERVICE_HOST`) and reports done only once that answers; until then it waits under the verification clock
(`VerifyingNewSide`, 30 minutes) with a message that says the old rules stay on each node until it reboots.

## Consequences

- Existing replaced clusters at the v1.14 contract render again; the legacy block in their patches is ignored.
- Applying `KubeProxyConfig.enabled` takes effect without a reboot, because the controller reads the config live
  (as ADR 0057 does for images). The DaemonSet does not reappear by itself when enabling: see the 2026-10-28
  amendment.
- The operation is created by hand (or by the UI/CLI); the admission guard (ADR 0013) still needs the
  annotation on the spec change. `ichor set kube-proxy <mode>` confirms before starting: it states that the
  configuration is applied without a reboot while the kubelet and control plane components restart on each machine,
  and the number of machines.

## Amendment: one-shot, Git-friendly confirmation

On an Argo CD-tracked cluster the Git change that switches `spec.cni.kubeProxy` was rejected by the admission policy (ADR 0013) because the confirmation annotation was missing, so the annotation had to be committed into Git, where it stayed as a standing consent. The annotation value now names the target mode (`replaced` or `enabled`) and the policy admits a switch only when the value equals the new `spec.cni.kubeProxy`. A leftover annotation therefore cannot admit a different later switch, and it is safe to commit for the one switch. The server sets it to the target for the one write it makes and removes it again (it used to write an actor name). For a tracked cluster `ichor set kube-proxy <mode>` prints the exact YAML to commit (spec field plus annotation) and says to run the same command again, which finds the spec already changed and starts the operation; the old text said "start the operation without a target", which was wrong.

## Amendment (2026-10-28): Talos never recreates kube-proxy after Ichor removed it; Ichor restores it when enabling

When enabling (`replaced` to `enabled`), the configuration applies without a reboot
(`cluster.proxy.disabled: false`), but the kube-proxy DaemonSet is never created and the operation sits in
`VerifyingNewSide` ("the kube-proxy DaemonSet has not been created yet"). The
`talos-bootstrap-manifests-inventory` ConfigMap in kube-system still listed the old kube-proxy objects.

Cause, in Talos v1.14.3 `internal/app/machined/pkg/controllers/k8s/manifest_apply.go`
(`ManifestApplyController.apply`): for every object of the rendered manifests it does
`if inv.Contains(objMeta) { continue }` before it looks at the cluster, and it never removes an entry from the
inventory for an object that disappeared from the cluster (only the v1.13 namespaced-cluster-wide migration
removes entries). The controller also only runs when its inputs change (the Kubernetes secrets, the
`k8s.Manifest` resources, the etcd service), and it only creates, never deletes. So an object Talos applied once,
which Ichor deleted when it replaced kube-proxy, is skipped forever: the apply is not tied to a node being the
bootstrap node, nor to a kubelet or apid event; the inventory alone decides. (`ManifestController` does render
`10-kube-proxy` again once `cfg.ProxyEnabled` is true; it is the apply that skips it.)

Decision: when enabling, and only while the kube-proxy DaemonSet is missing, the cni executor restores the
objects itself (`KubeProxyAdmin.RestoreKubeProxy`). It reads the `k8s.Manifest` resources Talos rendered on a
control plane (`TalosClient.BootstrapManifests`, the `talosctl get manifests` equivalent), keeps only the
kube-proxy objects (the DaemonSet, ServiceAccount and the `kube-proxy` or `kube-proxy-<checksum>` ConfigMaps in
kube-system, the ClusterRoleBinding `kube-proxy`) and server-side-applies them under Talos's own field manager,
through the admin kubeconfig a control plane issues (a worker's step goes through a control plane's client, as
for the removal). It is idempotent, bounded by the existing 30-minute `VerifyingNewSide` clock, and runs again
on each requeue only while the DaemonSet is absent; if Talos has not rendered the manifest yet it applies
nothing and the wait continues. This restores Talos's own component from Talos's own rendering; the inventory is
left as it is (editing Talos's bookkeeping would not wake its controller, and the entries are correct again once
the objects exist). Rejected: editing the inventory ConfigMap, because Talos would still need an input event to
re-run the apply.

## Amendment (2026-10-28): safe unrelated pending paths ride along with the switch

The executor refused any pending path outside `KubeProxyConfig` / `cluster.proxy`, which paused a
switch with no way out when a safe leftover (the CNI's `cluster.inlineManifests` removal, ADR 0024
amendment) was pending. It now classifies each unrelated path with `fieldmeta.Assess` and applies
those that are safe and need no reboot together with the switch. Unrelated reboot-requiring,
install-time-only, API-server-restarting or version-unsupported paths are still refused with the
"converge it first" error.

## Amendment (2026-10-28): a spec-first change reports KubeProxySwitchPending, not RebootRequired

On a Git-tracked cluster the user commits `spec.cni.kubeProxy: replaced` (and the consent annotation) before
running `ichor set kube-proxy replaced`. Until the switch sets `status.cni.kubeProxy` the render follows spec, so the
control plane differed from its node at `KubeProxyConfig.enabled` and showed `Ready=False/RebootRequired` with
"start a config rollout". That points at the wrong action: `ichor rollout config` would reboot the node onto the new
mode and skip the switch's checks. The reverse direction never showed it because status already overrides the render.

Chosen: report it differently, keep the render. The alternative, holding the render at the current mode, needs a
"current mode" when `status.cni.kubeProxy` is unset, and nothing records one: a fresh cluster created `replaced`
and a running cluster edited spec-first look the same from the TalosCluster, and the fresh one must render from spec.
Guessing from the machine's applied hash would make the render depend on node history.

- The signal that a switch is pending, in either direction, is the consent annotation
  (`ichor.maesh.dev/confirm-kube-proxy-switch`) naming the spec's mode while `status.cni.kubeProxy` is unset or
  differs. Spec alone cannot tell a switch from a patch-made `cluster.proxy.disabled` change, and keying on "spec says
  replaced, no status" would miss the reverse (a cluster created `replaced`, edited spec-first to `enabled`). The admission rule already requires the annotation for any switch, so Git-first commits carry it.
- While that holds, a TalosMachine whose only reboot-class paths are
  `KubeProxyConfig*` / `cluster.proxy*` (and no API server restart on a control plane) reports
  `Ready=False/KubeProxySwitchPending`: "run `ichor set kube-proxy replaced` (or the UI's kube-proxy switch) to apply
  it without a reboot". It does not set `status.rebootPaths`, so `ichor rollout config` and the rollout planner (which
  select on those paths) do not pick the machine up.
- A machine that also has other reboot-class paths keeps `RebootRequired` and is selectable, so the rollout step
  (`configRolloutAdvancer.begin`) refuses any machine with a kube-proxy path pending in this state ("run
  `ichor set kube-proxy <mode>` first"). A rollout therefore never reboots a node onto a kube-proxy mode, bypassing the
  switch's Cilium check and ordering. Once the switch sets the status the guard no longer applies.
- Without the annotation (a patch edit of `cluster.proxy.disabled`, an annotation for the other mode, or a finished
  switch whose status equals the spec) nothing changes: the change stays `RebootRequired` as before.

## Amendment (2026-10-29): only the operation changes Cilium's kube-proxy mode; the consent annotation is removed after the operation exists

Writing `spec.cni.kubeProxy=replaced` alone made
Cilium run `kube-proxy-replacement: true` while kube-proxy still ran, with no operation and no guard. Cause: the
hand-over reconciler (ADR 0099) hashed the effective values with `spec.cni.kubeProxy`, which derive
`kubeProxyReplacement` and `k8sService*`, saw a new hash and ran the `ichor-cni` Job at once. The operation then
found nothing to wait for and completed in about a second.

- The hand-over reconciler, the upgrade operation and every other Cilium Helm Job outside a switch now take the mode
  Cilium runs now (`currentKubeProxyMode`): `cilium-config`'s `kube-proxy-replacement`; when there is no
  `cilium-config` yet, `status.cni.kubeProxy`, then the spec (nothing was switched, the bootstrap rendered it). They
  never read the spec's mode for a changed value. `status.cni.kubeProxy` alone is not the signal, because enabling sets
  it first (kube-proxy must come back before Cilium steps aside), so the reconciler could otherwise run the Cilium
  Job ahead of kube-proxy; `cilium-config` is only ever changed by the switch's own Job.
- Only the switch executor applies the target mode: `requireCilium` passes it to `AdvanceCNIHelmJob`, which names the
  Job by the hash of the target values and gives it `--kube-proxy=<mode>` (`ichor cni helm apply` reads the mode
  from the flag, not the spec). Both Cilium-first (replaced) and kube-proxy-first (enabled) ordering therefore run
  under the operation's lock and verification, and the `VerifyingNewSide` wait shows while the Job runs.
- No fight: once the switch has changed `cilium-config` and `status.cni.kubeProxy`, the reconciler's hash is the
  target mode's, equal to the switch Job's, so it adopts that Job and records `appliedValuesHash`; it never runs a
  Job that reverts it. While the operation holds the cluster lock the reconciler waits anyway.
- The consent annotation is removed by the server after the operation is created, not before: the spec write
  (with the annotation) is retried on a conflict with a fresh read, then the operation is created, then the annotation
  is removed (also retried). A conflict can no longer leave a changed mode with no operation. The removal happens for
  every `cni` start, including one whose spec was already changed (a retry, or a Git-first change that carried the
  annotation), which is the case where it used to be left behind.

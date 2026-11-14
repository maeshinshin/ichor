# 0057. The Kubernetes upgrade applies the rendered configuration node by node; talosctl's upgrade-k8s is not reused whole

Status: Accepted · Date: 2026-10-26 · Design section: Upgrades
Decided: Execute `kind: Kubernetes` as a per-node AdvanceMachineFunc that applies the freshly rendered machine configuration without a reboot (only the component image fields may differ), pre-pulls the images, verifies the node from the Kubernetes API, and syncs the bootstrap manifests once the last control plane runs the target; it refuses a skipped minor, an unsupported Talos pairing and an unfinished CNI hand-over before touching a node, and only warns about removed APIs in use

## Context

The design's "Upgrades / Kubernetes" lists: one minor at a time, warn about removed APIs, pre-pull images,
switch the control-plane components node by node through machine config, update kube-proxy (skipped when
replaced), roll the kubelets, sync the bootstrap manifests, flag outstanding hand-overs first, and says reusing
`talosctl upgrade-k8s` is the first thing to evaluate. ADR 0026 supplies the lock, the status-held state machine,
pause on failure, `maxUnavailable`, hold annotations and ordering; ADR 0043 is the Talos-kind executor this
follows.

## Evaluating `upgrade-k8s` (Talos v1.14.3 source)

- `cmd/talosctl/cmd/talos/upgrade-k8s.go` builds a `kubernetes.UpgradeOptions` and calls
  `pkg/cluster/kubernetes.Upgrade` (`talos_managed.go`). That package is public, but `Upgrade` is one blocking
  call over every node: it takes an `UpgradeProvider` (a talosctl cluster client with a kubeconfig), loops
  `upgradeStaticPod` over all control planes per component, then kube-proxy, then kubelets, then
  `PerformManifestsSync`, and keeps its progress in goroutine and watch state. It cannot resume after a crash
  from `TalosOperation` status, and it cannot be split into the backbone's per-machine steps.
- It changes configuration with `patchNodeConfig` (`patch.go`): read the node's active config, patch an image
  field, `ApplyConfiguration` in `NO_REBOOT` mode. Ichor's controller renders each machine's whole configuration
  from the CRs and treats a node that differs as drift (design step 5), so a patch made behind its back would be
  contradicted by the next reconcile. Two findings from there carry over: every Kubernetes component image
  change applies with no reboot (the same call, `NO_REBOOT`, for apiserver, controller-manager, scheduler,
  kube-proxy and kubelet), and the pre-pull uses the `ImagePull` RPC in the CRI namespace for the static pods
  and the system namespace for the kubelet (`prePullImages`).
- The removed-API pre-check (`go-kubernetes/kubernetes/upgrade/checks.go`, a different module, ADR 0022) keeps
  a per-minor table of removed flags, feature gates and API resources that someone has to extend for every
  Kubernetes release. Most of what it checks (flags and gates on the static pods) cannot be set through Ichor's
  typed configuration in the first place.
- `go-kubernetes`'s `ssa.Manager` and `manifests` are reusable as a library, but adding the module moves
  `k8s.io/api`, `apimachinery`, `client-go`, `cli-runtime`, `component-base`, `kubectl` from v0.36.0 to v0.36.2
  for every package in the repository, to save about forty lines.

So: not reused as a whole, and not as a dependency. What is reused is what it proves: the apply mode, the
pre-pull RPC and namespaces, the order (control planes, then workers) and the bootstrap-manifest sync as the last
cluster-wide step.

## Decision

**The per-node switch is "apply the rendered configuration".** `pkg/render` already derives every component image
from `spec.kubernetesVersion`; for a control plane the diff against the node is exactly
`KubeAPIServerConfig.image`, `KubeControllerManagerConfig.image`, `KubeSchedulerConfig.image`,
`KubeProxyConfig.image` and `KubeletConfig.image` (a worker: `KubeletConfig.image`), found by
`ChangedPaths` on a render at two versions. Nothing hand-writes a component switch.

The existing apply path cannot do it by itself, and that is useful. `pkg/fieldmeta` has no rule for these
paths (it only knows the legacy `cluster.apiServer.*` prefixes, and `cluster.proxy` and the kubelet are
deliberately absent), so `TalosMachineReconciler` classifies a version change as reboot-required and refuses it
on every node (`RebootRequired`). That keeps the roll gated by the operation: no node changes until the
executor's turn, one control plane at a time. The executor applies with `NO_REBOOT` itself, accepting a changed
path only if it is one of the image paths above or `fieldmeta.Assess` says it is reboot-free and not
install-time-only; any other pending change fails the step ("unrelated pending configuration change") instead of
riding along. `fieldmeta` is not edited: a rule there would let the machine controller apply the roll on every
node at once.

**Per call, everything is re-derived** (the ADR 0043 discipline). If the node's configuration hash differs from
the render, the step has not been applied: pre-checks, dry run, pre-pull of the changed non-proxy images, apply,
and the `KubernetesUpgrade` condition (a resume marker and the verification clock, not a source of truth). If
they are equal it only verifies: the Node's kubelet version and Ready, and for a control plane its three static
pods (mirror pods in `kube-system`, found by `k8s-app` label and node) on the target image tag and Ready. Not
yet true is "still settling"; past 20 minutes from the marker the step errors and ADR 0026 pauses the operation.

**Pre-checks** (before any node is changed; failures pause with a message):

1. `upgrade.KubernetesSkew(node's kubelet version, target)`: first caller. Judged per node against what that node
   actually runs, so a skipped minor, a downgrade, or a node left behind is refused.
2. `upgrade.KubernetesPairing(target, the node's running Talos version)`.
3. The node is Ready.
4. **CNI hand-over: block, as an error.** The bootstrap-manifest sync re-applies the cluster's inline manifests;
   while Argo CD is taking the CNI over and Ichor still renders the inline entry (the `CNIHandover` condition is
   not True, ADR 0024), that rolls the CNI back, which can take the network down: not something to warn about and
   continue. Blocked: preset with a hand-over (cilium, via the same `cniPresetFor`/`ArgoCDApplication` decision
   the hand-over controller makes), `handover` automatic or manual, condition not True. Allowed: no CNI hand-over
   (flannel, custom), condition True, and `handover: disabled` (nothing ever takes over, so the inline manifest
   is the source of truth and re-applying it is the intended state). It is an error rather than a wait so a
   blocked operation does not hold the cluster lock indefinitely; the operator finishes the hand-over (or sets
   `disabled` on a cluster without Argo CD) and creates the operation again.
5. **Removed APIs: warn, not gate.** The API server's own `apiserver_requested_deprecated_apis` metric reports,
   per requested API version, the release that removes it (`pkg/upgrade.RemovedAPIsInUse`), so there is no table
   to maintain. APIs removed by the target minor or earlier that something requested are recorded in the
   `RemovedAPIsInUse` condition on the TalosOperation; a failed check is recorded as Unknown. Limits: it sees
   only APIs requested since that API server started, and one API server answers the scrape.

**Bootstrap manifests and kube-proxy** run once, in the control plane whose verification finds every
`kube-apiserver` pod on the target and Ready (observed state, no control-plane list): `KubernetesAdmin.SyncBootstrapManifests` server-side-applies the
node's `k8s.Manifest` resources under Talos's own field manager (`talos`) through an admin kubeconfig from the
Talos API. The controller's own ServiceAccount therefore gets no write access to ClusterRoles, DaemonSets or
CRDs; it gains `get` on DaemonSets, and `get;list` on Pods and Nodes it already had. When `spec.cni.kubeProxy` is
`replaced` nothing about kube-proxy is checked (no DaemonSet is shipped); otherwise the step waits for the
`kube-proxy` DaemonSet to carry the target and finish rolling out. This is before the workers, not after the
kubelets as in `upgrade-k8s`; kube-proxy may be newer than a kubelet but not than the API server. `shortcut:`
no pruning and no inventory, unlike `ssa.Manager`, so an object a later Talos stops shipping stays until removed; add an inventory if that matters.

**Component image tags.** `spec.kubernetesVersion` is `vX.Y.Z` (the CRD pattern) but `generate.NewInput`
expects it without the `v` and adds its own, so `pkg/render` trims the `v` before the call; otherwise every
component image would render as `...:vv1.36.0`.

**Deadlines.** Each Talos call in the step has its own deadline, not the 15 s `etcdDialTimeout`. The apply of
a control-plane image change blocks while Talos restarts the static pods, so it is bounded by
`upgradeCallTimeout`, the same shape as ADR 0043. The same holds for the kube-proxy switch and CA rotation
applies.

**Confirmation.** `ichor upgrade kubernetes` states the cluster, the from and to versions, the machine count and
the order (control planes one at a time, then workers, at most `maxUnavailable` at once) before writing anything;
declining writes nothing.

## Consequences

- `TalosClient` gains `PullImage`, `Kubeconfig` and `BootstrapManifests`; `KubernetesAdmin` is the seam for the
  two cluster-wide actions, with a fake in the tests.
- `upgrade.KubernetesSkew` has its first caller; `KubernetesMinorOf` and `RemovedAPIsInUse` are new.
- `pkg/fieldmeta` still lacks reboot rules for the multi-document Kubernetes component kinds. Any machine whose
  rendered config changes one of those kinds outside an operation is reported `RebootRequired`; left as is on
  purpose (above).
- Amended: every kubelet roll (the Kubelets stage, ADR 0087) now begins storage evacuation, cordons and drains the
  node with the config rollout's helpers, rolls the kubelet, waits for Ready, uncordons and ends the evacuation; a
  failure pauses and ends the evacuation too. The controller's own node goes last in the Kubelets stage
  (`holdOwnNodeLast`, as for config rollouts) and is drained and evacuated like the rest; its eviction restarts the
  controller, and the step resumes from the node's state.
- Deferred: the `Ichor` kind is still unwired.

## Later changes

- The executors write the machine condition through a re-read with retry on conflict (`setMachineCondition`),
  so the machine reconciler writing the same status during the long calls does not pause the first machine.
- Applying one node's control-plane images and kubelet together would leave the first node's kubelet newer than
  the apiservers it reaches through the endpoint on a minor upgrade. ADR 0087 therefore runs the executor in four
  stages (apiservers, then controller-manager and scheduler, then kube-proxy and the bootstrap-manifest sync, then
  kubelets), so the manifest sync does not wait for "the last control plane" and no kubelet is ever newer than an
  API server.
- Amendment (ADR 0113): the target of a Kubernetes upgrade is the release set's Kubernetes, and other targets are refused before anything is written. The rollout runs this step after the Talos step, and after any configuration step the executor needs for an unrelated pending change. The executor's pairing and skew checks are unchanged.

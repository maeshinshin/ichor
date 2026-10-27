# 0087. The Kubernetes upgrade runs in three stages: control-plane components, then kube-proxy, then kubelets

Status: Accepted · Date: 2026-10-27 · Design section: Upgrades

Decided: the `kubernetes` executor visits the machines three times, recorded in `status.kubernetes.stage` (`ControlPlanes`, `KubeProxy`, `Kubelets`): (1) each control plane, one at a time, gets the rendered configuration with apiserver, controller-manager and scheduler at the target and its kubelet and kube-proxy images pinned to the version its node runs; (2) each control plane gets kube-proxy at the target, then the bootstrap manifests are synced from it and the DaemonSet's rollout awaited; (3) each control plane, then workers within `maxUnavailable`, get the full rendered configuration, which is the cluster's desired state; `pkg/render` takes the pin as a system-layer input (`SystemInputs.Staged`) that only the executor sets, nothing is written to `spec`, and `status.machines` is cleared at each stage change; the plan names the stages and warns (does not refuse) about a minor upgrade of several control planes behind an endpoint of type `node`. Amended after hardware (see the end): the first stage is split into `APIServers` (kube-apiserver on every control plane) and `ControlPlaneComponents` (controller-manager and scheduler), so there are four stages and the old `ControlPlanes` stage resumes as `APIServers`

**Amended (hardware, v1.36.5 to v1.37.1): four stages, not three.** The first stage below, `ControlPlanes`, is now two: `APIServers` (kube-apiserver on every control plane, one at a time) then `ControlPlaneComponents` (controller-manager and scheduler, one control plane at a time). See "Amendment: apiservers first" at the end; where this text says three stages and `ControlPlanes`, read that.

## Context

ADR 0057 applied a node's control-plane images and its kubelet in one apply, one node at a time. On a minor upgrade the first node's kubelet and kube-proxy were the new minor while the other control planes' API servers were still the old one, which breaks the Kubernetes version-skew policy (a kubelet may not be newer than the API server it talks to). On hardware, v1.36.5 to v1.37.1 behind a single-node endpoint left the upgraded node's components unready and the executor paused after 20 minutes. `talosctl upgrade-k8s` (Talos v1.14.3, `pkg/cluster/kubernetes/talos_managed.go`, `Upgrade`) orders it differently: the three static-pod components on every control plane, then kube-proxy, then the kubelets, then the manifest sync.

## Decision

**Stages.** The backbone (ADR 0026) is unchanged in what it guarantees (lock, pause on failure, `maxUnavailable`, hold annotation, order label, per-machine status). For this kind `kubernetesBatch` picks the machines for the current stage and, when a stage has none left, moves to the next one and clears `status.machines` in the same status write, so a restarted controller resumes from the stage and the machines recorded in it.

| Stage | Machines | Rendered configuration | Verified |
|---|---|---|---|
| `ControlPlanes` | control planes, one at a time | components at the target; kubelet and kube-proxy pinned to the node's kubelet version | node Ready, three static pods Ready on the target |
| `KubeProxy` | control planes, one at a time | components and kube-proxy at the target; kubelet pinned | the same, then `SyncBootstrapManifests` from this node and the `kube-proxy` DaemonSet rolled out (skipped when kube-proxy is replaced) |
| `Kubelets` | control planes one at a time, then workers within `maxUnavailable` | the cluster's desired state (no pin) | kubelet on the target, node Ready, static pods on the target |

So at no step is any kubelet newer than any API server, and the node's final configuration is what `TalosMachineReconciler` renders, so it sees no drift afterwards. A patch upgrade takes the same path (one code path; the intermediate states are harmless). A held control plane is excluded from every stage.

**The intermediate render.** `render.SystemInputs.Staged` (`KubeletVersion`, `KubeProxyVersion`) is applied below the patch layers with machinery's own `stdpatches.WithKubeletImage` and `WithKubeProxyImage`, so the contract-dependent form (the `KubeletConfig` and `KubeProxyConfig` documents from v1.14, the legacy fields before) is machinery's choice, and a patch that sets an image itself still wins. A worker gets no kube-proxy pin (it has no such document). The pinned version is not stored: until the `Kubelets` stage the kubelet of a node has not been touched, so the executor reads it from the node's `Node.status.nodeInfo.kubeletVersion` on every call and gets the same answer. An operation with no recorded stage, or one that started before stages existed and already has machines recorded, is taken as being in `Kubelets` (the whole configuration), which is also what unit tests that pass no operation get.

**Everything is still re-derived.** In each stage a call compares the node's configuration hash with the render for that stage: different means pre-checks, dry run, pre-pull of the changed images, apply; equal means only verify. The images that change are the stage's own, so the kubelet image is pulled in the `Kubelets` stage only. The skew, Talos pairing, Ready and CNI hand-over checks run at the start of every stage's apply.

**kube-proxy and the manifest sync move to the middle stage.** ADR 0057 synced from the last control plane. Now the sync runs in the `KubeProxy` stage, from each control plane once it has the target kube-proxy image (server-side apply is idempotent, so repeating it is harmless); the manifests are generated by the node being asked, which is why each control plane is first given the image. `upgrade-k8s` syncs after the kubelets; Ichor does it before them because a kube-proxy newer than a kubelet is inside the skew policy and the rollout is verified before any kubelet moves. The mode is read with `render.KubeProxyMode`, so a switch recorded in `status.cni.kubeProxy` is honoured.

**Plan.** `PlanOperation` returns the three stages in `stages` and the CLI prints them ("no kubelet is ever newer than an API server"). For a minor upgrade of a cluster with more than one approved control plane whose `spec.endpoint.type` is `node`, the plan carries a `warning`: the endpoint is a single apiserver, so the whole cluster loses the API while that node's apiserver is replaced; use a virtual IP or a load balancer. It is not a refusal. Only a move known to stay within one minor is silent; an unknown target (the spec already says it) warns.

## Consequences

- Each control plane receives up to three applies instead of one, and each is an apply without a reboot; the wall-clock time of an upgrade grows by the second and third control-plane pass. Workers still get one.
- `TalosOperation.status.kubernetes.stage` is a new status field. The UI shows the plan's stages with its existing "passes" wording (written for the CA rotation) and does not show the current stage; a follow-up.
- A single control plane behind any endpoint is staged too: its apiserver restarts first, the kubelet later, with the API unreachable only while the apiserver restarts. That is the case that failed on hardware; this change has not been run there.
- With an endpoint of type `node` and several control planes, the API stays unreachable for clients using that node's address while its apiserver restarts, in every stage that restarts it; staging does not change that, which is why the plan warns.
- Not verified against a real cluster. Tests use fakes and envtest: they assert the apply order and the rendered versions of each step, that no kubelet is newer than any apiserver after any apply, resume by a fresh reconciler on every pass, and pause on a failed apply in each stage.

## Amendment: apiservers first

**What happened.** On hardware (3 control planes and 1 worker, Talos v1.14.3, v1.36.5 to v1.37.1) the `ControlPlanes` stage moved the first control plane's apiserver, controller-manager and scheduler together. The v1.37.1 apiserver and controller-manager became Ready; the v1.37.1 scheduler never did (readiness 500 for 20 minutes): `failed to list *v1.DeviceTaintRule: the server could not find the requested resource`. The v1.37 scheduler needs `resource.k8s.io/v1` DeviceTaintRule, which only v1.37 apiservers serve, and the other two apiservers were still v1.36.5. The executor waited for all three pods and paused after 20 minutes.

**Why the scheduler saw old apiservers.** Talos writes the controller-manager and scheduler kubeconfigs with the secrets controller's `LocalEndpoint` (`internal/app/machined/pkg/controllers/secrets/root.go:118-128`, `kubernetes.go:136,157`): with KubePrism enabled, which is the default, that is `https://127.0.0.1:7445`, a local load balancer over every apiserver in the cluster; without it, `https://localhost:<apiserver port>`, the node's own apiserver. So a new scheduler or controller-manager may talk to any apiserver, not only the one next to it, and cannot be assumed to find the APIs it needs until every apiserver is new.

**What `talosctl upgrade-k8s` does** (Talos v1.14.3, `pkg/cluster/kubernetes/talos_managed.go`): `Upgrade` (line 150) loops over the services `kube-apiserver`, `kube-controller-manager`, `kube-scheduler` in that order, and for each service `upgradeStaticPod` (line 221) visits every control plane node in turn and waits for that node's pod before the next. So all apiservers move first, then all controller-managers, then all schedulers; then kube-proxy on every control plane (line 156), the kubelets (line 160), and the manifest sync (line 168). Ichor already follows that for kube-proxy and the kubelets; it now follows it for the apiserver.

**Decision.** The first stage is split:

| Stage | Machines | Rendered configuration | Verified |
|---|---|---|---|
| `APIServers` | control planes, one at a time | apiserver at the target; controller-manager, scheduler, kubelet and kube-proxy pinned to the version the node runs | node Ready, that node's kube-apiserver Ready on the target |
| `ControlPlaneComponents` | control planes, one at a time | apiserver, controller-manager and scheduler at the target; kubelet and kube-proxy pinned | node Ready, three static pods Ready on the target |
| `KubeProxy` | unchanged | unchanged | unchanged |
| `Kubelets` | unchanged | unchanged | unchanged |

So at no step is a controller-manager, scheduler or kubelet newer than any apiserver. Controller-manager and scheduler are done together per control plane (`upgrade-k8s` does all controller-managers, then all schedulers); the stricter rule that matters, no component newer than any apiserver, holds either way, and this keeps the number of applies per control plane at four.

**Render.** `render.StagedKubernetes` gains `ControllerManagerVersion` and `SchedulerVersion`, applied with machinery's `WithKubeControllerManagerImage` and `WithKubeSchedulerImage` below the patch layers like the other pins (none on a worker). The pin is the node's `Node.status.nodeInfo.kubeletVersion`, read on every call: a node's controller-manager and scheduler run the version its kubelet does until their stage, and the kubelet is untouched until the last one, so the answer is the same on every call and nothing is stored.

**Resume.** `status.kubernetes.stage` takes the two new values; the CRD enum keeps `ControlPlanes`. An operation recorded in `ControlPlanes` (started before this change) continues as `APIServers`, keeping `status.machines`. A control plane it had finished has all three components on the target; its `APIServers` step is skipped, and in `ControlPlaneComponents` its configuration already equals the render, so the call only verifies. A control plane that had the combined configuration applied but not yet finished is re-rendered for `APIServers`, which pins its controller-manager and scheduler back to the old version; the apply is a downgrade of those two for as long as the apiservers are still moving, and they move again in `ControlPlaneComponents`. Nothing else could be done for it without reading what the node's pods run, and that state is the one the stage exists to avoid. The UI counts `ControlPlanes` as stage 1.

**Plan and UI.** The plan lists four stages; the UI shows "Stage N of 4".

**Not verified on hardware.** Tests (fakes and envtest) assert the apply order `APIServers` cp1 cp2 cp3, `ControlPlaneComponents` cp1 cp2 cp3, `KubeProxy`, `Kubelets`, and after every apply that no controller-manager or scheduler is newer than any apiserver and no kubelet is newer than any apiserver.

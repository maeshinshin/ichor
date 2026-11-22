# 0087. The Kubernetes upgrade runs in three stages: control-plane components, then kube-proxy, then kubelets

Status: Accepted, amended · Date: 2026-10-27 · Design section: Upgrades

Decided: the `kubernetes` executor visits the machines in four stages, recorded in `status.kubernetes.stage`: (1) `APIServers` — each control plane, one at a time, gets kube-apiserver at the target with controller-manager, scheduler, kubelet and kube-proxy pinned to the version its node runs; (2) `ControlPlaneComponents` — each control plane gets controller-manager and scheduler at the target; (3) `KubeProxy` — each control plane gets kube-proxy at the target, then the bootstrap manifests are synced and the DaemonSet's rollout awaited; (4) `Kubelets` — control planes, then workers within `maxUnavailable`, get the full rendered configuration, which is the cluster's desired state; `pkg/render` takes the pins as a system-layer input (`SystemInputs.Staged`) that only the executor sets, nothing is written to `spec`, `status.machines` is cleared at each stage change, an operation with no recorded stage starts at `APIServers`, and the plan names the stages and warns (does not refuse) about a minor upgrade of several control planes behind an endpoint of type `node`

**Amended: four stages, not three.** The first stage below, `ControlPlanes`, is now two: `APIServers` (kube-apiserver on every control plane, one at a time) then `ControlPlaneComponents` (controller-manager and scheduler, one control plane at a time). See "Amendment: apiservers first" at the end; where this text says three stages and `ControlPlanes`, read that.

## Context

ADR 0057 applied a node's control-plane images and its kubelet in one apply, one node at a time. On a minor upgrade the first node's kubelet and kube-proxy were the new minor while the other control planes' API servers were still the old one, which breaks the Kubernetes version-skew policy (a kubelet may not be newer than the API server it talks to). Moving a minor upgrade's components in one apply (v1.36.5 to v1.37.1 behind a single-node endpoint) leaves the upgraded node's components unready, and the executor pauses after 20 minutes (see "Amendment: apiservers first"). `talosctl upgrade-k8s` (Talos v1.14.3, `pkg/cluster/kubernetes/talos_managed.go`, `Upgrade`) orders it differently: the three static-pod components on every control plane, then kube-proxy, then the kubelets, then the manifest sync.

## Decision

**Stages.** The backbone (ADR 0026) is unchanged in what it guarantees (lock, pause on failure, `maxUnavailable`, hold annotation, order label, per-machine status). For this kind `kubernetesBatch` picks the machines for the current stage and, when a stage has none left, moves to the next one and clears `status.machines` in the same status write, so a restarted controller resumes from the stage and the machines recorded in it.

| Stage | Machines | Rendered configuration | Verified |
|---|---|---|---|
| `ControlPlanes` | control planes, one at a time | components at the target; kubelet and kube-proxy pinned to the node's kubelet version | node Ready, three static pods Ready on the target |
| `KubeProxy` | control planes, one at a time | components and kube-proxy at the target; kubelet pinned | the same, then `SyncBootstrapManifests` from this node and the `kube-proxy` DaemonSet rolled out (skipped when kube-proxy is replaced) |
| `Kubelets` | control planes one at a time, then workers within `maxUnavailable` | the cluster's desired state (no pin) | kubelet on the target, node Ready, static pods on the target |

So at no step is any kubelet newer than any API server, and the node's final configuration is what `TalosMachineReconciler` renders, so it sees no drift afterwards. A patch upgrade takes the same path (one code path; the intermediate states are harmless). A held control plane is excluded from every stage.

**The intermediate render.** `render.SystemInputs.Staged` (`KubeletVersion`, `KubeProxyVersion`) is applied below the patch layers with machinery's own `stdpatches.WithKubeletImage` and `WithKubeProxyImage`, so the contract-dependent form (the `KubeletConfig` and `KubeProxyConfig` documents from v1.14, the v1alpha1 fields before) is machinery's choice, and a patch that sets an image itself still wins. A worker gets no kube-proxy pin (it has no such document). The pinned version is not stored: until the `Kubelets` stage the kubelet of a node has not been touched, so the executor reads it from the node's `Node.status.nodeInfo.kubeletVersion` on every call and gets the same answer.

**Everything is still re-derived.** In each stage a call compares the node's configuration hash with the render for that stage: different means pre-checks, dry run, pre-pull of the changed images, apply; equal means only verify. The images that change are the stage's own, so the kubelet image is pulled in the `Kubelets` stage only. The skew, Talos pairing, Ready and CNI hand-over checks run at the start of every stage's apply.

**kube-proxy and the manifest sync move to the middle stage.** ADR 0057 synced from the last control plane. Now the sync runs in the `KubeProxy` stage, from each control plane once it has the target kube-proxy image (server-side apply is idempotent, so repeating it is harmless); the manifests are generated by the node being asked, which is why each control plane is first given the image. `upgrade-k8s` syncs after the kubelets; Ichor does it before them because a kube-proxy newer than a kubelet is inside the skew policy and the rollout is verified before any kubelet moves. The mode is read with `render.KubeProxyMode`, so a switch recorded in `status.cni.kubeProxy` is honoured.

**Plan.** `PlanOperation` returns the three stages in `stages` and the CLI prints them ("no kubelet is ever newer than an API server"). For a minor upgrade of a cluster with more than one approved control plane whose `spec.endpoint.type` is `node`, the plan carries a `warning`: the endpoint is a single apiserver, so the whole cluster loses the API while that node's apiserver is replaced; use a virtual IP or a load balancer. It is not a refusal. Only a move known to stay within one minor is silent; an unknown target (the spec already says it) warns.

## Consequences

- Each control plane receives up to three applies instead of one, and each is an apply without a reboot; the wall-clock time of an upgrade grows by the second and third control-plane pass. Workers still get one.
- `TalosOperation.status.kubernetes.stage` is a new status field. The UI shows the plan's stages with its existing "passes" wording (written for the CA rotation) and does not show the current stage; a follow-up.
- A single control plane behind any endpoint is staged too: its apiserver restarts first, the kubelet later, with the API unreachable only while the apiserver restarts. That is the single-control-plane case of the failure described in "Amendment: apiservers first".
- With an endpoint of type `node` and several control planes, the API stays unreachable for clients using that node's address while its apiserver restarts, in every stage that restarts it; staging does not change that, which is why the plan warns.

## Amendment: apiservers first

**The failure.** With 3 control planes, Talos v1.14.3 and an upgrade from v1.36.5 to v1.37.1, the `ControlPlanes` stage moves the first control plane's apiserver, controller-manager and scheduler together. The v1.37.1 apiserver and controller-manager become Ready; the v1.37.1 scheduler never does (readiness 500 for 20 minutes): `failed to list *v1.DeviceTaintRule: the server could not find the requested resource`. The v1.37 scheduler needs `resource.k8s.io/v1` DeviceTaintRule, which only v1.37 apiservers serve, and the other two apiservers were still v1.36.5. The executor waits for all three pods and pauses after 20 minutes.

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

**Resume.** `status.kubernetes.stage` is one of the four stages; every call compares the node's configuration hash with the render for the current stage, so a restarted controller either applies or only verifies.

**Plan and UI.** The plan lists four stages; the UI shows "Stage N of 4".

**Order.** The apply order is `APIServers` cp1 cp2 cp3, `ControlPlaneComponents` cp1 cp2 cp3, `KubeProxy`, `Kubelets`; after every apply no controller-manager, scheduler or kubelet is newer than any apiserver.

No compatibility with pre-release stage layouts: Ichor had no release, so the `ControlPlanes` stage and the unstaged-operation rule are gone; an operation with no recorded stage starts at `APIServers`.

## Amendment (2026-11-16): the plan states each stage's order

The plan listed the control planes and then the workers as one sequence, with the machine the controller runs on at its sorted place. The executor does not visit them that way. Stages 1 to 3 (`APIServers`, `ControlPlaneComponents`, `KubeProxy`) visit every control plane, the controller's own included, one at a time in the upgrade order. Only `Kubelets` holds the controller's own machine back and visits it last, after the control planes and the workers, because its node is drained and the controller restarts. A plan that named the Kubelets order for every stage, or the sorted order for all of them, would state an order that does not happen.

`pkg/upgrade.KubernetesOrder` is the order, as data: for each stage, the machines of each step. `PlanOperation` builds its `stages` from it, each stage's line followed by the machines in order, the controller's own marked "where Ichor's controller runs"; and its `machines` are the `Kubelets` stage's steps, which is the only stage that has workers and `maxUnavailable`. The executor's `kubernetesBatch` does not call it, because it decides one batch at a time from the status; a test in `internal/controller` walks `kubernetesBatch` to the end and requires the same stages, steps and machines as `KubernetesOrder`, so a change to either side fails there. When the controller's machine cannot be determined the plan says it cannot show which machine is upgraded last, and lists every stage in the sorted order.

## Amendment (2026-11-16): the upgrade stops when the controller's own machine cannot be told from one whose node name could not be read

The Kubelets stage holds the controller's own machine back and visits it last, because its node is drained and the controller restarts (see "the plan states each stage's order"). Before this amendment `holdOwnNodeLast` read the node names of the machines in the operation and, for a machine whose name could not be read, silently treated it as not the controller's. If that machine was the controller's, the Kubelets stage drained and restarted it in its tier and the controller went with it. `holdOwnNodeLast` now takes the operation kind and returns an error in that case (the check is `ownNodeNames`), and `Reconcile` pauses the operation there, before `kubernetesBatch` runs.

The stop is not confined to the Kubelets stage. Which machine is the controller's is settled before the stage is chosen, so an unreadable name stops the operation in every stage, including `APIServers`, `ControlPlaneComponents` and `KubeProxy`, which visit the controller's own machine in its place in the order.

The condition is narrow. It applies only when the controller knows the name of its own node (`NODE_NAME` is set), when at least one machine's node name could not be read (no `spec.hostname`, the node does not report its name, and no Kubernetes Node has the machine's Talos endpoint as an address), and when no machine whose name could be read carries the controller's node name. If some machine's name is the controller's, the controller's machine is found, and a read failure on another machine is ignored, because a node has one name; a cluster with one dead, never-joined machine keeps working as long as the controller's own machine is found. With `NODE_NAME` empty (the controller runs outside the cluster) nothing changes.

What the operator sees is that the operation pauses. `Reconcile` does not return the error: the operation's phase becomes `Paused` and its `Paused` condition carries the reason `OwnNodeUnread` and the message `cannot tell which machine runs this controller (node "X"): ...`, followed by the reason the name could not be read, and an Event is written when the pause starts and again each time its reason changes: a retry that finds the lease held pauses with `ClusterLockHeld`, so the next pause on the unread name writes another Event. `status.failureReason` stays empty, so this is not a failed step and no retry request is needed. The pause releases the cluster lease, which an operation that stopped on the error used to keep, and every retry renewed, so at the controller another operation, a machine join or removal, the CNI handover or an etcd restore can take it, while the CLI and the UI refuse a new operation whenever an unfinished one exists (`unfinishedOperationFor`), and a paused one is unfinished, so only `kubectl` reaches the lease then; the operation tries again every 30 seconds. It resumes by itself, with no `spec.paused` and no retry annotation, when the machine answers or its Node appears, or when the machine is removed from the cluster: the pass that can tell takes the lease again; the phase returns to `InProgress` (or to `Completed` when nothing is left to advance, as when the unread machine was the last one and has been removed), and the `Paused` condition is removed. Each retry takes the lease for the length of the pass and gives it back when it pauses again, so another operation that asks in that moment waits paused on the lock and retries.

The etcd restore already refused on an unreadable name: it needs the name of every control plane for its marker, so it refuses on any failure to read one, with its own message (`cannot tell which control plane runs this controller`), and this amendment does not change it.

## Amendment, 2026-11-22: why the plan warns that it cannot show which machine is last

The plan's warning ("This plan could not determine which machine runs Ichor's controller (...), so it cannot show which is rebooted last") appears when the controller knows its own node name but no machine's name matches it while at least one machine's node name could not be read: the controller's machine might be the unread one, so the plan cannot tell which machine the Kubelets stage holds back for last. It is the plan's counterpart of the `OwnNodeUnread` pause above, and the plan lists every stage in the sorted order.

# 0082. The machine page shows live Talos and Kubernetes state, read-only

Status: Accepted · Date: 2026-10-26 · Design section: UI backend, Talos access, Secrets
Decided: a second read-only RPC, `NodeService.GetNodeTalosRuntime`, returns six independently failing sections (etcd, top processes, containers, network state, kernel modules, the Kubernetes view of the node) under ADR 0073's rules; process command lines, container environments and arguments, WireGuard keys and machine configuration are never carried; the `ichor-ui` ClusterRole gains `get;list` on pods; actions the SDK enables are listed below as a proposal, none is implemented

## Context

ADR 0073 showed what Talos reports as resources: system, network, storage, services, extensions. The Talos client offers more that an operator reaches for when a node misbehaves: whether etcd has a leader and an alarm, which process eats the memory, which containers run, what the routing table and the discovery membership look like, and what Kubernetes says about the node. The machine page already shows CPU/memory/filesystem usage and the hardware (`ResourceUsage`, `Hardware`), so those are not repeated.

The calls were checked against `github.com/siderolabs/talos/pkg/machinery` v1.14.3: `client.Client.EtcdStatus`, `EtcdMemberList`, `EtcdAlarmList`, `Processes`, `Containers`, and the COSI types `network.RouteStatus`, `cluster.Member`, `kubespan.PeerStatus`, `time.AdjtimeStatus`, `runtime.LoadedKernelModule`. They are plain reads. Whether a `role.Reader` certificate may call each of them is decided by the node, not by the library; a refusal comes back as that section's error (ADR 0073), so a Talos version that narrows the table fails visibly instead of silently.

## Decision

**RPC.** `GetNodeTalosRuntime(machine)` in `NodeService`, messages in `proto/ichor/v1alpha1/talosruntime.proto`. Authorisation, dialling (`dialNode`), the 10 second per-section timeout, the 300 byte error cap and the `maintenance_mode` flag are exactly `GetNodeTalosDetails`'. A separate RPC rather than more sections on the first: the first is cheap and always wanted, this one lists processes and containers and a pod list, and the page loads it independently so a slow node delays only this half.

**What is shown.**
- etcd (control plane machines; a worker gets `not_applicable`, no call is made): members (ID, hostname, peer and client URLs, learner, leader), the queried member's status (leader, database size and size in use, raft term, index and applied index, protocol and storage version, errors) and alarms.
- Processes: the union of the 15 heaviest by resident memory and by cumulative CPU time, with the total count: pid, ppid, state, threads, CPU seconds, resident and virtual memory, executable base name. Sorted in the UI.
- Containers: runtime namespace (`k8s.io` through CRI, `system` through containerd), ID, pod, name, image, status, pid. At most 300. If one of the two runtimes fails (kubelet not started yet) the other still shows; the section fails only when both do.
- Network state: routing table (at most 200), discovery members (`cluster.Member`: hostname, addresses, machine type, OS), KubeSpan peer status when enabled (label, endpoint, state, bytes, last handshake), time synchronisation detail (state, offset, max error) from `AdjtimeStatus`.
- Kernel modules: name, size, reference count, state.
- Kubernetes: the Node behind the machine (conditions with reason and a 200 byte message, taints, capacity against allocatable for cpu, memory, pods and ephemeral storage) and the pods bound to it (namespace, name, phase, ready and total containers, restarts, age; at most 300). Read as the caller, like `GetNode` (ADR 0062): with OIDC on the user's own Kubernetes RBAC decides, and a refusal shows "not allowed", not an error.

**What is deliberately not shown, and why.**
- Process command lines and arguments (`ProcessInfo.Args`, the cgroup label) and the executable's full path: a command line can carry a token or password (`--token=...`). Only the base name of the executable is kept. This is stricter than the kernel-argument masking of ADR 0073 because process arguments have no fixed shape to mask by key.
- Container and pod environment, command and arguments, volumes and annotations: never read. `podFromObject` takes the name, namespace, phase, container counts, container statuses and creation time; the container list is used only for its length. Image references have a `user:pass@` prefix removed (`sanitizeImage`, applied in the handler for every client); digests are kept.
- KubeSpan: the peer's public key (the resource ID) and the private key are not copied. ADR 0073 listed "WireGuard private keys and peers" as not shown; this ADR shows peer status without any key material, because connection state is what an operator debugging a mesh needs.
- Machine configuration, certificates, secrets, and etcd data: unchanged from ADR 0073. etcd *status* is shown; nothing from the keyspace is.
- The Talos events stream and machine status history: a stream to summarise has no bound and no cheap snapshot, and the stage and unmet conditions are already in the details view. Left out until there is a request.
- Memory, CPU, load and mount usage: already on the page (`ResourceUsage`), not repeated. Load average has no cheap read-only source in this version.
- Container image listing (`ImageList`): not cheap, adds little over the container table.

**Mapping.** Allowlist, field by field, in `talosruntime_map.go` and `talosclient_runtime.go`; no Talos or Kubernetes object is marshalled whole. Tests build values whose secret-shaped fields (process args, label, env, image credentials) carry a marker and assert it never appears in the marshalled response.

**RBAC.** One addition to the `ichor-ui` ClusterRole: `pods` `get;list` (cluster-wide). Pods are namespaced, but a node's pods live in every namespace, so there is no narrower grant; one `List` per page view with `spec.nodeName` as a field selector, no watch, no `pods/log`, `pods/exec`, `secrets`. The ServiceAccount's grant matters only when OIDC is off (with OIDC on the user's own permissions apply). It is a metadata read of pod names and images across the cluster by a component that already reads every TalosMachine; the alternative, asking the user's token only, would break the OIDC-off install. `rbac_markers.go`, `config/rbac/ichor-ui/role.yaml` and the chart template change together; `dist/chart/chart_test.go` and `internal/uiapi/rbac_enforced_test.go` (which runs the new RPC as the generated role) cover them.

**UI.** `TalosRuntime` renders six cards, each with an anchor id (`etcd`, `processes`, `containers`, `k8s-node`, `netstate`, `kernel`) listed in the page's section navigation. Tables scroll inside their own container. In maintenance mode a section the insecure API refuses reads "not available over the maintenance API" instead of an error, and sections it allows show their data.

**One source per fact on the page.** The page used to show the same facts from `GetNode` and from the Talos details card. Now each appears once, from the richer source, as flat page sections: hardware (`GetNode`), resource usage (`GetNode`), Talos system, disks and volumes, network (links, addresses, gateways, DNS), services, extensions, then the live-state sections. `GetNode`'s disks and interface tables and its Kubernetes block are no longer rendered (the RPC is unchanged; the install-disk picker still uses its disks): the Talos disk table carries model, serial, type and the install disk, `GetNode`'s disk role is always unspecified, and the Kubernetes card now also carries the kubelet version and schedulability. The per-interface address list of `GetNode` is not shown; the node addresses are. The route count and time-sync flag of the details card are dropped in favour of the routing table and time detail in this view. Volumes of type directory, overlay, symlink and tmpfs (Talos's own size-less mounts) are hidden behind a "show all" toggle. A missing or firmware stand-in value ("None", "To Be Filled By O.E.M.") is shown as "-" everywhere (`display`). The machine breadcrumb shows the hostname, falling back to the name.

**Resource usage fixes.** `perf.Memory` is in kibibytes, not bytes (a 4 GB node showed "3.8 MB"); `perf.CPU` is cumulative seconds per state, not percentages (the old formula gave -24.99 cores), so the backend now samples it twice, up to 3 seconds apart, and reports the busy share of the difference, with `cpu_sampled` false when the counters did not move (first version; superseded below); the filesystem was never filled and now comes from the Mounts API (`/var`, else the largest mount).

**CPU source fix.** The `perf.CPU` resource is rewritten by Talos's perf controller only every 30 seconds (`updateInterval`), so two reads up to 3 seconds apart almost always returned the identical snapshot and `cpu_sampled` stayed false (a "-" on every node, busy or not). The backend now reads `MachineService.SystemStat`, which parses `/proc/stat` on each call (readable by `role.Reader`), twice one second apart. Memory still comes from `perf.Memory`.

## Actions the SDK enables: a proposal, nothing implemented

The rule stays: the UI and CLI change the world only by writing CRs, and the controller is the only writer to Talos nodes (never mutating `spec`). Every action below is therefore a `TalosOperation` of a new kind (ADR 0068), or a field on an existing CR the controller already acts on, executed by the controller with its own admin identity; the UI backend's `role.Reader` identity never gains a write method. Each operation records who asked (the audit record the project already writes), shows progress in its status conditions, and is blocked by the same guards as other operations (cluster paused, another operation running, etcd quorum). Ranked by usefulness, then lowest risk first:

1. **Reboot a node** (`Reboot`, optional `mode: powercycle`). Most requested after a config change or a stuck node. Operation `kind: Reboot`, `machineRef`, `wait` for the node to return Ready. Risk medium: guard that it leaves etcd quorum intact (refuse on the second control plane of three while one is down) and that a drain precedes it (ADR 0080 already rolls reboots through an operation).
2. **Restart a Talos service** (`ServiceRestart`, e.g. kubelet, containerd): the usual fix for a wedged kubelet. Operation `kind: RestartService`, `service` from an allowlist (kubelet, containerd, cri; never `etcd`, `apid`, `trustd`, `machined`). Risk low.
3. **Collect a support bundle** (`Support`): the controller fetches the archive and stores it as a Secret-free artifact the user downloads. Operation `kind: SupportBundle`; the archive contains logs and resource dumps that may carry secrets, so it is admin-gated, never shown in the UI, delivered as a download with its own audit entry. Risk low to the node, medium for data exposure.
4. **etcd defragment** (`EtcdDefragment`): reclaim space when the "in use" share of the database shown in this view is low or a `NOSPACE` alarm is set. Operation `kind: EtcdDefragment`, one member at a time. Risk medium: it blocks that member while it runs, so never two at once and never the leader first; refuse when quorum would be lost.
5. **etcd snapshot** (`EtcdSnapshot`): a backup operation writing to the cluster's configured backup target (needs the `os:etcd:backup` role the controller already has as admin). Risk low.
6. **etcd forfeit leadership** (`EtcdForfeitLeadership`): hand leadership to another member before maintenance. Operation `kind: EtcdForfeitLeadership` on the leader. Risk low-medium.
7. **etcd leave / remove member** (`EtcdLeaveCluster`, `EtcdRemoveMemberByID`): for retiring a control plane node or removing a dead member. Part of a node-removal operation, not a stand-alone button. Risk high: can break quorum; the operation re-checks member count and needs an explicit confirmation field in the CR.
8. **Reset a node** (`Reset`, graceful, wipe system/ephemeral or all): the destructive path to re-provision. Operation `kind: Reset` with `wipe` scope and a `confirm` field equal to the machine name, override gated (ADR 0064). Risk highest: loses data.
9. **Packet capture** (`PacketCapture`, bounded by duration and size): a diagnostic that streams pcap bytes. Operation `kind: PacketCapture` with an interface and a BPF filter, result delivered as a download; the capture can contain secrets on the wire, so it is admin-gated and time-boxed. Risk medium for data exposure.
10. **Rotate or refresh certificates, upgrade, bootstrap**: already covered by existing operations (ADR 0068, `carotation`).

`Kubeconfig`, `Read` (file contents), `Copy`, `Rollback` and `ImageList` are not proposed: the first two expose secrets or arbitrary files, the others duplicate existing operations or add little.

## Consequences

- One more read per page view per node (six short calls and one pod list); no polling.
- `NodeTalosClient` grows five methods; the fake returns canned sections and the secret-shape tests cover the response.
- The `ichor-ui` ClusterRole can now list pods cluster-wide when it acts as itself; operators who run OIDC on and want it narrower can bind the user's own role and remove the rule from their values later, which is a chart change this ADR does not make.
- `ichor` CLI's in-process fallback answers `Unavailable` for this RPC, like `GetNode`.

## Amendment (2026-10-28)

The runtime sections are no longer listed in a section navigation: the node page groups them into tabs (Talos; Kubernetes and etcd), with the active tab in `?tab=` (ADR 0097).

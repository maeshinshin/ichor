# 0083. Add a node from the cluster page; toasts; alert-dialog confirms; logs in a code block

Status: Accepted · Date: 2026-10-27 · Design section: UI
Decided: The cluster page lists its nodes and links to an Add node page that explains how a node joins, shows `spec.discovery` and adds one address through a new `ClusterService.AddDiscoveryAddress` write that passes the same gate as every other write; every successful change shows a sonner toast; every dangerous action confirms in a preactcn `alert-dialog`; the node log lines render in a preactcn `code-block`; amended 2026-11-02: AddDiscoveryCidr, RemoveDiscoveryAddress and RemoveDiscoveryCidr, one Add node input for an address or a range, `ichor discovery`, and the spec write itself starts the scan
Amends: 0078

## Context

A person looking at a cluster could not see its nodes, and there was no way to bring a new node in from the UI: discovery (ADR 0011) only dials the addresses listed in `spec.discovery`, so a node on a new address is never found until someone edits the cluster by hand. Changes also gave no feedback, dangerous actions used a plain `dialog` that can be dismissed by a stray click, and the log viewer was a hand-styled div.

## Decision

1. **Cluster page.** A Nodes section (hostname, role, Ready with reason, stage, Talos, kubelet) with links to each node page, counts in the header ("2 approved · 2 ready · 1 pending") and an "Add node" button. The list comes from `ListMachines(cluster)`. Stage and Ready are on the machine. The kubelet version comes from one `GetNode` per approved node, filled in after the table. The Talos column is the cluster's Talos version: a per-node version needs `GetNodeTalosDetails`, which makes many Talos calls per node, too heavy for a list.
2. **Add node page** at `/clusters/:namespace/:name/add-node` (a page, not a dialog, because it holds a list and a form). It has three short steps (boot the ISO or PXE; Ichor finds it; approve it), the cluster's addresses and ranges, a form to add one IPv4 address, and the cluster's pending nodes with the existing approval dialog (now `components/ApproveDialog.tsx`, shared with the node list). The pending list polls every 5 seconds.
3. **`ClusterService.AddDiscoveryAddress`** appends one IPv4 address to `spec.discovery.addresses` and writes nothing else. It rejects an empty, non-IPv4 or already listed address. It goes through `planWrite` like the other writes: refused when the cluster is tracked and no pull request can be opened, a pull request when Argo CD tracks it, override on request. In PR mode it sets `spec.discovery` as a whole, because the PR editor cannot create a missing parent key. `Cluster` gains `discovery_addresses` and `discovery_cidrs`. Ranges are not editable here (they stay in the spec).
   Override is now also valid on `talosclusters`: `kindOf` knows the kind and the `override` ClusterRoles list it. The override admission webhook (ADR 0065) still covers only machines and patches, so for a cluster the SelfSubjectAccessReview in the backend is the only check. This is called out because it is narrower than for the other kinds.
4. **Toasts.** preactcn `sonner`, mounted once in `AppShell` and following the theme class on `<html>`. One helper, `lib/notify.ts` (`notifyDone`, `notifyWrite`, `notifyError`). `notifyWrite` says "Pull request opened" instead of "done" when the response carries a pull request URL (`lib/outcome.ts`). Errors stay inline where they were.
5. **Confirms.** `components/ConfirmDialog.tsx` wraps preactcn `alert-dialog`: no dismiss on outside click, focus starts on Cancel, a destructive confirm button, and the error shows inside when the server refuses (the confirm is a plain button, not `AlertDialogAction`, so the dialog does not close). Used by: reject node, delete patch (list and page), delete operation, override, and the start-operation dialog (Talos and Kubernetes upgrades, Ichor, CNI, config rollout with reboots, CA rotation, shutdown), which also shows a destructive warning for its kind before and after the plan review. The typed cluster name for shutdown stays. Approve, save install disk and patch save are not destructive and keep `dialog`.
6. **Logs.** `NodeLogs` uses `code-block` (`CodeBlock`, header, title, actions, copy button, `CodeBlockContent` with its `wrap` prop, one `CodeBlockLine` per entry) with a wrap toggle. The component is plain markup: its `use-highlighted-code` dependency (shiki) is not used, because highlighting 2000 changing lines every 100 ms would be slow and log lines have no language. The ring buffer, batching and truncation of ADR 0079 are unchanged.
7. **Small fixes.** Overview: "All completed" / "N running" / "N failed" for operations; "N ready" or "N of M ready" for clusters, where a cluster without a Ready condition (a TalosCluster reports only Transitioning today) is ready when it is not transitioning and no health condition is False. Node resources show sizes and cores (`formatResource`); pods list Running before finished ones; "machine" wording became "node".

## Consequences

- preactcn components added: `sonner` (with its `next-themes` and `sonner` shims), `alert-dialog`, `code-block`.
- A node's own Talos version is not on the cluster page; revisit if a cheap read appears.
- The proposed node actions (reboot, shutdown per node, ADR 0082) will use `ConfirmDialog` and have a place in the node page header.

## Amendment (2026-11-02): ranges and removal

Adding a node must also work for a whole range, not only one address. The decisions:

1. **New RPCs** (append-only): `ClusterService.AddDiscoveryCidr`, `RemoveDiscoveryAddress` and `RemoveDiscoveryCidr`. Each reads the cluster, changes only `spec.discovery` and writes it as the caller (impersonated, so RBAC is the API server's), and returns the stored cluster plus an `AuditRecord` (`add-discovery-cidr`, `remove-discovery-address`, `remove-discovery-cidr`). A range must be IPv4 and /24 or smaller (the CRD rule, ADR 0011), is stored masked (`192.168.0.5/24` becomes `192.168.0.0/24`), and is refused as `InvalidArgument` with the reason when it is larger, not IPv4, already listed, or would be the fifth. Removing something not listed is `InvalidArgument` too. Removing a scan target never touches machines already found.
2. **Immediate scan.** Nothing new is needed in the controller: `TalosClusterReconciler` watches `TalosCluster` without a predicate, so the spec write itself enqueues the cluster and `DiscoverAll` runs at once instead of at the 5 minute requeue. The controller still never writes `spec`. Machines found in the range show up as pending in the existing view, which the page polls every 5 seconds.
3. **UI.** One input takes an address or a CIDR; a slash means a range. It is checked client-side with the same rules (`lib/discovery.ts`) and calls the matching RPC. Every listed address and range has a remove button (ghost icon button with an aria-label); removing a range asks first in a `ConfirmDialog`, an address does not.
4. **CLI.** `ichor discovery list|add|remove [--cluster NAME]`, through ichor-ui like the other writes; `add` and `remove` pick the RPC by the slash, and `remove` of a range asks unless `--yes`.

## Amendment (2026-11-03): the first scan can miss, so it is repeated soon

A machine that has just booted into maintenance at a new address can appear as pending only after the 5 minute requeue: the first probe runs before the machine or the egress rule is ready and finds nothing. Two things make it worse. The settle wait (3 s) runs only when the TalosCluster reconciler itself wrote the egress policy; when the egress reconciler wrote it first, `Sync` reports no change, the probe runs at once, and the two writers conflict. Decisions:

1. `TalosEgress.Sync` retries a Conflict or AlreadyExists (up to 3 times) against the fresh object, and `SettleAfter` waits until the settle time has passed since the last write by either reconciler.
2. The TalosCluster reconciler remembers, in memory, when it first saw each discovery address and CIDR. While any is younger than 3 minutes and a pass found nothing, it requeues after 15 s instead of 5 min. That bounds the extra probing to about 12 passes per new entry; after a controller restart every entry counts as new once.

Expected latency: the pending TalosMachine appears within about 15 to 30 s of the machine answering on the maintenance port, if that happens within 3 minutes of the add; later, within the 5 minute pass.



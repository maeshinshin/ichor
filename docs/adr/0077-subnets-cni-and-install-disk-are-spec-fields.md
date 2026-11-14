# 0077. Pod and service subnets, the CNI Talos runs and the install disk are spec fields rendered per contract

Status: Accepted · Date: 2026-10-26 · Design section: Nodes (approval, disk roles), CNI, Bootstrap

Decided: `TalosCluster.spec.network {podSubnets, serviceSubnets}` (optional, fixed once set) and `TalosMachine.spec.install {disk, image}` carry the values ADR 0075 could not express; the system layer renders them as the v1.14 documents (`KubeNetworkConfig`, `UnattendedInstallConfig`) from that contract and as the legacy fields before it, and drops Talos's own CNI (the `KubeFlannelCNIConfig` document, or `cluster.network.cni.name: none`) for every preset but flannel; approval (`ichor approve node --install-disk`, the UI form) sets the install disk, `ichor init` stops writing `machine.install`, hostnames are RFC 1123 checked, and `adopt` names the spec field for each such drift path. This is option C of ADR 0075 with the per-node half also as a spec field, not a patch kind.

## Context

ADR 0075 left open how a v1.14 cluster gets non-default subnets, an install disk and "no flannel". At the v1.14 contract
the generator emits its defaults, no spec field carried those values, and the legacy `Config` fields conflict with the
v1.14 documents, so a correct cluster could not be built, adopted, or have a maintenance node installed.

## Decision

1. `TalosCluster.spec.network.podSubnets` / `serviceSubnets`: lists of CIDRs, at most two each (one per family), valid
   CIDR by CEL (`isCIDR`). An unset list keeps Talos's default (10.244.0.0/16, 10.96.0.0/12). The whole field has
   `self == oldSelf`: it cannot change once set. A cluster that never declared it may set it once, so an adopted cluster
   can record what it already runs; setting it on a running cluster to something else renders a different
   `KubeNetworkConfig`, which is reboot-required and refused by the machine controller (below).
   `pkg/render` sets the generator's own `PodNet`/`ServiceNet`, so machinery chooses the form: the `KubeNetworkConfig`
   document at v1.14 and `cluster.network.podSubnets/serviceSubnets` before it.
2. CNI. For a preset other than `flannel` (`cilium`, `custom`) the system layer removes Talos's CNI: at v1.14 the
   `KubeFlannelCNIConfig` document, below it `cluster.network.cni.name: none`. Before this change the cilium preset
   did neither below v1.14 either (its patch only sets the kube-proxy flag), so the two contracts now agree. A custom
   preset's own `documents` are patches and still win. An unset preset is flannel (the CRD default).
3. `TalosMachine.spec.install.disk` selects the `system` disk by exactly one of `wwid`, `serial`, `byIDPath`,
   `busPath` (the design's fallback chain) or `devPath` (last resort, the kernel can renumber it); `install.image` is the
   installer image, empty meaning the booted schematic's. `disk` is required inside `install`: the v1.14 document is
   invalid without a selector. At v1.14 it renders `UnattendedInstallConfig` with a CEL selector checked against
   Talos's disk environment (`disk.wwid == "..."`, `disk.serial == "..."`, `"/dev/disk/by-id/x" in disk.symlinks`,
   `disk.bus_path == "..."`, `disk.dev_path == "..."`) and `wipe: false` (what the generator writes). Before v1.14 it
   renders `machine.install` (`diskSelector.wwid|serial|busPath`; `installDisk` for a by-id or device path, because the
   legacy selector has no symlink match). A machine with no `install` renders none, as before.
4. Approval. `ApproveMachineRequest.install` and `Machine.install` (proto), `ichor approve node --install-disk
   <wwid:ID | serial:S | /dev/disk/by-id/... | /pci... | /dev/sdX> [--install-image]`. The UI form lists the disks that
   `GetNode` already reads from a maintenance-mode machine (`Disk.dev_path` is new on the wire) and picks the most
   stable identifier each reports; if the machine reports no disks (Talos API unreachable) it falls back to a typed
   device path and says so. `ichor init --install-disk` takes the same syntax and writes `spec.install` on the first
   machine; its bootstrap patch keeps only `machine.type` and `machine.certSANs` (the patch CRD requires them).
5. Hostname: `TalosMachine.spec.hostname` is an RFC 1123 subdomain (CRD pattern and 253 limit), checked again in
   `ApproveMachine` (`InvalidArgument` with the reason) and in the UI form. This rejects "てすと".
6. `adopt` reports, for each drift path it cannot patch, the spec field that carries it: `KubeNetworkConfig.podSubnets`
   / `cluster.network.podSubnets` -> `spec.network.podSubnets` (service likewise), `UnattendedInstallConfig.*` /
   `machine.install.*` -> `spec.install`, `KubeFlannelCNIConfig.*` / `cluster.network.cni` -> `spec.cni.preset`.
   `adopt` still does not write those fields itself: they live on the TalosCluster and TalosMachine, which the user owns.

## Hash and reboot impact on existing clusters

The rendered hash is unchanged for a flannel cluster, for any cluster with no `spec.network` and no `spec.install`, and
for a machine with neither. It changes in two cases.

- A v1.14 cluster with a non-flannel preset: the rendered config no longer has `KubeFlannelCNIConfig`. The diff has
  `KubeFlannelCNIConfig.*` paths, which `pkg/fieldmeta` classifies as reboot-required (it has no rule for `KubeFlannelCNIConfig.*`, so the
  safe default applies). The machine controller never reboots by itself: `refuseReboot` sets Ready false and leaves the node as it is. So there is no surprise reboot, but
  such a machine reports "needs a reboot" until the operator applies the new config (`talosctl apply-config --mode
  no-reboot` after checking, or a planned reboot). The flannel DaemonSet Talos created at bootstrap is not pruned by
  Talos on its own either way. Talos's own flannel next to Cilium was the fault being fixed.
- A pre-1.14 cluster with a non-flannel preset gains `cluster.network.cni.name: none`: path `cluster.network.cni.name`,
  also reboot-required by default, same outcome.
- Setting `spec.install` on a machine that has none: v1.14 adds `UnattendedInstallConfig`, pre-1.14 changes
  `machine.install`. Both are classified install-time-only (new rule `UnattendedInstallConfig`, next to
  `machine.install`), so the controller reports "needs a reset and reinstall, not an apply" and applies nothing: the
  field is for approval of a node that has not installed yet, which receives the whole config at first apply.
- A cluster whose bootstrap patch still carries `machine.install` (written by an older `ichor init`) keeps working
  while its machine has no `spec.install`. Setting both at v1.14 fails validation inside Talos (`UnattendedInstallConfig`
  conflicts with `machine.install`), and the render error says so; remove the legacy field from the patch first.

## Not done

- Cert SANs, the DNS domain, install wipe and extra kernel arguments still have no field (certSANs stays in the init
  bootstrap patch as a legacy `Config`; whether Talos accepts that next to the v1.14 documents is an open question).
- No TalosOperation resets a node to apply a changed install disk.
- The disk list in the approval form comes from `GetNode`, so for a machine that no one can reach it is a typed path.
- Whether Talos v1.14.3 can apply the removal of `KubeFlannelCNIConfig` without a reboot is not known; until it is, the
  safe default (reboot-required) stands.

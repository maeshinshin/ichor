# 0112. Approval picks the install disk and NIC from what discovery recorded

Status: Accepted · Date: 2026-11-03 · Design section: Nodes (approval, disk roles) · Touches: 0011, 0076, 0077, 0092

Decided: discovery records the installable disks and the physical NICs with a permanent MAC that a node reports through its maintenance-mode API on `TalosMachine.status.discoveredDisks` and `status.discoveredLinks`; the approval form lets the person pick the install disk, the disk roles and the NIC from those lists, starting on the smallest disk of at least 10 GiB and on the first NIC with a link; a typed device path is offered only while neither list has anything; an approval whose pick is not in the recorded lists gets an admission warning, not a refusal.

## Context

ADR 0092 made approval write `spec.network.mac`, `spec.disks[]` and `spec.install.disk`, and its UI took the choices from `GetNode` (the node's disks) and `GetNodeTalosDetails` (its links). For a machine still in maintenance mode both reads go through the unauthenticated API, and a failed read leaves the form with no list. The approval dialog then showed a typed device path (`/dev/sda`), and a typed NIC MAC, where the person needed a choice. A typed path names a device the node may renumber on the next boot, and a typed MAC is a guess.

Talos's maintenance API serves the two resources that matter: `talosctl get disks --insecure` and `talosctl get links --insecure` are the documented way to see a node's disks and links before it is configured (Talos v1.14 getting-started and platform guides). Discovery already reads `network.LinkStatus` over that API for the permanent MAC that makes up the identity (ADR 0011), so the probe is the same connection and the same trust rule (ADR 0076, 0108).

## Decision

1. **What is recorded.** Discovery's probe (`MaintenanceClient.Inventory`) reads two lists:
   - `discoveredDisks`: every `block.Disk` that is neither a CD-ROM nor read-only, with its WWID, serial, by-id symlink, bus path, device path, size in bytes, model and transport. These are the identifiers `spec.install.disk` names, plus what a person needs to tell the disks apart.
   - `discoveredLinks`: every physical `network.LinkStatus` with a permanent address (no kind, so no bond, bridge or VLAN), with its name, permanent MAC, carrier (`linkUp`), speed and driver. The permanent MAC is the value `spec.network.mac` matches. It is not the current hardware address, which is different for a bond member or a spoofed address.

   Both lists are sorted, so an unchanged node writes nothing. The fields are status, not spec: the controller never writes spec (ADR 0086). Both are append-only CRD fields.
2. **When it is recorded.** Discovery records the lists when it creates a pending TalosMachine, and again when it finds a known pending machine at a new address. A failed read leaves the lists empty and does not fail discovery. Machines discovered before this decision have no lists until a probe reaches them; the form falls back to the node's own reads until then.
3. **Defaults.** The install disk starts on the smallest recorded disk of at least 10 GiB that has an identifier to select it by (10 GiB is Talos's stated minimum for its install; ties keep the list's order). Talos has no default of its own, so the form says the disk is preselected and the person checks it. The smallest disk is chosen so the larger ones stay free for data. The NIC starts on the first card with carrier. A card is preselected only while the person has not chosen one; "Leave to Talos" stays available as the choice for leaving every card to Talos.
4. **Fallback order.** The recorded lists come first. The node's own live reads (`GetNode` disks, `GetNodeTalosDetails` links) fill in only for a machine that has no recorded lists. A typed device path or MAC is offered only when both are empty, and the install disk then still needs a device path that starts with `/dev/`.
5. **Admission.** The TalosMachine webhook returns a warning, and never a refusal, for an approval (create approved, or an update that moves `spec.install`, `spec.network` or `spec.disks`) whose install disk, disk role or MAC matches nothing recorded. The check is skipped while a list is empty, so a machine with no record gets no warning. The same text reaches the caller in `ApproveMachineResponse.warnings`, since the client does not see admission warnings; the UI shows it as a toast and `ichor approve node` prints it on stderr. The approval still goes through: the disk may be attached after discovery, and nothing here is Ichor's own safety rule (ADR 0109).
6. **CLI.** `ichor approve node` keeps every flag. `ichor describe node -o yaml` shows the recorded lists, since the whole message is printed; there is no separate table.

## Not done

- A pending machine that is known to discovery (its endpoint is already on a TalosMachine) is not probed again by the sweep, so its lists are recorded at creation or at its next new address. A refresh on a timer would be a new reconcile path for pending machines, which the reconciler does not have.
- The list is a snapshot. A disk attached after discovery is not in it until the node is probed again, and the form then offers the typed path only when the list is empty.

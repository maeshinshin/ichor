---
sidebar_order: 21
---

# Testing

e2e runs against throwaway clusters from `talosctl cluster create qemu` from
the first phase onwards. QEMU is not a preference but a requirement for most of
it: the Docker provisioner is capped at one control plane, has no disks, and
its own documentation excludes the `upgrade` and `reset` APIs, so install, disk
roles, reboot-requiring patches, removal, topology changes and upgrades can
only run under QEMU. Docker stays useful for the cheap half — config applies
that need no reboot, and drift. Node discovery needs a node that is up but
unconfigured, which `--presets maintenance` provides. Scenarios follow the
features:
bootstrap and join, patches with and without reboot, drift, removal and
replacement, topology changes including the even-count rejection, CNI
Cilium bootstrap, adoption and values edits, prune protection, storage preparation and evacuation, all three upgrade kinds
with rollback, backup, restore and full-loss recovery, certificate rotation.
Each scenario runs through both the CLI and the backend API. Chaos cases kill
the controller or reboot a node mid-rollout and check that status-driven
resumption works. A short set runs per PR, the full set nightly.

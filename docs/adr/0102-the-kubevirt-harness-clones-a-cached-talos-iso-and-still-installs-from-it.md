# 0102. The KubeVirt harness clones a cached Talos ISO and still installs from it

Status: Accepted · Date: 2026-10-29 · Design section: Testing

Decided: the KubeVirt harness keeps one Talos ISO DataVolume per Talos version and schematic (`ichor-e2e-talos-<version>-<schematic8>-base`, in `ichor-e2e`) that no `down*` deletes, and each VM's ISO DataVolume clones it through CDI (`source.pvc`) instead of importing the ISO again; `ICHOR_E2E_BASE_CACHE=0` keeps the old per-VM import; a pre-installed disk image is not cached.

## Context

A bring-up took about 15 minutes, 2 to 5 of them importing the ISO per VM. The VMs boot the ISO in maintenance mode on a blank system disk, and Ichor installs Talos there (`ichor init` for the first node, `ichor machine approve --install-disk` for the others).

## Decision

- Only the ISO is cached. A cached `metal-amd64.raw.zst` disk would boot straight to a running, already-installed node: no install, no reboot, and no maintenance mode, which `ichor init`'s insecure apply and the approve flow both need. That would skip the step the product must exercise, so it is out.
- The cache is created by the first run (`kubectl apply`, idempotent, `deleteAfterCompletion` off so CDI keeps the DataVolume) and the harness waits for `Succeeded`. VMs clone it at the cache PVC's capacity. Clones need the same StorageClass (`ceph-block`).
- The VMs of a run are created together before any is waited on (the worker is not started after the control plane).

## Consequences

The cache is never deleted by the harness; remove it by hand when the Talos version moves on. Measured on the lab cluster: cold cache 87 s once, warm 1 s; control plane and worker at maintenance 64 s after the cache exists, against 2 to 5 minutes before.

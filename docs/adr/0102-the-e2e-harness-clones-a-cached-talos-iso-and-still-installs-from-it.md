# 0102. The e2e harness clones a cached Talos ISO and still installs from it

Status: Accepted · Date: 2026-10-29 · Design section: Testing

Decided: the e2e harness keeps one cached Talos ISO per Talos version and schematic (`ichor-e2e-talos-<version>-<schematic8>-base`, in `ichor-e2e`) that no `down*` deletes, and each node's ISO disk is cloned from it instead of importing the ISO again; `ICHOR_E2E_BASE_CACHE=0` keeps the old per-node import; a pre-installed disk image is not cached.

## Context

Importing the ISO again for every node dominates the time to bring a cluster up. The nodes boot the ISO in maintenance mode on a blank system disk, and Ichor installs Talos there (`ichor init` for the first node, `ichor approve node --install-disk` for the others).

## Decision

- Only the ISO is cached. A cached `metal-amd64.raw.zst` disk would boot straight to a running, already-installed node: no install, no reboot, and no maintenance mode, which `ichor init`'s insecure apply and the approve flow both need. That would skip the step the product must exercise, so it is out.
- The cache is created by the first run (`kubectl apply`, idempotent) and the harness waits for `Succeeded`. Nodes clone it at the cache PVC's capacity. Clones need the same StorageClass as the cache.
- The nodes of a run are created together before any is waited on (the worker is not started after the control plane).

## Consequences

The cache is never deleted by the harness; remove it by hand when the Talos version moves on. Cloning from a warm cache is orders of magnitude faster than fetching the ISO for each node.

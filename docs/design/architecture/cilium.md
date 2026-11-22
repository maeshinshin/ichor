---
sidebar_group: "Cluster lifecycle"
sidebar_order: 11
sidebar_label: "Architecture: Cilium"
---

# Cilium

![Cilium boots from the inline manifest, the ichor-cni Job adopts it as a Helm release and the controller removes the inline entry. Live values layer Ichor defaults, the user's layer and Ichor-owned keys. A foreign Cilium is checked, diffed and taken over.](../images/cilium.light.drawio.svg)

*Cilium is Ichor's own Helm release; this is how it gets that way, what its values are, and how a foreign one is taken over.*

## Handover

Removing the inline entry matters: Talos only server-side applies bootstrap manifests and re-applies them on a Kubernetes upgrade, so a stale entry would roll Cilium back. The `ichor-cni` Job (installer image, hidden `ichor cni helm apply`, host network via KubePrism) needs cluster-admin-equivalent rights, so the controller does not run Helm itself.

## Values

- Edited in the UI (Cilium page) or with `ichor get|apply|edit|diff cni-values`; one function validates them in the webhook, the backend and the controller; the Job applies them.
- Changing the layer needs `update` on `talosclusters/cilium-values`, held by `ichor-admin` only (the chart can run privileged pods on every node).
- The `*.secretsNamespace.name` keys are Ichor-owned, pinned to `cilium-secrets`.
- The operator runs one replica while the cluster has one node, two from then on (counted from the Nodes that exist). A Helm Job that keeps failing with the same values is retried with a growing backoff.

## Upgrades

A new Ichor release carries new pinned charts. Cilium upgrades only when a `cni` operation is started (`ichor upgrade cni`, or the UI), at most one minor at a time, rolled back by Helm on failure. The cluster rollout runs it last.

## Takeover

`ichor adopt` can make a Cilium installed by someone else (a Helm release, the Cilium CLI, a Talos inline manifest, an applied `helm template`) Ichor's release.

- **Refused before anything is written:** a live minor that is neither the pinned chart's minor nor one lower; `ipam.mode` other than `kubernetes` (`cluster-pool`); an unhealthy Cilium or KubePrism on any node. The tunnel protocol is carried over.
- **Values** come from the newest Helm release record. With none (inline manifest, `helm template`) Ichor's defaults are used and the diff shows what is missing.
- **Diff:** Ichor renders the pinned chart with the effective values and compares every object with the live one. A difference needs `--accept-cilium-diff`; a change to an Ichor-owned key on the running data plane needs `--accept-cilium-changes`. IPAM and the tunnel protocol are never accepted.
- **Apply:** the `ichor-cni` Job runs `cilium.Apply` with `--takeover`, so Helm installs with `TakeOwnership`.
- **Cleanup:** once the rollout is Ready the foreign release record Secrets are copied into `ichor-cilium-prior-release` in `ichor-system`, read back byte for byte, then deleted (`RemoveForeignReleaseRecord`; never `helm uninstall`). An inline manifest entry is found by content, recorded in `status.cni.takenOverInlineManifest`, and removed from the machine config only after `CNIHandover` is true.
- There is no rollback of an adoption.

---
sidebar_group: "Cluster lifecycle"
sidebar_order: 11
---

# Cilium

Cilium is Ichor's own Helm release. It boots from the inline manifest;
once the DaemonSet is Ready the `ichor-cni` Job (installer image, hidden
`ichor cni helm apply`, host network via KubePrism) adopts the bootstrapped
objects as release `cilium` with `TakeOwnership`, and the controller then
removes the inline entry (`CNIHandover`). Removing it matters: Talos only
server-side applies bootstrap manifests and re-applies them on a Kubernetes
upgrade, so a stale entry would roll Cilium back. The Job needs
cluster-admin-equivalent rights, so the controller does not run Helm itself.

Values. Cilium's live values are Ichor defaults, then the user's layer in
`spec.cni.cilium.values`, then Ichor-owned keys (the Talos-required ones, the
KubePrism address, `kubeProxyReplacement`, `tunnelProtocol`). They are edited
in the UI (Cilium page) or with `ichor get cni-values|set|edit|diff`, validated
by one function in the webhook, the backend and the controller, and applied by
the Job. Changing the layer needs `update` on `talosclusters/cilium-values`, held
by `ichor-admin` only (the chart can run privileged pods on every node), and the
`*.secretsNamespace.name` keys are Ichor-owned, pinned to `cilium-secrets`.
The operator runs one replica while the cluster has one node and the chart's two
from then on, counted from the Nodes that exist, and a Helm Job that keeps failing
with the same values is retried with a growing backoff.

Upgrades. A new Ichor release carries new pinned charts. Cilium is upgraded
only when the user starts a `cni` operation (`ichor upgrade cni`, or the UI),
at most one minor at a time, rolled back by Helm on failure. The cluster rollout runs that operation last, as its Cilium step.

Takeover. `ichor adopt` can make a Cilium that someone else
installed (a Helm release, the Cilium CLI, a Talos inline manifest or an
applied `helm template`) Ichor's release. Before anything is written it
checks that the live minor equals the pinned chart's minor or is one lower,
that `ipam.mode` is `kubernetes` (a `cluster-pool` Cilium is refused), that
the tunnel protocol is carried over, and that Cilium and KubePrism are
healthy on every node. The user values come from the newest Helm release
record; where there is none (a Talos inline manifest or an applied
`helm template`) Ichor's defaults are used and the diff shows what is missing. Ichor then renders the
pinned chart with the effective values and compares every object with the
live one. A difference needs `--accept-cilium-diff`, and a change to an
Ichor-owned key on the running data plane needs `--accept-cilium-changes`
(IPAM and the tunnel protocol are never accepted). The `ichor-cni` Job runs
`cilium.Apply` with `--takeover`, so Helm installs with `TakeOwnership`. Once
the rollout is Ready the foreign release record Secrets are copied into
`ichor-cilium-prior-release` in `ichor-system`, read back byte for byte, and
only then deleted (`RemoveForeignReleaseRecord`; never `helm uninstall`). An
inline manifest entry is found by content, its name is recorded in
`status.cni.takenOverInlineManifest`, and it is removed from the machine
config only after `CNIHandover` is true. There is no rollback of an adoption.

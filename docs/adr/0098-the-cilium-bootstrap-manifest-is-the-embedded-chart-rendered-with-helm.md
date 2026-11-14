# 0098. The Cilium bootstrap manifest is the embedded chart rendered with Helm

Status: Accepted · Date: 2026-10-28 · Design section: CNI · Extends: 0013, 0024

Decided: `BootstrapManifest` for the Cilium preset returns the pinned Cilium chart expanded with the preset values into plain manifests, rendered with Helm's Go SDK from a chart archive embedded in the binary, and the tunnel protocol is a preset option that defaults to Cilium's VXLAN

## Context

ADR 0013 left chart expansion "to a later unit", so `BootstrapManifest` returned only the Helm values. `ichor init --cni cilium` put that values file in `cluster.inlineManifests`, which Talos cannot apply, so no CNI was installed and the first node stayed NotReady. Some networks drop VXLAN (UDP 8472) between nodes, so the tunnel protocol is selectable and geneve is available.

## Decision

- The chart `cilium-1.18.0.tgz` (the version `ChartVersion` pins, from https://helm.cilium.io/) is checked in under `pkg/cni/cilium/chart/` and embedded with `go:embed`. `make cilium-chart` fetches it and refuses a file whose SHA-256 differs from the digest recorded in the Makefile. The embedded chart's name and version equal `ChartName` and `ChartVersion`.
- `BootstrapManifest` loads the archive, renders it with `helm.sh/helm/v3` (engine, release name `cilium`, namespace `kube-system`) and the preset values, drops hooks and NOTES, and joins the rest in Helm's install order. There is no cluster access and no network. The output is deterministic. The 1.18.0 chart has no `crds/` directory (the operator installs Cilium's CRDs itself), so none are included. The `cilium-secrets` namespace and its Roles come from the chart.
- Rendered size with kube-proxy replaced is about 58 KB, which fits in the machine config comfortably.
- `Preset.WithTunnel("vxlan"|"geneve")` sets `tunnelProtocol` in the values. The default is unset, so Cilium's VXLAN. The same values go to the Argo CD Application, so the hand-over still shows no diff (ADR 0024).

## Why

Embedding means no network at init time and the same bytes every run, which `helm template` against a live repo cannot promise. Downloading at render time was rejected for that reason. Helm v3 is used because it builds with the Kubernetes libraries Ichor already pins; v4 would pull a second set.

## Consequences

Moving to a newer Cilium means bumping `ChartVersion`, the Makefile digest and the archive together. The binary grows by the chart archive plus Helm's dependencies. `ichor init --cni-tunnel` picks the tunnel protocol and stores it in `spec.cni.tunnel` on the TalosCluster, so `ichor gitops export` reproduces it in the Argo CD Application instead of reverting to Cilium's default.

## Amendment 2026-11-03: the bootstrap renders for the release set's Kubernetes and pins one operator replica per control plane

The bootstrap render used Helm's default capabilities, a Kubernetes version older than 1.33, so the chart omitted `hostUsers` from the operator and agent pod templates. The hand-over runs real Helm on the cluster (1.36.6), which renders `hostUsers: true`. The pod template changed, the operator Deployment rolled, and on a one-node cluster the rollout could not finish (the second replica has no node and no free host port), so the hand-over stalled (ADR 0113 amendment).

- **The render targets one Kubernetes version.** `renderChart` takes its capabilities from `RenderKubernetesVersion` (`v1.36.6`, the set's Kubernetes; `pkg/release` checks they agree), so the bootstrap and the hand-over render the same templates.
- **The operator has one replica for one control plane.** `operator.replicas` is 1 when the TalosCluster has one control plane (`spec.controlPlane.count == 1`, which covers the `single` preset and single-control-plane clusters whose workers come later). Any other count keeps the chart's default of 2. Cilium's docs (`docs.cilium.io/en/stable/overview/component-overview`) say the operator "is not in the critical path for any forwarding or network policy decision" and that "a cluster will generally continue to function if the operator is temporarily unavailable"; its failures delay IPAM and the kvstore heartbeat. They recommend a high-availability setup for resilience (the BGP operation page), which a one-node cluster cannot have. The chart requires one operator pod per node through `podAntiAffinity` (chart 1.20.4 `templates/cilium-operator/deployment.yaml`), so a second replica on one node never schedules. The value is not an owned key: a user may still set `operator.replicas` in `spec.cni.cilium.values`.
- **Readback.** The release record's `operator.replicas` says whether it was made for one control plane, so an adopt readback reconstructs the same values.

The rule above covered only `count == 1`. An HA cluster has the same problem while it has one node: see ADR 0113, amendment 2026-11-04, where the replicas follow the nodes that exist.

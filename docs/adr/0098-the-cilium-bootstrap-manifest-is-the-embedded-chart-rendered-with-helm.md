# 0098. The Cilium bootstrap manifest is the embedded chart rendered with Helm

Status: Accepted · Date: 2026-10-28 · Design section: CNI · Extends: 0013, 0024

Decided: `BootstrapManifest` for the Cilium preset returns the pinned Cilium chart expanded with the preset values into plain manifests, rendered with Helm's Go SDK from a chart archive embedded in the binary, and the tunnel protocol is a preset option that defaults to Cilium's VXLAN

## Context

ADR 0013 left chart expansion "to a later unit", so `BootstrapManifest` returned only the Helm values. `ichor init --cni cilium` put that values file in `cluster.inlineManifests`, which Talos cannot apply, so no CNI was installed and the first node stayed NotReady. On the KubeVirt test harness Cilium's default VXLAN tunnel also passes no traffic between VMs, while geneve works.

## Decision

- The chart `cilium-1.18.0.tgz` (the version `ChartVersion` pins, from https://helm.cilium.io/) is checked in under `pkg/cni/cilium/chart/` and embedded with `go:embed`. `make cilium-chart` fetches it and refuses a file whose SHA-256 differs from the digest recorded in the Makefile. A test checks that the embedded chart's name and version equal `ChartName` and `ChartVersion`.
- `BootstrapManifest` loads the archive, renders it with `helm.sh/helm/v3` (engine, release name `cilium`, namespace `kube-system`) and the preset values, drops hooks and NOTES, and joins the rest in Helm's install order. There is no cluster access and no network. The output is deterministic. The 1.18.0 chart has no `crds/` directory (the operator installs Cilium's CRDs itself), so none are included. The `cilium-secrets` namespace and its Roles come from the chart.
- Rendered size with kube-proxy replaced is about 58 KB, which fits in the machine config comfortably.
- `Preset.WithTunnel("vxlan"|"geneve")` sets `tunnelProtocol` in the values. The default is unset, so Cilium's VXLAN. The same values go to the Argo CD Application, so the hand-over still shows no diff (ADR 0024).

## Why

Embedding means no network at init time and the same bytes every run, which `helm template` against a live repo cannot promise. Downloading at render time was rejected for that reason. Helm v3 is used because it builds with the Kubernetes libraries Ichor already pins; v4 would pull a second set.

## Consequences

Moving to a newer Cilium means bumping `ChartVersion`, the Makefile digest and the archive together. The binary grows by about 230 KB plus Helm's dependencies. `ichor init --cni-tunnel` picks the tunnel protocol and stores it in `spec.cni.tunnel` on the TalosCluster, so `ichor gitops export` reproduces it in the Argo CD Application instead of reverting to Cilium's default.

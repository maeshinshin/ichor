---
sidebar_order: 1
---

# What the install needs

Installing the chart and managing a cluster are two steps, and the chart asks
for little.

| Condition | What happens |
|---|---|
| Kubernetes 1.30 or later, Helm 3 | The chart declares `kubeVersion >=1.30.0-0`; Helm refuses an older cluster. |
| A working pod network | The chart's pods need one. If every node is NotReady with "cni plugin not initialized", the chart refuses to install and tells you to run `ichor bootstrap cni` first (see [A cluster with no CNI](no-cni.md)). A cluster with at least one Ready node passes. |
| Cilium as the CNI | Not needed to install. With Cilium, the chart also renders Ichor's network policies (see [Network policies](network-policies.md)). |
| Another CNI (flannel, Calico, ...) | The chart installs and runs; it does not touch your CNI and renders no network policies, so `ichor-system` is not isolated by Ichor. `ichor adopt` later refuses such a cluster unless you pass `--cni-unmanaged`, which leaves the CNI exactly as it is and refuses the Cilium commands (`ichor get cni-values`, `ichor edit cni-values`, `ichor apply cni-values`, `ichor diff cni-values`, `ichor upgrade cni`, `ichor set kube-proxy`) for the cluster. Ichor does not migrate a CNI. |
| A talosconfig, a secrets bundle, a kubeconfig | Not needed to install. The chart never takes them; they go to `ichor adopt`, which needs every node at Talos v1.14 or later (a node added later with `--add-machine` needs v1.12; sign-in cannot be turned off, and only v1.14 carries its issuer) and, for a new cluster, a version contract of v1.14 or later probed on its first control plane (see [Adopt a running cluster](../adopt-a-cluster/before-you-start.md#what-you-need)). |
| OIDC or Ichor users | Required. ichor-ui does not start without a sign-in, so the chart refuses to render it with neither: set `ichorUI.users.enabled` with `ichorUI.users.externalURL` (Ichor's own users; the install below uses the default issuer `https://localhost:8443`, reached with `ichor ui`), or `ichorUI.oidc`; or `ichorUI.enabled=false` to install without ichor-ui (see [Users and login](../users-and-login.md)). |

---
sidebar_group: "Configuration"
sidebar_order: 6
---

# API

Group `ichor.maesh.dev`, version `v1alpha1`.

| Kind | Scope | Purpose | In Git |
|---|---|---|---|
| `TalosCluster` | namespaced | Cluster-wide settings: endpoint, versions, topology, Cilium, monitoring, integrations | yes |
| `TalosMachine` | namespaced | One node: identity, role, labels, disk roles, per-node values | yes, once approved |
| `TalosConfigPatch` | namespaced | A typed config patch with a layer, priority and target | yes |
| `TalosStorage` | namespaced | Thin integration with a storage system: node selector, disk preset, evacuation settings | yes |
| `TalosEtcdBackup` | namespaced | Backup schedule, destination, retention, encryption | yes |
| `TalosOperation` | namespaced | A Job-style run of one action over the cluster or a node: a Talos, Kubernetes or Ichor upgrade, a kube-proxy switch or Cilium chart upgrade (`cni`), shutdown, CA rotation, service-account and secretbox key rotation, config rollout, node action, etcd restore | no |
| `IchorInstallation` | cluster, singleton | Ichor's own version | yes |
| `IchorUser` | namespaced | One of Ichor's own users: display name, e-mail, roles, disabled flag | yes |

There are no CRDs for installing CNI, storage or monitoring. Their Talos-side
needs are expressed on `TalosCluster`, `TalosMachine` and `TalosStorage`.

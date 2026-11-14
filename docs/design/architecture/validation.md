---
sidebar_group: "Configuration"
sidebar_order: 9
---

# Validation

Webhooks enforce, among other things: control-plane count in {1, 3, 5}; no
approving more control planes than configured; never deleting the last
control plane; no `schedulable: false` with zero workers; CNI preset is
`cilium` and immutable; Cilium values are validated and may not set Ichor-owned
keys; kube-proxy mode changes only with an explicit
annotation; disk roles that require reinstall are immutable after install;
`TalosCluster` and `TalosMachine` can only be deleted with
`ichor.maesh.dev/allow-delete: "true"` (so a prune by the user's own tooling can't cascade into
node resets); removing an Ichor finalizer, resetting a node and restoring etcd
need the override verb; Talos/Kubernetes version pairs and upgrade paths are checked, and a version the
webhook admits is never a downgrade, and never a pair the compatibility table refuses.

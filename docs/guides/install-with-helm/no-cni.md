---
sidebar_order: 8
---

# A cluster with no CNI

Its nodes are NotReady, so the chart's pods could not start and `helm install
--wait` would hang. The chart's preflight refuses to install and says so. The
sequence is:

```sh
ichor bootstrap cni --kubeconfig FILE --kube-proxy enabled|replaced [--tunnel vxlan|geneve] [--cilium-values FILE]
helm install ichor ichor/ichor --version X.Y.Z \
  --namespace ichor-system --create-namespace \
  --set ichorUI.users.enabled=true --set ichorUI.users.externalURL=https://localhost:8443
ichor create user admin --role admin --generate --bootstrap
ichor adopt --cluster NAME --node ADDR --recover-secrets --recover-from ADDR
```

- `ichor bootstrap cni` creates `ichor-system` if needed, installs the pinned
  Cilium as Ichor's own Helm release (release `cilium`, records in
  `ichor-system`), and waits for the nodes to be Ready. It asks for
  confirmation unless `--yes`. Running it again is safe.
- `--kube-proxy` is required: `enabled` if Talos runs kube-proxy, `replaced` if
  it is disabled in Talos. The command has no Talos credentials, so it cannot
  read this, and it cannot check KubePrism either. KubePrism must be enabled on
  port 7445 on every node (Talos's default); adopt checks it later.
- `ichor adopt` then finds Cilium with an Ichor release record, and checks the
  tunnel and kube-proxy mode against it.

What adopt does on a Helm install is in
[Adopt a running cluster, when Ichor was installed with Helm](../adopt-a-cluster/with-helm.md).

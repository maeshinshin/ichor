---
title: "ichor cni"
description: "Operate on the cluster's CNI settings Ichor owns"
---

## ichor cni

Operate on the cluster's CNI settings Ichor owns

### Options

```
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
  -h, --help                     help for cni
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --override                 incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --tls-server-name string   name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
```

### Options inherited from parent commands

```
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle (ADR 0127); any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor](ichor.md)	 - Manage a Talos Linux cluster through Ichor's CRDs
* [ichor cni bootstrap](ichor_cni_bootstrap.md)	 - Install Ichor's Cilium on a cluster with no CNI, before helm install ichor
* [ichor cni kube-proxy](ichor_cni_kube-proxy.md)	 - Switch kube-proxy between enabled and replaced by the CNI
* [ichor cni upgrade](ichor_cni_upgrade.md)	 - Upgrade Cilium to the chart this Ichor ships
* [ichor cni values](ichor_cni_values.md)	 - Show, edit and diff Cilium's Helm values (the user layer on the TalosCluster)


---
title: "ichor cni values"
description: "Show, edit and diff Cilium's Helm values (the user layer on the TalosCluster)"
---

## ichor cni values

Show, edit and diff Cilium's Helm values (the user layer on the TalosCluster)

### Synopsis

Cilium's Helm values are Ichor's defaults, then your layer (spec.cni.cilium.values)
merged on top, then the keys Ichor owns. Only your layer is written; the controller applies it.

Changing your layer (set, edit) needs the verb "update" on talosclusters/cilium-values, which only the
ichor-admin role holds: the chart can run privileged pods on every node. Reading and diffing need none.

### Options

```text
  -h, --help   help for values
```

### Options inherited from parent commands

```text
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle (ADR 0127); any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --ca-file string                  PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --ichor-namespace string          namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
      --local                           do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string                namespace of the Ichor resources the command names (default "ichor-system")
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --override                        incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --tls-server-name string          name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor cni](ichor_cni.md)	 - Operate on the cluster's CNI settings Ichor owns
* [ichor cni values diff](ichor_cni_values_diff.md)	 - Show how the effective values would change if FILE became your layer
* [ichor cni values edit](ichor_cni_values_edit.md)	 - Edit your layer of Cilium's values in $EDITOR, validate it, show the diff and save
* [ichor cni values get](ichor_cni_values_get.md)	 - Print your layer of Cilium's values (--layer shows the others)
* [ichor cni values set](ichor_cni_values_set.md)	 - Replace your layer of Cilium's values with a YAML file (- is stdin; an empty file clears it)


---
title: "ichor apply cni-values"
description: "Replace your layer of Cilium's values with a YAML file (- is stdin; an empty file clears it)"
---

## ichor apply cni-values

Replace your layer of Cilium's values with a YAML file (- is stdin; an empty file clears it)

### Synopsis

Cilium's Helm values are Ichor's defaults, then your layer (spec.cni.cilium.values)
merged on top, then the keys Ichor owns. Only your layer is written; the controller applies it.

Changing your layer (apply, edit) needs the verb "update" on talosclusters/cilium-values, which only the
ichor-admin role holds: the chart can run privileged pods on every node. Reading and diffing need none.

```sh
ichor apply cni-values -f FILE [flags]
```

### Examples

```sh
  ichor apply cni-values -f cilium-values.yaml
  ichor apply cni-values -f cilium-values.yaml --cluster prod
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --cluster string           the cluster (default: the only one in the namespace)
  -f, --filename string          YAML file holding your layer of the values; - reads stdin (required)
  -h, --help                     help for cni-values
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --override                 incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --tls-server-name string   name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
```

### Options inherited from parent commands

```text
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor apply](ichor_apply.md)	 - Create or update a TalosConfigPatch from a file (shows its impact first; confirms if it needs a reboot or reinstall)


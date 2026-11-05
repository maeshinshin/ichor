---
title: "ichor cluster shutdown"
description: "Stop every machine of a cluster in the order that keeps the API up longest"
---

## ichor cluster shutdown

Stop every machine of a cluster in the order that keeps the API up longest

### Synopsis

shutdown starts a shutdown operation: workers are drained and stopped first, then
every control plane. Machines stay off until you power them on; the operation
completes once they answer again. While it has not completed, every other
disruptive operation on the cluster is refused.

It shows which machines stop and in which order, and asks for confirmation unless --yes.

```
ichor cluster shutdown NAME [flags]
```

### Options

```
      --follow   after creating the operation, follow its progress (it ends at ShutDown, when every machine is down)
  -h, --help     help for shutdown
```

### Options inherited from parent commands

```
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
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

* [ichor cluster](ichor_cluster.md)	 - List or inspect clusters


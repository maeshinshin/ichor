---
title: "ichor operation"
description: "List, follow, pause, resume or delete the operations (upgrades, shutdowns) of a cluster"
---

## ichor operation

List, follow, pause, resume or delete the operations (upgrades, shutdowns) of a cluster

### Synopsis

An operation is the record of one machine-by-machine action: a Talos,
Kubernetes or Ichor upgrade, a kube-proxy switch, or a cluster shutdown. These
commands read and change the records; `ichor upgrade`, `ichor cni` and `ichor cluster shutdown` start them.

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
  -h, --help                     help for operation
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

* [ichor](ichor.md)	 - Manage a Talos Linux cluster through Ichor's CRDs
* [ichor operation delete](ichor_operation_delete.md)	 - Delete an operation record
* [ichor operation get](ichor_operation_get.md)	 - Show one operation: phase, failure reason and the state of each machine
* [ichor operation list](ichor_operation_list.md)	 - List operations
* [ichor operation pause](ichor_operation_pause.md)	 - Stop the operation from advancing any further machine (the cluster lock stays held, nothing is undone)
* [ichor operation resume](ichor_operation_resume.md)	 - Let a paused operation continue; one paused by a failed step runs that step again, so fix the cause first


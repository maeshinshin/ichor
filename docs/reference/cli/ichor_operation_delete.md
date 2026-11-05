---
title: "ichor operation delete"
description: "Delete an operation record"
---

## ichor operation delete

Delete an operation record

### Synopsis

delete removes the record and, when it has not finished, lets the controller release
the cluster lock. It does not stop a step already sent to a machine and it does not
roll anything back. It asks for confirmation unless --yes.

A ca-rotation or key-rotation deleted before it made the new CA or key issue is abandoned: the
new material leaves the bundle, the lock is released and the machines are rendered from the old
material again (the nodes then report a pending change; 'ichor config apply' drops what they
still accept). Later in the rotation the delete is refused: let it finish.

```
ichor operation delete NAME [flags]
```

### Options

```
  -h, --help   help for delete
```

### Options inherited from parent commands

```
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

* [ichor operation](ichor_operation.md)	 - List, follow, pause, resume or delete the operations (upgrades, shutdowns) of a cluster


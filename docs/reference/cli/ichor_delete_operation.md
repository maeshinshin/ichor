---
title: "ichor delete operation"
description: "Delete an operation record"
---

## ichor delete operation

Delete an operation record

### Synopsis

delete operation removes the record and, when it has not finished, lets the controller release
the cluster lock. It does not stop a step already sent to a machine and it does not
roll anything back. It asks for confirmation unless --yes.

A ca-rotation or key-rotation deleted before it made the new CA or key issue is abandoned: the
new material leaves the bundle, the lock is released and the machines are rendered from the old
material again (the nodes then report a pending change; 'ichor rollout config' drops what they
still accept). Later in the rotation the delete is refused: let it finish.

```sh
ichor delete operation NAME [flags]
```

### Examples

```sh
  ichor delete operation upgrade-talos-x7k2p
  ichor delete operation upgrade-talos-x7k2p --yes
```

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

* [ichor delete](ichor_delete.md)	 - Delete a node, patch, operation or user


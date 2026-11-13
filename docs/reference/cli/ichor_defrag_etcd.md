---
title: "ichor defrag etcd"
description: "Defragment the etcd member of a control plane node"
---

## ichor defrag etcd

Defragment the etcd member of a control plane node

### Synopsis

Defragment the etcd member of a control plane node.

It shows the plan and asks for confirmation unless --yes. For delete node the typed node name
(the TalosMachine name, or --confirm NAME) is the confirmation and replaces the question; --yes alone does not. The CLI only writes the operation
(or deletes the node); the controller does the work. Follow it with --follow.

```sh
ichor defrag etcd --node CONTROL_PLANE [flags]
```

### Examples

```sh
  ichor defrag etcd --node cp-2
  ichor defrag etcd --node cp-1 --allow-leader
```

### Options

```text
      --allow-leader             allow defragmenting the etcd leader (writes pause for the whole cluster meanwhile)
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --follow                   after creating the operation, follow its progress until it finishes
  -h, --help                     help for etcd
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --node string              the control plane node (required)
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

* [ichor defrag](ichor_defrag.md)	 - Defragment etcd on a control plane node


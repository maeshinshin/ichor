---
title: "ichor node reboot"
description: "Reboot a node gracefully: cordon, drain, reboot, wait until it is Ready again"
---

## ichor node reboot

Reboot a node gracefully: cordon, drain, reboot, wait until it is Ready again

### Synopsis

Reboot a node gracefully: cordon, drain, reboot, wait until it is Ready again.

It shows the plan and asks for confirmation unless --yes. For reset and remove the typed node name
(or --confirm NAME) is the confirmation and replaces the question; --yes alone does not. The CLI only writes the operation
(or deletes the machine); the controller does the work. Follow it with --follow.

```
ichor node reboot NODE [flags]
```

### Options

```
      --follow   after creating the operation, follow its progress until it finishes
  -h, --help     help for reboot
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

* [ichor node](ichor_node.md)	 - Show a node, tail its logs (needs ichor-ui), or reboot, shut down, reset or remove it


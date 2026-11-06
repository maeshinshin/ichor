---
title: "ichor upgrade talos"
description: "Upgrade Talos on every machine of a cluster (each machine reboots)"
---

## ichor upgrade talos

Upgrade Talos on every machine of a cluster (each machine reboots)

### Synopsis

Upgrade Talos on every machine of a cluster (each machine reboots).

Without VERSION it uses the recommended version (see `--list`). It shows which machines restart
and in which order, and asks for confirmation unless --yes.

```sh
ichor upgrade talos [VERSION] [flags]
```

### Options

```text
      --cluster string          the cluster (default: the only one in the namespace)
      --follow                  after creating the operation, follow its progress until it finishes
  -h, --help                    help for talos
      --list                    print the versions the cluster can move to, the recommended one first, and exit
      --max-unavailable int32   workers handled at once (default 1)
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

* [ichor upgrade](ichor_upgrade.md)	 - Upgrade the cluster's Talos or Kubernetes, or Ichor itself


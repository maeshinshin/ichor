---
title: "ichor cluster talosconfig"
description: "Write a short-lived os:reader talosconfig for talosctl"
---

## ichor cluster talosconfig

Write a short-lived os:reader talosconfig for talosctl

### Synopsis

talosconfig writes a talosconfig (mode 0600, content never printed) whose client certificate carries
the Talos role os:reader: talosctl can read a node (get members, logs, dmesg, processes) and cannot change
it (reboot, upgrade and reset are refused by the node).

It lives one hour by default; --ttl asks for fewer or up to 12h0m0s. The key is made on
this machine and never leaves it: the controller signs its public half. You need the Kubernetes RBAC
verb "download" on talosclusters/talosconfig-reader (ichor-operator and ichor-admin hold it; ichor-viewer
does not), and no access to the secrets bundle. Each request is recorded as an Event on the cluster that
names you.

The Talos endpoints are the cluster's control-plane machines (or --talos-endpoints, or your current
talosconfig's). The file is written next to your current talosconfig with the suffix .ichor-reader (never over
it), unless --out says otherwise. Use it with: TALOSCONFIG=<file> talosctl get members

```
ichor cluster talosconfig NAME [flags]
```

### Options

```
  -h, --help                      help for talosconfig
      --out string                where to write the talosconfig (default: your current talosconfig's path plus .ichor-reader)
      --talos-endpoints strings   the Talos API endpoints for the talosconfig (default: the cluster's control-plane machines, else your current talosconfig's)
      --ttl duration              how long the talosconfig is valid; the default is 1h0m0s and the maximum 12h0m0s
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


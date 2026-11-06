---
title: "ichor cluster discovery"
description: "List, add or remove the addresses and ranges Ichor scans for new machines"
---

## ichor cluster discovery

List, add or remove the addresses and ranges Ichor scans for new machines

### Options

```text
      --cluster string   TalosCluster (in --namespace) to edit; optional when the namespace has exactly one
  -h, --help             help for discovery
```

### Options inherited from parent commands

```text
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
* [ichor cluster discovery add](ichor_cluster_discovery_add.md)	 - Scan one more IPv4 address or range (/24 or smaller) for machines in maintenance mode
* [ichor cluster discovery list](ichor_cluster_discovery_list.md)	 - List the addresses and ranges the cluster scans
* [ichor cluster discovery remove](ichor_cluster_discovery_remove.md)	 - Stop scanning an address or range (machines already found stay)


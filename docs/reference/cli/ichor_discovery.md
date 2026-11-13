---
title: "ichor discovery"
description: "List, add or remove the addresses and ranges Ichor scans for new nodes"
---

## ichor discovery

List, add or remove the addresses and ranges Ichor scans for new nodes

```sh
ichor discovery [command]
```

### Examples

```sh
  ichor discovery list
  ichor discovery add 192.168.10.0/24
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --cluster string           TalosCluster (in --namespace) to edit; optional when the namespace has exactly one
  -h, --help                     help for discovery
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
* [ichor discovery add](ichor_discovery_add.md)	 - Scan one more IPv4 address or range (/24 or smaller) for nodes in maintenance mode
* [ichor discovery list](ichor_discovery_list.md)	 - List the addresses and ranges the cluster scans
* [ichor discovery remove](ichor_discovery_remove.md)	 - Stop scanning an address or range (nodes already found stay)


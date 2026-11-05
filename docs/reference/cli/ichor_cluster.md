---
title: "ichor cluster"
description: "List or inspect clusters"
---

## ichor cluster

List or inspect clusters

### Options

```
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
  -h, --help                     help for cluster
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --override                 incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --tls-server-name string   name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
```

### Options inherited from parent commands

```
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle (ADR 0127); any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor](ichor.md)	 - Manage a Talos Linux cluster through Ichor's CRDs
* [ichor cluster bundle-export](ichor_cluster_bundle-export.md)	 - Save the cluster's current secrets bundle locally and acknowledge it after a CA rotation
* [ichor cluster credentials](ichor_cluster_credentials.md)	 - Write a kubeconfig and a talosconfig for the cluster's admin, from its secrets bundle
* [ichor cluster discovery](ichor_cluster_discovery.md)	 - List, add or remove the addresses and ranges Ichor scans for new machines
* [ichor cluster get](ichor_cluster_get.md)	 - Show one cluster: versions, conditions, rollout and hand-over state
* [ichor cluster list](ichor_cluster_list.md)	 - List clusters
* [ichor cluster rotate-ca](ichor_cluster_rotate-ca.md)	 - Replace the cluster's certificate authorities (Talos API, Kubernetes API, and optionally etcd and aggregator)
* [ichor cluster rotate-keys](ichor_cluster_rotate-keys.md)	 - Replace the cluster's service-account signing key and secretbox (encryption at rest) key
* [ichor cluster shutdown](ichor_cluster_shutdown.md)	 - Stop every machine of a cluster in the order that keeps the API up longest
* [ichor cluster status](ichor_cluster_status.md)	 - Show a cluster's versions, endpoint and CNI in a few lines
* [ichor cluster talosconfig](ichor_cluster_talosconfig.md)	 - Write a short-lived os:reader talosconfig for talosctl


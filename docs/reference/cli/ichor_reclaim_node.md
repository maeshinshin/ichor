---
title: "ichor reclaim node"
description: "Reset a configured Talos node of another cluster back to maintenance mode"
---

## ichor reclaim node

Reset a configured Talos node of another cluster back to maintenance mode

### Synopsis

Reclaim a node that already runs a configured Talos, so that Ichor can join it.

The node is inspected with YOUR talosconfig (read from --talosconfig, used
only for this command, never stored and never printed). Ichor refuses a node
that is already in an Ichor cluster, and a control plane whose removal would
break its cluster's etcd quorum (override: --force plus the cluster name typed
a second time). The reset is graceful (cordon, drain, leave etcd) when the
foreign cluster is reachable; for a dead cluster pass --force-ungraceful.

The reset wipes only the STATE and EPHEMERAL partitions and reboots.
User disks are not touched; they are listed, and encrypted ones are called out
because their keys may be gone.

Without --talosconfig, give --cluster NAME: the node is then an orphan of that Ichor
cluster (its TalosMachine was deleted, which wipes on remove, so it still
carries the cluster's configuration) and the controller wipes it with the credentials Ichor
already holds for that cluster, deletes its Kubernetes Node and, afterwards, this command
registers it as a pending node. A node that still has an approved TalosMachine is
refused: use "ichor delete node". Needs the "reclaim" verb on machinereclaims.

Consent is the node's UUID, typed at the prompt or given as --confirm. --yes
does not satisfy it.

Afterwards the node comes back in maintenance mode with the same UUID. With
--cluster NAME its address is added to that cluster's spec.discovery.addresses
so that Ichor registers it as a pending node. A pending TalosMachine of the same
UUID that an earlier reclaim left is kept: on its next pass (within about five
minutes) discovery re-probes the node and records the new maintenance certificate
and disks on that same node (Event MaintenanceIdentityRefreshed); approve it after that.

```sh
ichor reclaim node ADDRESS (--talosconfig FILE | --cluster NAME) [flags]
```

### Examples

```sh
  ichor reclaim node 192.168.10.21 --talosconfig old-cluster/talosconfig
  ichor reclaim node 192.168.10.21 --cluster prod
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --cluster string           TalosCluster (in --namespace) whose discovery addresses get the machine's address afterwards
      --confirm string           the machine's UUID, for non-interactive use (--yes does not replace it)
      --confirm-cluster string   the foreign cluster's name, the second confirmation --force needs
      --context string           talosconfig context to use (default: the file's own)
      --force                    allow a control plane whose removal breaks etcd quorum, or is the last one
      --force-ungraceful         reset without cordon, drain and etcd leave, for a dead cluster
  -h, --help                     help for node
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --override                 incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --tls-server-name string   name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
      --wait duration            how long to wait for maintenance mode (0 to not wait) (default 10m0s)
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

* [ichor reclaim](ichor_reclaim.md)	 - Reset a configured Talos node of another cluster back to maintenance mode


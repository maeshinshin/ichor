---
title: "ichor node"
description: "Show a node, tail its logs (needs ichor-ui), or reboot, shut down, reset or remove it"
---

## ichor node

Show a node, tail its logs (needs ichor-ui), or reboot, shut down, reset or remove it

### Options

```
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
  -h, --help                     help for node
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --override                 incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --tls-server-name string   name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
```

### Options inherited from parent commands

```
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor](ichor.md)	 - Manage a Talos Linux cluster through Ichor's CRDs
* [ichor node get](ichor_node_get.md)	 - Show a node's hardware, disks, network interfaces, usage and Kubernetes status
* [ichor node logs](ichor_node_logs.md)	 - Print a node's kernel or service log; --follow keeps streaming
* [ichor node pcap](ichor_node_pcap.md)	 - Capture packets on a node's interface into a pcap file, for a bounded time and size
* [ichor node reboot](ichor_node_reboot.md)	 - Reboot a node gracefully: cordon, drain, reboot, wait until it is Ready again
* [ichor node remove](ichor_node_remove.md)	 - Remove a node from the cluster: drain it and, for a control plane, remove its etcd member
* [ichor node reset](ichor_node_reset.md)	 - Remove a node from the cluster and wipe it (its STATE and EPHEMERAL partitions, or the whole disk with --wipe-mode); needs the override permission
* [ichor node restart-service](ichor_node_restart-service.md)	 - Restart a Talos service on a node (kubelet, containerd or cri)
* [ichor node shutdown](ichor_node_shutdown.md)	 - Shut a node down; it stays off until you power it on
* [ichor node support-bundle](ichor_node_support-bundle.md)	 - Download a zip of a node's (or every node of a cluster's) logs, processes, mounts and Talos resources


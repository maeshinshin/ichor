---
title: "ichor delete node"
description: "Delete a node from the cluster: drain it, wipe it (STATE and EPHEMERAL, or more with --wipe-mode), power it off (or reboot it with --reboot) and delete its Kubernetes Node; needs the override permission"
---

## ichor delete node

Delete a node from the cluster: drain it, wipe it (STATE and EPHEMERAL, or more with --wipe-mode), power it off (or reboot it with --reboot) and delete its Kubernetes Node; needs the override permission

### Synopsis

Delete a node from the cluster: drain it, wipe it (STATE and EPHEMERAL, or more with --wipe-mode), power it off (or reboot it with --reboot) and delete its Kubernetes Node; needs the override permission.

It shows the plan and asks for confirmation unless --yes. For delete node the typed node name
(the TalosMachine name, or --confirm NAME) is the confirmation and replaces the question; --yes alone does not. The CLI only writes the operation
(or deletes the node); the controller does the work. Follow it with --follow.

delete node --wipe-mode chooses what the wipe erases: system-partitions (the default) wipes the STATE and
EPHEMERAL partitions, so the node boots its installed system again from a fresh state; system-disk
wipes the whole system disk, boot and EFI included, so the node starts from its boot image (ISO or PXE)
and installs afresh, which is how a node is moved onto SecureBoot images (docs/guides/hardening.md);
all erases the same whole system disk as system-disk and never a user disk. system-disk and all are irreversible.

```sh
ichor delete node NAME [flags]
```

### Examples

```sh
  ichor delete node worker-3
  ichor delete node worker-3 --confirm worker-3 --wipe-mode system-disk --override
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --confirm string           the node name (the TalosMachine name, not the hostname), to go on without being asked to type it
      --follow                   after creating the operation, follow its progress until it finishes
  -h, --help                     help for node
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --override                 incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --reboot                   after the wipe reboot the node into maintenance mode, so it can be added again, instead of powering it off
      --tls-server-name string   name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
      --wipe-mode string         what to wipe: system-partitions (STATE and EPHEMERAL), system-disk (the whole system disk) or all (the whole system disk, as system-disk; user disks are not wiped) (default "system-partitions")
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


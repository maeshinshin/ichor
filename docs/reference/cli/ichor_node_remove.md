---
title: "ichor node remove"
description: "Remove a node from the cluster: drain it, wipe it (STATE and EPHEMERAL, or more with --wipe-mode), power it off (or reboot it with --reboot) and delete its Kubernetes Node; needs the override permission"
---

## ichor node remove

Remove a node from the cluster: drain it, wipe it (STATE and EPHEMERAL, or more with --wipe-mode), power it off (or reboot it with --reboot) and delete its Kubernetes Node; needs the override permission

### Synopsis

Remove a node from the cluster: drain it, wipe it (STATE and EPHEMERAL, or more with --wipe-mode), power it off (or reboot it with --reboot) and delete its Kubernetes Node; needs the override permission.

It shows the plan and asks for confirmation unless --yes. For remove the typed machine name
(the TalosMachine name, or --confirm NAME) is the confirmation and replaces the question; --yes alone does not. The CLI only writes the operation
(or deletes the machine); the controller does the work. Follow it with --follow.

remove --wipe-mode chooses what the wipe erases: system-partitions (the default) wipes the STATE and
EPHEMERAL partitions, so the node boots its installed system again from a fresh state; system-disk
wipes the whole system disk, boot and EFI included, so the node starts from its boot image (ISO or PXE)
and installs afresh, which is how a node is moved onto SecureBoot images (docs/guides/hardening.md);
all also wipes every user disk, destroying any data on them. system-disk and all are irreversible.

```sh
ichor node remove NODE [flags]
```

### Options

```text
      --confirm string     the machine name (the TalosMachine name, not the hostname), to go on without being asked to type it
      --follow             after creating the operation, follow its progress until it finishes
  -h, --help               help for remove
      --reboot             after the wipe reboot the node into maintenance mode, so it can be added again, instead of powering it off
      --wipe-mode string   what to wipe: system-partitions (STATE and EPHEMERAL), system-disk (the whole system disk) or all (system disk and every user disk) (default "system-partitions")
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

* [ichor node](ichor_node.md)	 - Show a node, tail its logs (needs ichor-ui), or reboot, shut down or remove it


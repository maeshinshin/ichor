---
title: "ichor machine approve"
description: "Approve a pending machine so the controller starts configuring it"
---

## ichor machine approve

Approve a pending machine so the controller starts configuring it

### Synopsis

approve sets the role (and optionally node labels, a hostname and the
install disk, network link and disk roles) of a discovered machine and marks it approved; the controller then renders and
applies its configuration. It is refused for a machine whose identity is
weak (missing UUID, serial number or permanent MAC) and for one already
approved -- by ichor-ui, or, when ichor-ui is unreachable, by the same
checks running in this process. It asks for confirmation unless --yes.

```sh
ichor machine approve NAME --role controlplane|worker [flags]
```

### Options

```text
      --address strings           static address with prefix length, 192.168.1.10/24 (repeatable; needs --nic)
      --dns strings               DNS server address (repeatable; needs --nic)
      --ephemeral-disk string     put EPHEMERAL on its own disk: "wwid:ID", "serial:SERIAL", a /dev/disk/by-id/ path, a bus path such as /pci0000:00/..., or a device path such as /dev/sda
      --gateway string            default gateway for the static address
  -h, --help                      help for approve
      --hostname string           hostname to set
      --install-disk string       system disk Talos installs onto: "wwid:ID", "serial:SERIAL", a /dev/disk/by-id/ path, a bus path such as /pci0000:00/..., or a device path such as /dev/sda
      --install-image string      installer image (default: the booted schematic's; the cluster's SecureBoot installer when spec.security.secureBoot.mode is Preferred or Required)
      --label stringToString      node label KEY=VALUE (repeatable) (default [])
      --nic string                primary network link, by its permanent MAC (aa:bb:cc:dd:ee:ff); alone it means DHCP
      --role string               controlplane or worker (required)
      --user-volume stringArray   NAME=DISK[,size=200GiB] user volume on a disk (repeatable)
      --vlan int32                VLAN ID (1-4094) to put the addressing on (needs --nic)
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

* [ichor machine](ichor_machine.md)	 - List, inspect, approve or reject machines


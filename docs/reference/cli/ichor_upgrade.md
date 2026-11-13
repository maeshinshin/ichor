---
title: "ichor upgrade"
description: "Upgrade the cluster's Talos, Kubernetes or Cilium, or Ichor itself"
---

## ichor upgrade

Upgrade the cluster's Talos, Kubernetes or Cilium, or Ichor itself

### Synopsis

Upgrade one component of the cluster, or Ichor itself. Talos, Kubernetes and Cilium move
independently: the release set of the Ichor that runs the cluster is the version of a
first install, and any valid target is allowed after that.

`ichor upgrade talos` and `ichor upgrade kubernetes` offer the versions the cluster can
move to (`--list`). Without a VERSION they use the recommended one, the release set's version
when it is a valid upgrade, else the newest patch of the running minor. With a VERSION they move to it.
`ichor upgrade ichor` moves Ichor itself and `ichor upgrade cni` moves Cilium to the chart this Ichor ships. Before the installer Job runs, the controller pins the
installer image to a digest and checks its signature; --skip-signature-check is the opt-out for
images nobody signs.

Each upgrade changes the version in the spec that names it and then starts the
operation that rolls it out node by node: control planes one at a time,
then workers. The spec is changed first, because the operation reads its target
from it; if the operation cannot be created (no permission, another operation
running) nothing was changed. Run the command again to retry: a spec that
already names the version is not written twice.

```sh
ichor upgrade [flags]
```

### Examples

```sh
  ichor upgrade talos --list
  ichor upgrade kubernetes v1.36.3 --cluster prod --follow
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
  -h, --help                     help for upgrade
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
* [ichor upgrade cni](ichor_upgrade_cni.md)	 - Upgrade Cilium to the chart this Ichor ships
* [ichor upgrade ichor](ichor_upgrade_ichor.md)	 - Upgrade Ichor itself (IchorInstallation.spec.version)
* [ichor upgrade kubernetes](ichor_upgrade_kubernetes.md)	 - Upgrade Kubernetes on a cluster (no reboot)
* [ichor upgrade talos](ichor_upgrade_talos.md)	 - Upgrade Talos on every node of a cluster (each node reboots)


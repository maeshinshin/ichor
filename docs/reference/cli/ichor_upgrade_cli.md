---
title: "ichor upgrade cli"
description: "Replace this ichor binary with a verified release"
---

## ichor upgrade cli

Replace this ichor binary with a verified release

### Synopsis

Download a release of the ichor binary from its GitHub release and replace this one with it.

Without VERSION the newest release is used. The checksums file of the release is verified against its
Sigstore bundle (the identity of the release workflow of that tag), the archive against the checksums, and
only then is the binary written next to this one and renamed over it. Any mismatch stops the command;
there is no way to skip the check. A development build is never replaced. --check shows what would be
replaced and changes nothing. It asks for confirmation unless --yes.

The command reaches ghcr.io (to find the newest release) and github.com (the release assets); on a machine
that cannot, it prints the steps to do the same by hand. It does not read a cluster.

```sh
ichor upgrade cli [VERSION] [flags]
```

### Examples

```sh
  ichor upgrade cli --check
  ichor upgrade cli v0.3.0 --yes
```

### Options

```text
      --check   show which release would replace this binary and change nothing
  -h, --help    help for cli
```

### Options inherited from parent commands

```text
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --ca-file string                  PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --ichor-namespace string          namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
      --local                           do not use ichor-ui; compute from the Kubernetes API directly
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --override                        incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --tls-server-name string          name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor upgrade](ichor_upgrade.md)	 - Upgrade the cluster's Talos, Kubernetes or Cilium, or Ichor itself


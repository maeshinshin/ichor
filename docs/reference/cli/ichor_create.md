---
title: "ichor create"
description: "Create a user, or write credentials for talosctl and kubectl"
---

## ichor create

Create a user, or write credentials for talosctl and kubectl

### Synopsis

create makes an Ichor user, or writes the files a person needs to reach a cluster: the admin
credentials (a kubeconfig and a talosconfig), a short-lived read-only talosconfig, or your own kubeconfig.

### Examples

```sh
  ichor create user alice --role operator --generate
  ichor create talosconfig prod
```

### Options

```text
  -h, --help   help for create
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
* [ichor create credentials](ichor_create_credentials.md)	 - Write a kubeconfig and a talosconfig for the cluster's admin, from its secrets bundle
* [ichor create kubeconfig](ichor_create_kubeconfig.md)	 - Download your own kubeconfig (it holds no secret: it runs `ichor auth token`)
* [ichor create talosconfig](ichor_create_talosconfig.md)	 - Write a short-lived os:reader talosconfig for talosctl
* [ichor create user](ichor_create_user.md)	 - Create a user and set its first password (it must be changed at the first sign-in)


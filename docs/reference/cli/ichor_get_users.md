---
title: "ichor get users"
description: "List users"
---

## ichor get users

List users

```sh
ichor get users [flags]
```

### Examples

```sh
  ichor get users --issuer https://ichor.example.com
```

### Options

```text
      --ca-file string   PEM file of the CA that signed the issuer's TLS certificate, added to the system roots (env ICHOR_CA_FILE)
  -h, --help             help for users
      --issuer string    external https URL of ichor-ui, which is Ichor's OIDC issuer (env ICHOR_ISSUER)
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

* [ichor get](ichor_get.md)	 - List objects, or show a cluster in a few lines


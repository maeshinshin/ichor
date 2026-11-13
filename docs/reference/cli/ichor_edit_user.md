---
title: "ichor edit user"
description: "Change a user's display name, e-mail, roles or disabled flag (only the flags you pass)"
---

## ichor edit user

Change a user's display name, e-mail, roles or disabled flag (only the flags you pass)

```sh
ichor edit user NAME [flags]
```

### Examples

```sh
  ichor edit user alice --role admin
  ichor edit user alice --disabled=true
```

### Options

```text
      --ca-file string        PEM file of the CA that signed the issuer's TLS certificate, added to the system roots (env ICHOR_CA_FILE)
      --disabled              disable (true) or enable (false) the user
      --display-name string   new display name
      --email string          new e-mail address
  -h, --help                  help for user
      --issuer string         external https URL of ichor-ui, which is Ichor's OIDC issuer (env ICHOR_ISSUER)
      --role strings          replace the roles with these; repeatable
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

* [ichor edit](ichor_edit.md)	 - Edit an object in $EDITOR, or change a user's fields


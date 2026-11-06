---
title: "ichor user create"
description: "Create a user and set its first password (it must be changed at the first sign-in)"
---

## ichor user create

Create a user and set its first password (it must be changed at the first sign-in)

```sh
ichor user create NAME [flags]
```

### Options

```text
      --bootstrap             write the user and its password hash with your kubeconfig's own rights instead of through ichor-ui (for the first admin of an existing install)
      --display-name string   display name
      --email string          e-mail address (informational)
      --generate              generate a password; it is printed once to stderr and stored nowhere
  -h, --help                  help for create
      --password-stdin        read the password from the first line of stdin (no prompt)
      --role strings          role (viewer, operator, admin or your own); repeatable
```

### Options inherited from parent commands

```text
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --ca-file string                  PEM file of the CA that signed the issuer's TLS certificate, added to the system roots (env ICHOR_CA_FILE)
      --issuer string                   external https URL of ichor-ui, which is Ichor's OIDC issuer (env ICHOR_ISSUER)
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor user](ichor_user.md)	 - Manage Ichor's own users (list, create, update, delete, set-password, revoke-sessions)


---
title: "ichor user update"
description: "Change a user's display name, e-mail, roles or disabled flag (only the flags you pass)"
---

## ichor user update

Change a user's display name, e-mail, roles or disabled flag (only the flags you pass)

```
ichor user update NAME [flags]
```

### Options

```
      --disabled              disable (true) or enable (false) the user
      --display-name string   new display name
      --email string          new e-mail address
  -h, --help                  help for update
      --role strings          replace the roles with these; repeatable
```

### Options inherited from parent commands

```
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle (ADR 0127); any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --ca-file string                  PEM file of the CA that signed the issuer's TLS certificate, added to the system roots (env ICHOR_CA_FILE)
      --issuer string                   external https URL of ichor-ui, which is Ichor's OIDC issuer (env ICHOR_ISSUER)
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor user](ichor_user.md)	 - Manage Ichor's own users (list, create, update, delete, set-password, revoke-sessions)


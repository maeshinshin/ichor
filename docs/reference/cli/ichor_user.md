---
title: "ichor user"
description: "Set a user's password or sign a user out everywhere"
---

## ichor user

Set a user's password or sign a user out everywhere

### Synopsis

Manage the users of Ichor's own login. These commands talk to
ichor-ui at --issuer over HTTPS with your login (run `ichor auth login --issuer URL --audience ichor-ui`
or let the first command open the browser). Use --ca-file for a private CA.

See also: ichor get users, ichor describe user, ichor create user, ichor edit user, ichor delete user.

To create the very first admin on an existing install, use
  ichor create user NAME --role admin --generate --bootstrap
which needs no login: it writes the user with your kubeconfig's own rights.

```sh
ichor user [flags]
```

### Examples

```sh
  ichor user set-password alice --generate
  ichor user revoke-sessions alice
```

### Options

```text
      --ca-file string   PEM file of the CA that signed the issuer's TLS certificate, added to the system roots (env ICHOR_CA_FILE)
  -h, --help             help for user
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

* [ichor](ichor.md)	 - Manage a Talos Linux cluster through Ichor's CRDs
* [ichor user revoke-sessions](ichor_user_revoke-sessions.md)	 - Sign a user out everywhere (kubectl tokens last at most the token lifetime)
* [ichor user set-password](ichor_user_set-password.md)	 - Set a user's password as an administrator (the user must change it at the next sign-in)


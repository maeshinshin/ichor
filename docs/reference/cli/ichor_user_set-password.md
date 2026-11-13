---
title: "ichor user set-password"
description: "Set a user's password as an administrator (the user must change it at the next sign-in)"
---

## ichor user set-password

Set a user's password as an administrator (the user must change it at the next sign-in)

```sh
ichor user set-password NAME [flags]
```

### Examples

```sh
  ichor user set-password alice --generate
  echo 'correct horse' | ichor user set-password alice --password-stdin
```

### Options

```text
      --generate         generate a password; it is printed once to stderr and stored nowhere
  -h, --help             help for set-password
      --password-stdin   read the password from the first line of stdin (no prompt)
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

* [ichor user](ichor_user.md)	 - Set a user's password or sign a user out everywhere


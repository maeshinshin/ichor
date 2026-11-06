---
title: "ichor auth login"
description: "Log in through the browser (authorization code + PKCE, loopback redirect); the default audience ichor-kube is the login kubectl needs when it has no terminal"
---

## ichor auth login

Log in through the browser (authorization code + PKCE, loopback redirect); the default audience ichor-kube is the login kubectl needs when it has no terminal

```sh
ichor auth login [flags]
```

### Options

```text
  -h, --help         help for login
      --no-browser   print the login URL instead of opening a browser
```

### Options inherited from parent commands

```text
      --audience ichor auth login       token audience: ichor-kube (the kube-apiserver, default; ichor auth login once lets kubectl run without a terminal) or ichor-ui (ichor-ui itself) (default "ichor-kube")
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --ca-file string                  PEM file of the CA that signed the issuer's TLS certificate, added to the system roots (env ICHOR_CA_FILE)
      --client-id string                OIDC client id (default "ichor-cli")
      --issuer string                   Ichor OIDC issuer URL, the external https URL of ichor-ui (required; env ICHOR_ISSUER)
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor auth](ichor_auth.md)	 - Log in to Ichor's OIDC issuer and act as a kubectl credential plugin


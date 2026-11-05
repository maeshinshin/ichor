---
title: "ichor auth"
description: "Log in to Ichor's OIDC issuer and act as a kubectl credential plugin"
---

## ichor auth

Log in to Ichor's OIDC issuer and act as a kubectl credential plugin

### Synopsis

Log in to Ichor's own OIDC issuer with the browser, and print
short-lived ID tokens for kubectl. The login cache lives in ~/.ichor/auth
(directory 0700, files 0600) and holds a refresh token; the kubeconfig itself
holds no secret.

### Options

```
      --audience ichor auth login   token audience: ichor-kube (the kube-apiserver, default; ichor auth login once lets kubectl run without a terminal) or ichor-ui (ichor-ui itself) (default "ichor-kube")
      --ca-file string              PEM file of the CA that signed the issuer's TLS certificate, added to the system roots (env ICHOR_CA_FILE)
      --client-id string            OIDC client id (default "ichor-cli")
  -h, --help                        help for auth
      --issuer string               Ichor OIDC issuer URL, the external https URL of ichor-ui (required; env ICHOR_ISSUER)
```

### Options inherited from parent commands

```
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor](ichor.md)	 - Manage a Talos Linux cluster through Ichor's CRDs
* [ichor auth kubeconfig](ichor_auth_kubeconfig.md)	 - Download your own kubeconfig (it holds no secret: it runs `ichor auth token`)
* [ichor auth login](ichor_auth_login.md)	 - Log in through the browser (authorization code + PKCE, loopback redirect); the default audience ichor-kube is the login kubectl needs when it has no terminal
* [ichor auth logout](ichor_auth_logout.md)	 - Revoke and delete every cached login for the issuer (all audiences), or only --audience's
* [ichor auth status](ichor_auth_status.md)	 - Show who you are signed in as and when the cached tokens expire (prints no token)
* [ichor auth token](ichor_auth_token.md)	 - Print an ExecCredential for kubectl (client-go exec plugin)


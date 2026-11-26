---
title: "ichor create credentials"
description: "Write a kubeconfig and a talosconfig for the cluster's admin, from its secrets bundle"
---

## ichor create credentials

Write a kubeconfig and a talosconfig for the cluster's admin, from its secrets bundle

### Synopsis

credentials writes two files and never prints their contents (mode 0600):

  a kubeconfig whose CA bundle holds the Kubernetes CA(s) and whose client certificate is signed by
  the newest Kubernetes CA, and
  a talosconfig whose CA bundle holds the Talos API CA(s) and whose os:admin client certificate is
  signed by the newest Talos API CA.

By default the client certificates live a year, which is also the maximum; --ttl asks for fewer
hours and --read-only makes the talosconfig os:reader instead of os:admin, for access that cannot
change a node.

The Kubernetes server is --kube-server, else the cluster's endpoint (https://ADDRESS:6443: the VIP when the
endpoint is a vip), with a warning when --kube-server names another host. Your current kubeconfig's context
names are kept; with --kube-server no current kubeconfig is read at all. The Talos endpoints are the
cluster's control-plane nodes (or --talos-endpoints, or your current talosconfig). The files are
written next to your current kubeconfig and talosconfig with the suffix .ichor-new
(never over them), unless --kubeconfig-out and --talosconfig-out say otherwise.

By default the CAs are read from the cluster's secrets bundle Secret. Reading the secrets bundle Secret needs get on that one Secret in --ichor-namespace. Bind the
Role ichor-bundle-secret-role (installed when the chart's rbac.bundleSecret.names lists the Secret) to
yourself with a RoleBinding in that namespace; it grants get on the named Secrets and nothing else. While a CA rotation is under way
the files trust the old and the new CAs and are signed by the new one, which every node accepts from
the moment the rotation waits for credentials: they keep working after the old CA is dropped.

With --bundle FILE (the output of 'ichor export bundle') the CAs come from the file, not the cluster,
so an unreachable cluster does not stop it. The bundle plus --kube-server URL and --talos-endpoints is
all it needs: no kubeconfig or talosconfig has to exist. Without --kube-server it still takes the
server from your current kubeconfig, since a bundle holds no endpoint, so run it where that kubeconfig and context exist. A bundle
saved before a rotation names CAs that no node accepts any more.

With --acknowledge the command also tells a waiting CA rotation that you have the credentials, after
checking that the new kubeconfig authenticates to the cluster: the rotation does not continue without
it. 'ichor rotate ca' does all of this itself.

```sh
ichor create credentials NAME [flags]
```

### Examples

```sh
  ichor create credentials prod
  ichor create credentials prod --read-only --ttl 8h
  ichor create credentials prod --bundle ~/.ichor/clusters/prod/secrets.yaml \
    --kube-server https://10.0.0.2:6443 --talos-endpoints 10.0.0.2
```

### Options

```text
      --acknowledge               after checking the new kubeconfig works, tell the CA rotation that is waiting for credentials to continue
      --bundle string             read the CAs from this secrets bundle file (export bundle's output) instead of the cluster; works offline
      --ca-file string            PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --force                     with --bundle: write the files even though the bundle's CAs differ from the stored secrets bundle; pass it when you know the file is the current bundle (for example after a restore)
  -h, --help                      help for credentials
      --ichor-namespace string    namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --kube-server string        the Kubernetes API server URL for the kubeconfig, for example https://10.0.0.2:6443 (default: the cluster's endpoint on port 6443)
      --kubeconfig-out string     where to write the kubeconfig (default: your current kubeconfig's path plus .ichor-new)
      --local                     do not use ichor-ui; compute from the Kubernetes API directly
      --operation string          with --acknowledge: the CA rotation operation to acknowledge
      --override                  incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --read-only                 make the talosconfig an os:reader client (read-only Talos API access) instead of os:admin
      --talos-endpoints strings   the Talos API endpoints for the talosconfig (default: the cluster's control-plane nodes, else your current talosconfig's)
      --talosconfig-out string    where to write the talosconfig (default: your current talosconfig's path plus .ichor-new)
      --tls-server-name string    name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
      --ttl duration              how long the client certificates in the files are valid; the default and the maximum is 8760h0m0s (a rotation hand-off must outlive the rotation)
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

* [ichor create](ichor_create.md)	 - Create a user or an etcd backup, or write credentials for talosctl and kubectl


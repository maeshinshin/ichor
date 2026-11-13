---
title: "ichor export bundle"
description: "Save the cluster's current secrets bundle locally and acknowledge it after a CA rotation"
---

## ichor export bundle

Save the cluster's current secrets bundle locally and acknowledge it after a CA rotation

### Synopsis

export bundle reads the secrets bundle the cluster is running with from its Secret and
writes it to ~/.ichor/clusters/<name>/secrets.yaml (mode 0600, encrypted with the passphrase of the file it
replaces, or a new one: --bundle-passphrase-file, $ICHOR_BUNDLE_PASSPHRASE, or generated on a terminal and shown
once). A CA rotation replaces the bundle, and a
copy saved before it no longer matches any node, so recovering a lost cluster (ichor restore
cluster) needs this one.

After the file is written it is read back; only if it holds the live bundle does the command mark the
TalosCluster as exported, which clears its BundleBackupStale condition. The bundle is never printed.
It refuses while a CA rotation is still under way, because the bundle is about to change.

```sh
ichor export bundle NAME [flags]
```

### Examples

```sh
  ichor export bundle prod
  ichor export bundle prod --bundle-passphrase-file /run/secrets/bundle-pass
```

### Options

```text
      --bundle-plaintext         store the bundle unencrypted (default: encrypted; an existing encrypted file keeps its passphrase)
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
  -h, --help                     help for bundle
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --override                 incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --tls-server-name string   name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
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

* [ichor export](ichor_export.md)	 - Save a cluster's secrets bundle locally


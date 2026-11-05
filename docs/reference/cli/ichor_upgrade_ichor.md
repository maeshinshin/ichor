---
title: "ichor upgrade ichor"
description: "Upgrade Ichor itself (IchorInstallation.spec.version)"
---

## ichor upgrade ichor

Upgrade Ichor itself (IchorInstallation.spec.version)

### Synopsis

Upgrade Ichor itself (IchorInstallation.spec.version).

Without VERSION it uses the recommended version (see `--list`). It shows which machines restart
and in which order, and asks for confirmation unless --yes.

```
ichor upgrade ichor VERSION [flags]
```

### Options

```
      --cluster string            the cluster (default: the only one in the namespace)
      --follow                    after creating the operation, follow its progress until it finishes
  -h, --help                      help for ichor
      --installer-digest string   pin the installer image to this digest (sha256:...) instead of resolving the version's tag
      --max-unavailable int32     workers handled at once (default 1)
      --skip-signature-check      run the installer image WITHOUT checking its signature (for builds nobody signs: development and demo images)
```

### Options inherited from parent commands

```
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --ca-file string                  PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --ichor-namespace string          namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
      --local                           do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string                namespace of the Ichor resources the command names (default "ichor-system")
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --override                        incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --tls-server-name string          name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor upgrade](ichor_upgrade.md)	 - Upgrade the cluster's Talos or Kubernetes, or Ichor itself


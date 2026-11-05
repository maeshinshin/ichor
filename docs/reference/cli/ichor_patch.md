---
title: "ichor patch"
description: "List, inspect, create, update, delete or preview TalosConfigPatches"
---

## ichor patch

List, inspect, create, update, delete or preview TalosConfigPatches

### Options

```
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
  -h, --help                     help for patch
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --override                 incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --tls-server-name string   name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
```

### Options inherited from parent commands

```
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle (ADR 0127); any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor](ichor.md)	 - Manage a Talos Linux cluster through Ichor's CRDs
* [ichor patch create](ichor_patch_create.md)	 - Create a patch (shows its impact first; confirms if it needs a reboot or reinstall)
* [ichor patch delete](ichor_patch_delete.md)	 - Delete a patch
* [ichor patch get](ichor_patch_get.md)	 - Show one patch
* [ichor patch list](ichor_patch_list.md)	 - List patches
* [ichor patch preview](ichor_patch_preview.md)	 - Show a patch's impact (reboot, install-time-only fields) without writing it
* [ichor patch update](ichor_patch_update.md)	 - Replace an existing patch's spec (shows its impact first; confirms if it needs a reboot or reinstall)


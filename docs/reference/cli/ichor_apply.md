---
title: "ichor apply"
description: "Create or update a TalosConfigPatch from a file (shows its impact first; confirms if it needs a reboot or reinstall)"
---

## ichor apply

Create or update a TalosConfigPatch from a file (shows its impact first; confirms if it needs a reboot or reinstall)

### Synopsis

apply writes the TalosConfigPatch in FILE: it creates the patch when none of that name exists in the
namespace and replaces its spec when one does. It shows the impact first (a reboot, a reset and
reinstall for install-time-only fields, fields too new for the cluster's Talos) and asks for confirmation
when there is any, unless --yes. - reads the manifest from stdin.

The manifest is a typed TalosConfigPatch: spec.documents[] holds Talos machine configuration documents.

apply cni-values writes your layer of Cilium's Helm values instead.

```sh
ichor apply -f FILE [flags]
```

### Examples

```sh
  ichor apply -f ingress-sysctls.yaml
  ichor apply -f ingress-sysctls.yaml --yes
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
  -f, --filename string          TalosConfigPatch manifest (YAML or JSON); - reads stdin (required)
  -h, --help                     help for apply
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

* [ichor](ichor.md)	 - Manage a Talos Linux cluster through Ichor's CRDs
* [ichor apply cni-values](ichor_apply_cni-values.md)	 - Replace your layer of Cilium's values with a YAML file (- is stdin; an empty file clears it)


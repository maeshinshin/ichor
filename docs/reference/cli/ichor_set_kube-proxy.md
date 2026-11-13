---
title: "ichor set kube-proxy"
description: "Switch kube-proxy between enabled and replaced by the CNI"
---

## ichor set kube-proxy

Switch kube-proxy between enabled and replaced by the CNI

### Synopsis

set kube-proxy sets spec.cni.kubeProxy and starts the operation that carries the switch out
node by node: the new side comes up and is verified before the old side is turned off.
The admission policy that guards this field wants a confirmation annotation; the
server sets it (to the new mode) for this one write and removes it again, so it never stays behind as
a standing consent. Ichor owns the Cilium release (a Helm release applied by a Job), so the
operation applies the new kube-proxy mode to it itself: replacing starts Cilium's replacement
first and verifies it on every node before kube-proxy is turned off; enabling restores
kube-proxy first and then switches Cilium's replacement off. Writing spec.cni.kubeProxy alone
changes nothing in Cilium; only this operation does.

It shows which nodes are touched and in which order, and asks for confirmation unless --yes.

```sh
ichor set kube-proxy replaced|enabled [flags]
```

### Examples

```sh
  ichor set kube-proxy replaced --cluster prod
  ichor set kube-proxy enabled --follow
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --cluster string           the cluster (default: the only one in the namespace)
      --follow                   after creating the operation, follow its progress until it finishes
  -h, --help                     help for kube-proxy
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
      --max-unavailable int32    workers handled at once (default 1)
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

* [ichor set](ichor_set.md)	 - Change one setting: kube-proxy mode or Ichor's runtime settings


---
title: "ichor set containerd-device-ownership"
description: "Let containerd give a pod's block devices to its user and group, so non-root pods can use volumeMode: Block volumes"
---

## ichor set containerd-device-ownership

Let containerd give a pod's block devices to its user and group, so non-root pods can use volumeMode: Block volumes

### Synopsis

Set spec.containerd.deviceOwnershipFromSecurityContext of the cluster (ADR 0172). With true, containerd's
device_ownership_from_security_context is on: a container's devices are owned by the pod's runAsUser and fsGroup
instead of root. KubeVirt virtual machines and CDI importers run as a non-root user and need it to open a
volumeMode: Block volume.

The controller renders the change into every node, which restarts containerd there; no node reboots. Pods that
are already running keep their device ownership until they are recreated. Needs versionContract v1.14 or later.

```sh
ichor set containerd-device-ownership true|false [flags]
```

### Examples

```sh
  ichor set containerd-device-ownership true --cluster prod
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --cluster string           TalosCluster (in --namespace) to change; optional when the namespace has exactly one
  -h, --help                     help for containerd-device-ownership
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

* [ichor set](ichor_set.md)	 - Change one setting: kube-proxy mode, containerd device ownership or Ichor's runtime settings


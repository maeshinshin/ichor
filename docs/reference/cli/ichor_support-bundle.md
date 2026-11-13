---
title: "ichor support-bundle"
description: "Download a zip of a node's (or every node of a cluster's) logs, processes, mounts and Talos resources"
---

## ichor support-bundle

Download a zip of a node's (or every node of a cluster's) logs, processes, mounts and Talos resources

### Synopsis

support-bundle downloads a zip with, per node: the kernel log, the log of each Talos
service, the service list, process names (no command lines), mounts, memory, containers and every
Talos resource that Talos does not mark sensitive. It leaves out the machine configuration,
certificates and keys. Logs are free text and can still contain a credential a program printed,
so treat the file as confidential.

Name one NODE, or --cluster for every approved node of a cluster. Needs the "download" verb on
talosmachines/support-bundle. The file is written with mode 0600; --out - writes to standard output.

```sh
ichor support-bundle [NODE] [flags]
```

### Examples

```sh
  ichor support-bundle worker-3
  ichor support-bundle --cluster prod --out prod-support.zip
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --cluster string           download for every approved node of this cluster instead of one NODE
  -h, --help                     help for support-bundle
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --out string               file to write (mode 0600); default the name the server suggests; - for standard output
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


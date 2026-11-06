---
title: "ichor node logs"
description: "Print a node's kernel or service log; --follow keeps streaming"
---

## ichor node logs

Print a node's kernel or service log; --follow keeps streaming

### Synopsis

logs prints the kernel log (or, with --service, a Talos service's log) of one
or more nodes, merged onto one stream. Without --follow it prints what the node
has and exits once nothing new arrives for --idle-timeout. With --follow it keeps
streaming until Ctrl-C and, if the connection drops or ichor-ui drains, reconnects
and resumes from the last resume token (a few lines around the cut may repeat or
be missed: Talos offers no per-line identity). Output is plain
text (prefixed with the node name when several are given); -o json prints one
JSON object per line instead.

```sh
ichor node logs NAME... [flags]
```

### Options

```text
  -f, --follow                  keep streaming, reconnecting with the resume token if the connection drops
  -h, --help                    help for logs
      --idle-timeout duration   without --follow, stop after this long without a new line (default 3s)
      --resume-token string     resume token from an earlier run (opaque)
      --service string          Talos service whose log to read (kubelet, etcd, ...); empty means the kernel log
```

### Options inherited from parent commands

```text
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

* [ichor node](ichor_node.md)	 - Show a node, tail its logs (needs ichor-ui), or reboot, shut down, reset or remove it


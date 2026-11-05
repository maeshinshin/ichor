---
title: "ichor config apply"
description: "Apply the pending configuration changes that need a reboot or an API server restart"
---

## ichor config apply

Apply the pending configuration changes that need a reboot or an API server restart

### Synopsis

Ichor never reboots a node for a configuration change on its own: a machine whose
rendered configuration differs from the node's in a way that needs a reboot waits with
Ready=False, reason RebootRequired, and the message names the fields. apply starts the
config operation that rolls those changes out, one machine at a time: control planes
first, one at a time with an etcd membership check before each, then workers (at most
--max-unavailable at once), and the machine this controller runs on last. Per machine:
storage evacuation hook, the node is cordoned and drained (evictions respect
PodDisruptionBudgets; DaemonSet and mirror pods stay), the configuration is staged, the node
reboots, and the step finishes when the node runs the configuration, is Ready, is
uncordoned and (control planes) is back in etcd. On a cluster with one control plane the
Kubernetes API is unavailable while it reboots. A change that needs no reboot but restarts the
Kubernetes API server (authentication, API server arguments, admission, audit, authorizers: Ready=False,
reason APIServerRestartRequired) is applied the same way, one control plane at a time with no drain
and no reboot, waiting for that node's API server to answer /readyz before the next. A machine that fails pauses the operation; nothing is
rolled back. Machines held with upgrade-hold are skipped.

It shows which machines reboot, which fields need it and in which order, and asks for
confirmation unless --yes.

```
ichor config apply [flags]
```

### Options

```
      --cluster string          the cluster (default: the only one in the namespace)
      --follow                  after creating the operation, follow its progress until it finishes
  -h, --help                    help for apply
      --max-unavailable int32   workers handled at once (default 1)
```

### Options inherited from parent commands

```
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle (ADR 0127); any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
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

* [ichor config](ichor_config.md)	 - Inspect machine configuration


---
title: "ichor cluster rotate-keys"
description: "Replace the cluster's service-account signing key and secretbox (encryption at rest) key"
---

## ichor cluster rotate-keys

Replace the cluster's service-account signing key and secretbox (encryption at rest) key

### Synopsis

rotate-keys starts a key rotation operation: the Kubernetes service-account signing key and the
kube-apiserver secretbox key that Secrets are encrypted with in etcd are replaced in a dual-key period.
The new keys are first only accepted by every control plane (the old ones still sign and encrypt), then
made primary; every Secret in the cluster is rewritten under the new secretbox key; the old service-account
key is kept while the tokens it signed are re-issued; and only then are the old keys dropped. Control planes
are reconfigured one at a time and each kube-apiserver is waited for, so the Kubernetes API stays up on a
cluster with more than one control plane. Workers are not touched.

  --only service-account|secretbox   rotate one key (default: both)
  --token-settle 90m                 how long the old service-account key is kept after the new one signs;
                                     raise it when workloads ask for projected tokens that live longer than an hour
  --ignore-legacy-tokens             drop the old service-account key although token Secrets
                                     (type kubernetes.io/service-account-token) it signed still exist; they stop working

Your kubeconfig and talosconfig keep working: no CA changes. The cluster must use versionContract
v1.14 or later. Before it starts, every control plane must answer, run the configuration Ichor
renders for it, and every Kubernetes Node must be Ready; otherwise the operation pauses without
changing anything. While it runs, the machine controller does not reconcile the control planes and other
disruptive operations are refused. It resumes by itself after the controller restarts and does not advance
past a step it could not verify.

When it completes, the secrets bundle in the cluster has been replaced and it now also keeps the retired
keys (backups sealed before the rotation stay readable with them), so the copy saved on your machine is
stale: run 'ichor cluster bundle-export NAME' and keep both files safe. Defragment etcd on each control
plane afterwards ('ichor node ...' etcd-defrag): old revisions encrypted with the old key stay until then.
A snapshot taken before the rotation finished cannot be restored.

It shows which control planes are reconfigured and in which order, and asks for confirmation unless --yes.
--follow keeps reporting progress until the rotation completes.

```
ichor cluster rotate-keys NAME [flags]
```

### Options

```
      --follow                 keep reporting progress until the rotation completes or fails
  -h, --help                   help for rotate-keys
      --ignore-legacy-tokens   drop the old service-account key although token Secrets it signed still exist
      --only string            rotate only this key: service-account or secretbox (default: both)
      --token-settle string    how long the old service-account key is kept after the new one signs (default 90m, at least 75m: pods refresh their tokens after 48m)
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

* [ichor cluster](ichor_cluster.md)	 - List or inspect clusters


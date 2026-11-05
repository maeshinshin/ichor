---
title: "ichor etcd backup now"
description: "Take an etcd snapshot now, outside the schedule"
---

## ichor etcd backup now

Take an etcd snapshot now, outside the schedule

### Synopsis

now asks the controller of the TalosEtcdBackup NAME for one snapshot outside its schedule
(ADR 0047). It only sets an annotation on the TalosEtcdBackup; the controller takes, verifies,
encrypts and stores the snapshot, prunes to the retention, and records the request in
status.lastManualBackup. A suspended backup, or one with a request the controller has not started
yet, is refused. Check the result with 'ichor restore --live' (it lists the snapshots) or the
Backups page.

```
ichor etcd backup now NAME [flags]
```

### Options

```
  -h, --help   help for now
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

* [ichor etcd backup](ichor_etcd_backup.md)	 - Work with the etcd backups (TalosEtcdBackup)


---
title: "ichor"
description: "Manage a Talos Linux cluster through Ichor's CRDs"
---

## ichor

Manage a Talos Linux cluster through Ichor's CRDs

### Synopsis

ichor is the CLI half of Ichor's CLI/UI parity (docs/design/architecture.md,
"CLI and UI"): it changes the world by writing the same custom resources
the UI and controller use, never by talking to a Talos node directly to
mutate it.

After a command that reaches a cluster, ichor prints one line on stderr when the
cluster knows of a newer Ichor release, or when this binary is older than that
release. The check is made by the cluster's controller, not by ichor. The line is
shown on a terminal only, at most once a day per cluster, and never with -o or for
help and completion. Set ICHOR_NO_UPDATE_NOTICE=1 to turn it off.

### Options

```text
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle (ADR 0127); any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
  -h, --help                            help for ichor
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor adopt](ichor_adopt.md)	 - Bring an existing Talos cluster under Ichor's management with zero drift
* [ichor auth](ichor_auth.md)	 - Log in to Ichor's OIDC issuer and act as a kubectl credential plugin
* [ichor cluster](ichor_cluster.md)	 - List or inspect clusters
* [ichor cni](ichor_cni.md)	 - Operate on the cluster's CNI settings Ichor owns
* [ichor config](ichor_config.md)	 - Inspect machine configuration
* [ichor etcd](ichor_etcd.md)	 - Download an etcd snapshot from, or run an etcd maintenance action on, a control plane node
* [ichor init](ichor_init.md)	 - Bootstrap a brand-new Talos cluster's first control plane
* [ichor machine](ichor_machine.md)	 - List, inspect, approve or reject machines
* [ichor node](ichor_node.md)	 - Show a node, tail its logs (needs ichor-ui), or reboot, shut down, reset or remove it
* [ichor operation](ichor_operation.md)	 - List, follow, pause, resume or delete the operations (upgrades, shutdowns) of a cluster
* [ichor patch](ichor_patch.md)	 - List, inspect, create, update, delete or preview TalosConfigPatches
* [ichor restore](ichor_restore.md)	 - Restore etcd from a backup snapshot: into the running cluster (--live) or after losing it (--recover-cluster)
* [ichor secrets](ichor_secrets.md)	 - Manage the local secrets bundle file
* [ichor settings](ichor_settings.md)	 - Show and change Ichor's runtime settings (sign-in lifetimes, default drift policy, update check, automatic patching)
* [ichor ui](ichor_ui.md)	 - Open the Ichor UI through a port-forward to ichor-ui
* [ichor upgrade](ichor_upgrade.md)	 - Upgrade the cluster's Talos or Kubernetes, or Ichor itself
* [ichor user](ichor_user.md)	 - Manage Ichor's own users (list, create, update, delete, set-password, revoke-sessions)
* [ichor version](ichor_version.md)	 - Print the CLI version and the installed Ichor version


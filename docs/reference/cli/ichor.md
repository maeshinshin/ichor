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

### Examples

```sh
  ichor get clusters
  ichor describe node cp-1
  ichor upgrade talos --list
```

### Options

```text
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
  -h, --help                            help for ichor
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

#### Get Started Commands

* [ichor adopt](ichor_adopt.md)	 - Bring an existing Talos cluster under Ichor's management with zero drift
* [ichor init](ichor_init.md)	 - Bootstrap a brand-new Talos cluster's first control plane
* [ichor ui](ichor_ui.md)	 - Open the Ichor UI through a port-forward to ichor-ui

#### Inspect Commands

* [ichor describe](ichor_describe.md)	 - Show one object in full
* [ichor diff](ichor_diff.md)	 - Show how a change would alter what is in force
* [ichor get](ichor_get.md)	 - List objects, or show a cluster in a few lines
* [ichor preview](ichor_preview.md)	 - Show a patch's impact (reboot, install-time-only fields) without writing it

#### Change Commands

* [ichor apply](ichor_apply.md)	 - Create or update a TalosConfigPatch from a file (shows its impact first; confirms if it needs a reboot or reinstall)
* [ichor create](ichor_create.md)	 - Create a user, or write credentials for talosctl and kubectl
* [ichor delete](ichor_delete.md)	 - Delete a node, patch, operation or user
* [ichor discovery](ichor_discovery.md)	 - List, add or remove the addresses and ranges Ichor scans for new nodes
* [ichor edit](ichor_edit.md)	 - Edit an object in $EDITOR, or change a user's fields
* [ichor rollout](ichor_rollout.md)	 - Roll out pending changes that need a reboot or an API server restart
* [ichor set](ichor_set.md)	 - Change one setting: kube-proxy mode or Ichor's runtime settings

#### Node and Cluster Lifecycle Commands

* [ichor approve](ichor_approve.md)	 - Approve a pending node so the controller starts configuring it
* [ichor reboot](ichor_reboot.md)	 - Reboot a node gracefully
* [ichor reclaim](ichor_reclaim.md)	 - Reset a configured Talos node of another cluster back to maintenance mode
* [ichor reject](ichor_reject.md)	 - Reject a pending node
* [ichor restart](ichor_restart.md)	 - Restart a Talos service on a node
* [ichor shutdown](ichor_shutdown.md)	 - Shut a node down, or every node of a cluster

#### Maintenance Commands

* [ichor backup](ichor_backup.md)	 - Take a backup now, outside the schedule
* [ichor bootstrap](ichor_bootstrap.md)	 - Install Ichor's Cilium on a cluster with no CNI, before helm install ichor
* [ichor defrag](ichor_defrag.md)	 - Defragment etcd on a control plane node
* [ichor export](ichor_export.md)	 - Save a cluster's secrets bundle locally
* [ichor forfeit-leadership](ichor_forfeit-leadership.md)	 - Hand etcd leadership over from a control plane node that leads
* [ichor pause](ichor_pause.md)	 - Pause a running operation
* [ichor restore](ichor_restore.md)	 - Restore etcd from a backup: into the running cluster (restore etcd) or after losing it (restore cluster)
* [ichor resume](ichor_resume.md)	 - Resume a paused operation
* [ichor rotate](ichor_rotate.md)	 - Rotate a cluster's certificate authorities or its signing and encryption keys
* [ichor snapshot](ichor_snapshot.md)	 - Download a snapshot from a node
* [ichor upgrade](ichor_upgrade.md)	 - Upgrade the cluster's Talos, Kubernetes or Cilium, or Ichor itself

#### Troubleshooting Commands

* [ichor logs](ichor_logs.md)	 - Print a node's kernel or service log
* [ichor pcap](ichor_pcap.md)	 - Capture packets on a node's interface into a pcap file, for a bounded time and size
* [ichor support-bundle](ichor_support-bundle.md)	 - Download a zip of a node's (or every node of a cluster's) logs, processes, mounts and Talos resources

#### Access Commands

* [ichor auth](ichor_auth.md)	 - Log in to Ichor's OIDC issuer and act as a kubectl credential plugin
* [ichor secrets](ichor_secrets.md)	 - Manage the local secrets bundle file
* [ichor user](ichor_user.md)	 - Set a user's password or sign a user out everywhere

#### Other Commands

* [ichor version](ichor_version.md)	 - Print the CLI version and the installed Ichor version

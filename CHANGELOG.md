# Changelog

## v0.1.0 (2026-11-04)

First release. Alpha: exercised end to end on Talos v1.14 test clusters, not yet on bare metal.

Ichor manages [Talos Linux](https://www.talos.dev/) clusters from Kubernetes.
Nodes, machine configuration, upgrades and etcd backups are custom resources
(`TalosCluster`, `TalosMachine`, `TalosConfigPatch`, `TalosOperation`,
`TalosEtcdBackup`, `TalosStorage`, `IchorInstallation`, `IchorUser`), worked with through the
`ichor` CLI and ichor-ui. The controller is the only writer to Talos nodes.
See [docs/design/architecture.md](docs/design/architecture.md).

### Install defaults

A first install (`ichor init`) uses this release set. After that, Talos,
Kubernetes and Cilium upgrade on their own, to any version the compatibility
table accepts; a refusal passes only with the override verb.

| Component | Version |
|---|---|
| Talos | v1.14.5 (version contract v1.14) |
| Kubernetes | v1.36.6 |
| Cilium | 1.20.4 (chart embedded in the CLI) |

### Features

- Discovery and approval: a node in maintenance mode is found by address or
  range, shows up as pending, and joins when approved in the UI or with
  `ichor machine approve`. Approval can set the NIC, addressing and disk roles.
- Typed machine configuration: patches built on the Talos machinery types, no
  raw YAML. Drift is reported; changes that need a reboot roll out as one
  operation, a node at a time.
- Upgrades of Talos, Kubernetes, Cilium and Ichor itself, one node at a time
  with etcd quorum in mind; a failed step pauses the operation.
- Automatic patch upgrades, off by default, inside a daily window you set.
- A notice in the UI and in `ichor version` when a newer Ichor release is
  published; it can be switched off.
- CA rotation for the Talos API and Kubernetes API CAs, in verified passes
  that wait for your new credentials.
- etcd backups on a schedule or on demand, restore into a running cluster, and
  rebuild from the secrets bundle and a snapshot alone (`ichor restore`).
- Adopt a running Talos cluster with zero drift, and reclaim a machine that
  another cluster configured; install Ichor with Helm.
- Cilium as the only CNI, kept as a Helm release; its values, upgrades and
  kube-proxy mode are managed from the UI or `ichor cni`.
- ichor-ui: a Preact web UI served over TLS only, and optional Ichor-owned
  users with the roles viewer, operator and admin, personal kubeconfigs and a
  forced password change at first login.
- Hardening of Ichor itself and typed, off-by-default hardening options for
  the clusters it creates.
- `ichor cluster talosconfig` and a UI download give operators and admins a
  short-lived (1 hour, at most 12) `os:reader` talosconfig for `talosctl`,
  without access to the secrets bundle; each request is an Event with the
  user's name.
- Release artifacts are built with `make release VERSION=v0.1.0`.
- `ichor init -f init.yaml` reads the init settings from an `InitConfig` file
  instead of a long flag line (a flag overrides the file;
  `--print-config-template` prints an example). `ichor init` also
  waits for ichor-ui to roll out before it returns, and no longer requires the
  hidden `--cni` flag.
- `ichor init` shows its steps as a short list: a live spinner line with the
  elapsed time and the reason a pod is pending on a terminal, one line per
  change in a pipe or CI log. It honours `NO_COLOR` and `TERM=dumb`, prints a
  total and the next commands at the end, and keeps `-o yaml|json` on stdout
  free of progress lines.
- The CLI prints for people by default. Lists and results are
  tables and aligned blocks with plain enum names (`control-plane`,
  `approved`), ✓ ! ✗ … for status and ages like `5m`; a change prints one ✓
  line. Colour appears only on a terminal (`NO_COLOR` and `TERM=dumb` are
  honoured) and the layout is the same in a pipe. This covers `cluster`,
  `machine`, `node`, `operation`, `upgrade`, `patch`, `user`, `settings`,
  `config`, `cni`, `etcd`, `restore`, `adopt`, `version` and `ichor ui`.
  `-o yaml` and `-o json` print what they always printed, so a script names
  its format.
- `--follow` (on `upgrade`, `config apply`, `cni kube-proxy`, the node and etcd
  actions and `restore --live`) and `operation get --watch` show one live line
  with the phase and message and end it with ✓ Completed or ✗ Failed and the
  time taken, or a line per change in a pipe. The exit codes and the resume
  hint are unchanged.

### Not part of Ichor

Ichor does not manage workloads, GitOps tools, storage or monitoring. It
installs Cilium and nothing else on the cluster besides its own controller and
UI.

### Known limits

- No images are published. Build them from the tree and push them to a
  registry the nodes can reach; see
  [Getting started](docs/guides/getting-started.md).
- Images and the embedded CLI build are linux/amd64 only.
- IPv6 and PXE provisioning are not supported. Ichor does not boot machines;
  air-gapped installs are out of scope.
- Follow-ups, deliberately deferred: rotation of the etcd and
  aggregator CAs, the service-account signing key and the secretbox key;
  resolving the installer image by digest; FQDN network-policy rules;
  SecureBoot and the other unmodelled node options; encrypting the local
  `secrets.yaml` (plaintext, mode 0600). See "Follow-ups".

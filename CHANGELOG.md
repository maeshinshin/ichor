# Changelog

## v0.1.0 (2026-11-05)

First release. Alpha: exercised end to end on Talos v1.14 clusters running as
KubeVirt VMs, not yet on bare metal.

Ichor manages [Talos Linux](https://www.talos.dev/) clusters from Kubernetes.
Nodes, machine configuration, upgrades and etcd backups are custom resources
(`TalosCluster`, `TalosMachine`, `TalosConfigPatch`, `TalosOperation`,
`TalosEtcdBackup`, `TalosStorage`, `IchorInstallation`, `IchorUser`), worked with through the
`ichor` CLI and ichor-ui. The controller is the only writer to Talos nodes.
See [docs/design/architecture.md](docs/design/architecture.md) and
[docs/adr](docs/adr/README.md).

### Install defaults

A first install (`ichor init`) uses this release set. After that, Talos,
Kubernetes and Cilium upgrade on their own, to any version the compatibility
table accepts; a refusal passes only with the override verb
([ADR 0115](docs/adr/0115-the-release-set-decides-a-first-install-only.md)).

| Component | Version |
|---|---|
| Talos | v1.14.5 (version contract v1.14) |
| Kubernetes | v1.36.6 |
| Cilium | 1.20.4 (chart embedded in the CLI) |

### Features

- Discovery and approval: a node in maintenance mode is found by address or
  range, shows up as pending, and joins when approved in the UI or with
  `ichor machine approve`. Approval can set the NIC, addressing and disk roles
  (ADR 0092).
- Typed machine configuration: patches built on the Talos machinery types, no
  raw YAML. Drift is reported; changes that need a reboot roll out as one
  operation, a node at a time (ADR 0080).
- Upgrades of Talos, Kubernetes, Cilium and Ichor itself, one node at a time
  with etcd quorum in mind; a failed step pauses the operation (ADR 0059,
  0115). Cilium moves only to the chart this Ichor embeds (below).
- Automatic patch upgrades, off by default, inside a daily window you set
  (ADR 0116).
- A notice in the UI and in `ichor version` when a newer Ichor release is
  published; it can be switched off (ADR 0114).
- CA rotation for the Talos API and Kubernetes API CAs, in verified passes
  that wait for your new credentials (ADR 0063, 0091, 0093). `--ca etcd` and
  `--ca kubernetes-aggregator` (or `--all`) also replace the etcd CA and the
  aggregator CA (ADR 0120). The etcd members load the new CA by a reboot, one
  control plane at a time with a health check before each, not by a service
  restart; deleting a rotation before its point of no return abandons it, and
  the credential hand-off waits while the Kubernetes API restarts.
- Rotation of the service-account signing key and the secretbox (encryption at
  rest) key, `ichor cluster rotate-keys` or Rotate keys in the UI: both keys are
  accepted before either is dropped, every Secret is rewritten under the new
  key, and the old signing key is kept until the pods have refreshed their
  tokens (`--token-settle`, default 90 minutes, at least 75). Ichor's own
  controller and ichor-ui are rolled once the new key signs. The etcd backup key
  is versioned, so snapshots sealed before a rotation still decrypt; a restore
  of a snapshot older than a finished rotation is refused (ADR 0121,
  [guide](docs/guides/rotate-keys.md)).
- etcd backups on a schedule or on demand, restore into a running cluster, and
  rebuild from the secrets bundle and a snapshot alone (`ichor restore`).
- Adopt a running Talos cluster with zero drift, and reclaim a machine that
  another cluster configured (ADR 0103, 0104); install Ichor with Helm.
  `ichor adopt --add-machine ADDRESS` adds a machine that already carries a
  managed cluster's CAs (it joined with your own tooling, or its `TalosMachine`
  was deleted) without a reset; `--cilium-values` carries your Helm values into
  a Cilium takeover; a cluster on another CNI is refused with the options
  written down (Ichor does not migrate it) (ADR 0128).
- Cilium as the only CNI, kept as a Helm release; its values, upgrades and
  kube-proxy mode are managed from the UI or `ichor cni` (ADR 0098, 0099).
  Cilium is pinned to the chart embedded in the running Ichor: a cluster moves
  to that chart and to nothing else, so there is one tested combination per
  release, and a recorded patch newer than the embedded chart is never stepped
  back (ADR 0115). A daily CI run opens a pull request when Cilium publishes a
  new patch of the pinned minor, after signature and digest checks; a person
  merges it and cuts a release (ADR 0119). The Cilium operator runs as many
  replicas as the cluster has nodes, and a Helm Job that keeps failing with the
  same values backs off (ADR 0113).
- ichor-ui: a Preact web UI served over TLS only, and optional Ichor-owned
  users with the roles viewer, operator and admin, personal kubeconfigs and a
  forced password change at first login (ADR 0094, 0105). The browser session
  renews itself with a refresh token (up to the issuer's refresh lifetime, 12
  hours by default) instead of ending after 5 minutes, a password change keeps
  the tab signed in, and signing out, in the UI or with `ichor auth logout`,
  revokes the refresh token at the issuer's new RFC 7009 endpoint
  `/oidc/revoke`.
- Hardening of Ichor itself and typed, off-by-default hardening options for
  the clusters it creates (ADR 0108).
- SecureBoot, UKI and kernel lockdown as typed options
  (`spec.security.secureBoot` `Off|Preferred|Required`, `spec.security.kernelLockdown`):
  approval defaults to the SecureBoot installer, a Talos upgrade picks the right
  installer variant per node, `Required` holds the first configuration of a node
  that did not boot with SecureBoot, and per-node and cluster `SecureBoot` and
  `KernelLockdown` conditions plus the Talos details in the UI show what each node
  reports. Ichor never changes a node's SecureBoot state in place (ADR 0123).
- `ichor node reset --wipe-mode system-disk|all` wipes the whole system disk (all
  also the user disks), so a node can be installed afresh from the SecureBoot ISO;
  the plain reset still wipes STATE and EPHEMERAL only (ADR 0042, 0123).
- The installer image of an Ichor upgrade runs by digest and its keyless release
  signature is verified, offline against an embedded Sigstore root, before the
  installer Job is created. `ichor upgrade ichor` takes `--installer-digest` and
  `--skip-signature-check REASON` (recorded, shown in `ichor version`, the plan
  and the UI); the API server refuses an installer Job that names a tag. The
  release workflow pushes the controller and ichor-ui images first and builds the
  installer last, so the install manifest embedded in it names both by digest
  (ADR 0122).
- FQDN egress and a closed metrics port: the controller writes a
  `CiliumNetworkPolicy` that lets ichor-ui reach only the OIDC issuer its
  Deployment names (by name, `toFQDNs`), the static policies carry no `world`
  egress, and the controller's metrics port is open only to the namespaces you
  name (ADR 0126, [guide](docs/guides/install-with-helm.md#network-policies)).
- The local secrets bundle is encrypted: `ichor init` and `ichor adopt --recover-secrets` seal
  `~/.ichor/clusters/<name>/secrets.yaml` as an armored age file under a
  passphrase (`--bundle-passphrase-file`, `$ICHOR_BUNDLE_PASSPHRASE`, or one
  generated and shown once on a terminal; `age -d` opens it). `ichor secrets
  encrypt` migrates a plaintext file; the retired keys of a key rotation are
  sealed the same way (ADR 0127).
- Each operation takes the cluster lock under its own identity, as does a
  control-plane join or removal under the machine's, so two disruptive pieces of
  work never run together even when created with `kubectl` or GitOps; the one
  that waits pauses with `ClusterLockHeld` naming the holder, and the lock of a
  deleted, completed or failed-paused holder is taken over (ADR 0026, 0068).
- An approved machine that converged and then came back in maintenance mode with
  its approved identity (its disk was wiped or replaced) is re-provisioned by
  the controller, with a Warning Event; a stale etcd member of a control plane
  is removed first. `hold-apply` stops it (ADR 0076).
- Kubelet serving certificates signed by the Kubernetes CA
  (`spec.security.kubeletServingCertificates`): the controller approves each
  kubelet's serving-certificate request itself, only for that node's own names
  and addresses, and the kube-apiserver can then verify the kubelets
  (ADR 0125).
- `ichor cluster talosconfig` and a UI download give operators and admins a
  short-lived (1 hour, at most 12) `os:reader` talosconfig for `talosctl`,
  without access to the secrets bundle; each request is an Event with the
  user's name (ADR 0124).
- After a node reset, discovery looks again every 15 seconds for 3 minutes and
  wakes on the Node, so a wiped machine shows up as pending soon after it is
  back in maintenance mode; a pending machine whose node was reset has its pinned maintenance
  certificate and inventory refreshed by discovery, so `ichor machine reclaim`
  no longer deletes it (ADR 0107, 0128).
- Release artifacts are built with `make release VERSION=v0.1.0`.
- `ichor init -f init.yaml` reads the init settings from an `InitConfig` file
  instead of a long flag line (a flag overrides the file;
  `--print-config-template` prints an example; ADR 0117). `ichor init` also
  waits for ichor-ui to roll out before it returns, and no longer requires the
  hidden `--cni` flag.
- `ichor init` shows its steps as a short list: a live spinner line with the
  elapsed time and the reason a pod is pending on a terminal, one line per
  change in a pipe or CI log. It honours `NO_COLOR` and `TERM=dumb`, prints a
  total and the next commands at the end, and keeps `-o yaml|json` on stdout
  free of progress lines.
- The CLI prints for people by default (ADR 0118). Lists and results are
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

### Behaviour changes

These are decisions made late in the cycle. They matter if you ran a pre-release
build or wrote scripts against one.

- The local secrets bundle is encrypted by default. `ichor init` prints a
  generated passphrase once on a terminal and waits for Enter; `ichor adopt
  --recover-secrets` seals the recovered bundle the same way. With no terminal and
  no `--bundle-passphrase-file` or `$ICHOR_BUNDLE_PASSPHRASE` they refuse before
  they do anything (fail closed); `--bundle-plaintext` opts out
  with a warning at write time and on every later read. A plaintext file still
  loads, with a warning; `ichor secrets encrypt` migrates it. Lose the passphrase
  and the file is unreadable, so keep it apart from the file (ADR 0127).
- The controller's metrics port is closed until a namespace is named (chart value
  `networkPolicy.cilium.metricsNamespaces`, plus `metricsPodLabels` to narrow it
  to the Prometheus pods). An installer or `ichor init` install carries no such
  value; add a `CiliumNetworkPolicy` of your own, for which the network policies
  guide has an example. ichor-ui's `/metrics` is protected by its token check
  alone (ADR 0126).
- The static policies no longer allow `world` egress for ichor-ui. An external
  OIDC issuer is reached through an FQDN policy the controller writes from the
  issuer the ichor-ui Deployment names, which needs Cilium's L7 DNS proxy
  (`enable-l7-proxy`); with the proxy off the issuer is unreachable unless
  `dynamicTalosEgress` is turned off (ADR 0126).
- `ichor upgrade ichor` verifies the installer image's signature. Development and
  demo builds (`v0.0.N-gSHA`) are not signed and need `--skip-signature-check
  "REASON"`, and, when the controller cannot resolve the tag in its registry,
  `--installer-digest sha256:...`. The skip is recorded and never inherited by
  the next upgrade (ADR 0122).
- Image-only Cilium patches are gone. The Cilium upgrade dialog and `ichor cni
  upgrade` offer the chart this Ichor embeds and nothing else, and automatic
  patching (ADR 0116) selects no Cilium operation; a Cilium patch arrives with an
  Ichor release (ADR 0115, 0116).
- `ichor adopt -n` defaults to `ichor-system`, like every other command (it was
  `default`) (ADR 0128).
- `ichor machine reclaim` no longer deletes the stale pending `TalosMachine` of a
  node it reset; discovery refreshes it, so the command needs no `delete` right
  on `talosmachines` (ADR 0128).
- `rotate-keys` refuses a `--token-settle` below 75 minutes, for the webhook, the
  CLI and the UI alike (ADR 0121).
- Two operations on one cluster no longer run side by side: the second waits for
  the first (ADR 0026).
- A plain `ichor node reset` does not reinstall a SecureBoot node, which boots its
  installed system again; use `--wipe-mode system-disk` (ADR 0123).
- `ichor machine approve` without sign-in on an `--allow-unauthenticated` install
  is refused for lack of the `approve` right; pass `--local` or sign in (ADR 0108).

### Notable fixes

Found on hardware while building this release.

- An HA cluster's Cilium operator stayed Pending on the one node that existed
  and a failing Helm Job retried once a minute for ever; replicas now follow the
  nodes, the bootstrap manifest of `ichor init` renders for the one node it
  starts on, and the retry backs off (ADR 0113).
- The etcd CA rotation hung because etcd does not reload its CA on a service
  restart; members now reboot (ADR 0120). Key rotation dropped the old
  service-account key before pods had refreshed their tokens and crash-looped
  Ichor's own pods; the settle has a minimum and Ichor's pods are rolled first
  (ADR 0121).
- `ichor node reset` no longer pauses with a refused delete when discovery
  recreates the pending machine under the same name; the operation follows the
  machine it started on by UID (ADR 0108).
- `ichor machine reclaim` and `adopt --add-machine` on a node of another cluster:
  a control plane whose etcd is not running no longer hangs the inspection (every
  node call gives up after 20 seconds), and a node of another cluster gets the
  refusal that names `ichor machine reclaim` instead of a raw TLS error
  (ADR 0128).
- A fresh install's NodeStarting message no longer shows the transient "STATE:
  error evaluating disk locator" for the first minute, and a stale
  `AdoptionHeld` condition no longer lingers after `hold-apply` is removed.
- ichor-ui: list tables and dialogs fit a phone, the Overview actions stay on
  while an operation runs and show the server's refusal.
- An error is printed once, and the plaintext-bundle warning once per run.

### Not part of Ichor

Ichor does not manage workloads, GitOps tools, storage or monitoring. It
installs Cilium and nothing else on the cluster besides its own controller and
UI (ADR 0101).

### Known limits

- No images are published. Build them from the tree and push them to a
  registry the nodes can reach; see
  [Getting started](docs/guides/getting-started.md).
- Images and the embedded CLI build are linux/amd64 only.
- IPv6 and PXE provisioning are not supported. Ichor does not boot machines;
  air-gapped installs are out of scope (ADR 0090).
- Hardware coverage is partial. SecureBoot was exercised only on KubeVirt VMs
  with OVMF in setup mode, not on bare metal. The etcd CA rotation by reboot, the
  key rotation after its token-settle fix, the FQDN and metrics policies, the
  `os:reader` talosconfig, kubelet serving certificates and a signed installer
  upgrade have unit and envtest coverage but await a hardware run; the Cilium
  takeover with `--cilium-values` and the flannel move were not run. Each ADR's
  status line says what was verified.
- `ichor adopt --add-machine` has no UI button. Ichor does not migrate a cluster
  from another CNI to Cilium; `adopt --cni-unmanaged` leaves it as it is.
- A pod that asks for a projected service-account token valid for more than about
  80 minutes is refreshed later than the key-rotation settle and is rejected when
  the old key is dropped (ADR 0121).
- The signature of the installer image is checked against a Sigstore root built
  into the controller; after Sigstore rotates its keys an older controller needs
  the recorded skip until the next release (ADR 0122).

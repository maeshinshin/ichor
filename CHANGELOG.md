# Changelog

## v0.3.0 (unreleased)

The command line is verb first, which breaks every script that calls it, and a
`TalosConfigPatch` can carry all 65 Talos document kinds Ichor accepts, up from
7. `ichor init` and `ichor adopt` now always turn sign-in on, and the open mode
(ichor-ui without a sign-in) is removed everywhere, `ichor adopt` takes
Talos v1.14 or later only, and an endpoint is `vip` or `node`: a load
balancer's name or address goes in `node`. The Talos, Kubernetes and Cilium
versions a first install gets are the same as in v0.2.4. The CRDs gain the 58
new document fields and one wipe mode, and lose two credential fields and the
`dns` endpoint type; `loadBalancer` stays in the endpoint enum only so that a
stored object still reads, and is refused (see Cluster and Typed
configuration). With `--endpoint-type vip`, each control plane now holds the
endpoint address on its primary link, and `ichor init` takes the first node's
network for it.

v0.3.0 is the last release with changes of this size. No further large change is planned after it.

### Upgrade notes

What in this release can refuse something that used to work, or change what a
command does. Each item is described where it is listed below.

- **A Helm values file with no sign-in stops rendering: add `--set
  ichorUI.users.enabled=true --set ichorUI.users.externalURL=https://localhost:8443`
  (or `ichorUI.oidc`, or `ichorUI.enabled=false`).** The chart refuses to render an
  ichor-ui with neither `ichorUI.users.enabled` nor `ichorUI.oidc.enabled`, so the
  default values, and any values file of yours that sets neither, fail `helm install`,
  `helm upgrade`, `helm template` and `helm lint` with a message that names what to
  set. `externalURL` is the https address browsers and the CLI reach ichor-ui at;
  `https://localhost:8443` is the default issuer of `ichor init` and is reached with
  `ichor ui`. Before you upgrade a release that ran without a sign-in, add it to the
  values, then create the first administrator with `ichor create user admin --role
  admin --generate --bootstrap`, set `spec.authentication.ichor` on the
  `TalosCluster` and run `ichor rollout config` (Users and login); users need a Talos
  v1.14 contract, and OIDC is the alternative without it. `ichor upgrade ichor` does not
  touch a Helm install. Your own CI that renders the chart needs the same addition.
- **A `TalosConfigPatch` that sets `spec.documents` loses it.** The field is gone
  from the CRD: `spec.config`, Ichor's own schema for a patch, is the only place
  to write Talos configuration. The API server prunes `documents` from a stored
  patch when this release's CRD is installed, so the nodes a patch reached lose
  its settings at their next apply, and `kubectl apply` of a manifest that still
  sets it is refused as an unknown field. Write each setting in `spec.config`
  before you upgrade, once each, in the place its meaning gives it; the Kubernetes
  component settings in it need machines on Talos 1.14 or later. A patch you
  wrote for a bond, bridge or other link is the exception to "once each": see
  Typed configuration.
- **A client older than this release cannot write a patch through `PatchService`.**
  `Patch.documents` is deprecated and never set; `Patch.config` is the patch's
  `spec.config` as JSON. A create, update or preview that sets `documents`, or that
  leaves `config` out, is refused with a message that names `config`, so an older
  browser tab or CLI can no longer save over a patch and clear it: reload the page, or
  update the CLI. An older client that only reads gets patches with no documents.
  A newer ichor-ui serves its own page, so only a tab left open across the upgrade
  is affected, and `ichor` is installed with the release it talks to.
- **Scripts.** Every command moved to a verb-first name, `restore` lost its mode
  flags, and `-o FILE` became `--out FILE` (CLI).
- **A `TalosCluster` whose endpoint type is `dns` or `loadBalancer`** still
  reads, and an update that leaves its endpoint alone still goes through. A new
  one with either type is refused, and so is an update that changes the type or
  the address. Move it to `node` when you next change the endpoint; the address
  stays as it is (Cluster).
- **A `TalosConfigPatch` or `TalosCluster` that sets
  `machine.registries.config.<host>.tls.clientIdentity` or
  `cluster.extraManifestHeaders`** loses the field when the API server prunes it
  (Typed configuration).
- **The open mode is removed, and an ichor-ui with no sign-in cannot exist.**
  `--allow-unauthenticated` on `ichor init` and `ichor adopt` (and
  `ui.allowUnauthenticated` in an init file), on `ichor-ui` (and
  `ICHOR_ALLOW_UNAUTHENTICATED=true`), and the chart value
  `ichorUI.allowUnauthenticated` are refused, and none of them is ignored.
  ichor-ui itself no longer starts with neither Ichor users nor OIDC, and serves
  nothing to a caller it cannot identify (the read-only calls it served without a
  sign-in are gone). If you relied on an ichor-ui that anyone who can reach it could
  read or change the cluster through, turn sign-in on instead:
  - `ichor init` and `ichor adopt`: leave the flag out. Sign-in is on by default,
    at `https://localhost:8443` (reached with `ichor ui`), or at `--ui-url`.
    `ichor adopt` also takes `--ui-tls-secret`, `--ui-tls-cert`, `--ui-tls-key`,
    `--kube-api-server-url` and `--kube-api-tls-server-name`, as `ichor init`
    does, for an address of your own.
  - `ichor upgrade ichor` on an install whose ichor-ui has no sign-in turns it on
    first, as `ichor init` does: it asks (`--yes` skips the question), creates the
    first administrator, switches ichor-ui, sets `spec.authentication.ichor` on
    each `TalosCluster` on Talos v1.14 or later (that marks its control planes
    `APIServerRestartRequired`; kube-apiserver restarts only when you run
    `ichor rollout config`) and prints the generated password once, at the end.
    Run it from the command line: the installer Job refuses an ichor-ui with no
    sign-in, so an upgrade started any other way (a hand-written
    `TalosOperation`, the controller's own patch upgrades) changes nothing and
    says to use the command. It takes `--ui-url`, `--admin-user`, `--ui-tls-*` and
    `--kube-api-*`.
  - Helm: see the first item of these notes. A render with `ichorUI.allowUnauthenticated`
    true fails too: remove it; `helm upgrade --reuse-values` keeps the old value, so add
    `--set ichorUI.allowUnauthenticated=null` to it or drop `--reuse-values`. An
    explicit `false` is still accepted.
  - An install with the flag that also has a sign-in has the flag dropped at the
    next `ichor upgrade ichor` (with a `NOTICE`). An ichor-ui Deployment you wrote
    yourself and started with the flag, or with no sign-in, stops at startup, with the
    instruction.
  - ichor-ui's ServiceAccount no longer holds the `start` verbs, so a custom
    binding that relied on it to start a shutdown or a rotation without a user no
    longer works (UI).
- **`ichor adopt` takes Talos v1.14 and later only, with no way around it.** Every
  node it reads when it adopts a cluster must run Talos v1.14 or later: the floor was
  v1.12, and `--allow-unauthenticated` was the only way to adopt
  without Ichor's sign-in, which needs the issuer that v1.14 carries. A cluster below
  v1.14 has to be upgraded first, with `talosctl upgrade` or your usual tool. The
  refusal names the reason and writes nothing. A node added with `--add-machine` to a
  cluster adopted before keeps the v1.12 floor, since that cluster's sign-in already
  exists. `ichor init`'s floor is the release
  set's Talos and is not affected, and `ichor upgrade talos` still lists from v1.12
  for a cluster Ichor already manages (Adopt).
- **`ichor adopt` now turns sign-in on**, as `ichor init` does: it creates the
  first admin, prints its password once, and sets `spec.authentication.ichor`,
  which marks every control plane `APIServerRestartRequired`. kube-apiserver
  restarts on each control plane only when you run `ichor rollout config`; the
  consent prompt says so (Adopt).
- **A `TalosCluster` that sets `spec.cni.custom.documents`** loses the field:
  the API server no longer serves it from the CRD this release installs, and
  drops it from storage at the object's next write. The field was part of the
  `custom` CNI, which every release has refused, so no cluster a release
  accepted holds it. `kubectl apply` of a manifest that still sets it is
  refused as an unknown field (Typed configuration).
- **`ichor adopt` refuses a node below Talos v1.14** (it was v1.12), and refuses a
  `--namespace` that is not the namespace the controller runs in, before it
  writes anything (Adopt).
- **`ichor init` now sets up sign-in**, creating the first admin and printing
  its password; and it refuses a first node on a Talos release older than the pinned one (CLI).
- **The `all` wipe mode never erased user disks**, although its help said it
  did. To erase them use the new `system-disk-and-user-disks` (Nodes).
- **Messages changed.** Anything that matches on the text of a condition
  `message`, an event, or an admission denial stops matching (Nodes, Operations,
  Typed configuration).
- **A refused read or write is `PermissionDenied`, not `Internal`.** A client of the
  ichor-ui API that retried or alerted on `Internal` from `ListClusters`,
  `ListMachines`, `ListPatches`, `GetEffectiveConfig`, `ApproveMachine`,
  `RejectMachine` or `SetMachineInstall` no longer sees it for a user whose
  Kubernetes RBAC forbids the call, and a conflicting write is `Aborted` (UI).
- **A JSON patch that removes `spec.network` or `spec.cni`** from a stored
  `TalosCluster` is refused, and so is an update that sets the `unmanaged`
  preset without the adoption annotation (Cluster).
- **An update that leaves a `TalosCluster` with no `spec.cni.cilium` while it
  held Cilium values** is refused. A whole-object `kubectl apply` or `replace`
  with `cni: {}` or without the `cilium` block used to discard the values with no
  message. Keep the values, or write `cilium: {}` to clear them on purpose
  (Cluster, ADR 0163).
- **Applying the CRDs yourself** with `kubectl apply` needs `--server-side`;
  they are too large for client-side apply (Typed configuration).
- **A drain no longer goes ahead on a node that a Longhorn replica still names,
  where Longhorn is installed outside `longhorn-system`.** Ichor does not request
  eviction of such a replica in that install, so it has to leave the node by other
  means first (Operations).
- **A `TalosConfigPatch` that writes a bond in both spellings is refused, and so is a
  `BondConfig` enumeration above its table.** Apply the patch again with each bond setting
  in one spelling: `primary` and `deviceSelectors` stay in the `v1alpha1` entry, and every
  other bond setting goes in its `BondConfig` (Typed configuration). A stored object
  still renders, because the refusal runs when a patch is written.
- **An upgrade from v0.2.x no longer stops with `talosclusters.ichor.maesh.dev is
  forbidden`.** The installer's own Role now grants `list` on `TalosClusters` in the
  release's namespace, and the installer reads the issuer URL after that Role is in
  effect. An upgrade that stopped with that message can be run again (Operations).

### CLI
- **`ichor upgrade cli` replaces the `ichor` binary with a release.** It downloads the
  release archive, verifies the signed checksums file and the archive against it
  (no way to skip), and renames the binary over the running one; `ichor upgrade cli VERSION` picks
  that release, `--check` changes nothing. The update notice now names it (ADR 0171).
- **A second `ichor init` says the cluster is already initialised.** It used to
  fail with the raw Kubernetes "already exists" error on the secrets bundle Secret;
  it now names the cluster as initialised and the Secret that exists.
- **`gatewayAPI.enabled` refuses a Gateway API older than Cilium needs.** The
  check was that the `gateways` CRD existed, so any release and any partial set
  passed and Cilium failed later. It now reads the CRD's `bundle-version` and
  refuses anything below v1.6.1, the minimum Cilium 1.20's upgrade notes give,
  naming both releases; a CRD that records no release is refused too. The six
  other CRDs are still unchecked, which the guide now says.
- **`ichor init --gateway-api` no longer overwrites Gateway API CRDs it did not
  install.** It applied all seven with forced ownership, taking over fields
  another manager owned and replacing a CRD at any release without a word. It
  now reads each one first: absent, it applies without forcing; its own, it
  updates; another manager's at the same bundle version, it leaves alone with a
  warning; another manager's at a different version, it refuses the run before
  writing anything, naming the CRD and both versions.
- **`ichor create credentials --kube-server` refuses an address that is not an
  HTTPS URL.** It used to accept `host:port` and write it into the kubeconfig,
  where client-go accepts any non-empty server, so the credential was broken
  only when something tried to use it.

- **New: `ichor create etcd-backup`** writes the S3 credentials Secret, in the
  controller's namespace, and the `TalosEtcdBackup` that names it. Nothing in
  Ichor created a `TalosEtcdBackup` before, so a scheduled backup meant writing
  the object by hand, and the advice to take a snapshot before a CA rotation
  could not be followed. The S3 credentials come from `ICHOR_S3_ACCESS_KEY_ID` and
  `ICHOR_S3_SECRET_ACCESS_KEY`, as for `ichor restore cluster`, never from flags.
  The bucket is not created. The `TalosEtcdBackup` sample is a working example,
  and a new guide, Back up etcd to S3, has the procedure (CLI, ADR 0164). The
  etcd warning of `rotate ca`, in its help and in the plan, names this command
  for an operator with no backup yet, and `ichor backup etcd NAME` for one that exists.
- **New: `ichor create credentials --kube-server URL`** names the Kubernetes API
  server for the kubeconfig. With `--bundle FILE` and `--talos-endpoints`, the
  command now writes a working kubeconfig and talosconfig with no kubeconfig on
  disk; before, it copied the server from a current kubeconfig and failed
  without one. Without the flag it behaves as before (CLI, ADR 0091).
- **`ichor create credentials` writes the cluster's endpoint as the kubeconfig's
  server.** It used to copy the server from your current kubeconfig, which kept
  naming a node after the endpoint became a VIP. Now the default is
  `https://ADDRESS:6443` from `spec.endpoint`; `--kube-server` overrides it, with
  a warning when it names another host. `--bundle` has no endpoint and still
  copies from your current kubeconfig (CLI, ADR 0091, amended).
- **Breaking: the command surface is verb first.** `ichor machine list` is now
  `ichor get nodes`, and `ichor machine approve NAME` is
  `ichor approve node NAME`. The noun-first groups `cluster`, `machine`,
  `node`, `patch`, `operation`, `config`, `etcd`, `cni` and `settings` are
  gone, and no alias keeps the old names: a script that calls one fails with
  `unknown command`, except a bare `ichor cni`, which prints the help of the
  hidden group the Cilium Job runs and exits 0. `auth`, `user` and `secrets`
  stay as groups, and `discovery` becomes one of its own; `user set-password`,
  `user revoke-sessions`, `auth login`, `auth logout`, `auth token` and
  `secrets encrypt` are unchanged, and the other commands under them are
  listed here. `adopt`, `init`, `ui`, `upgrade talos`, `upgrade kubernetes`,
  `upgrade ichor` and `version` keep their names. `docs/reference/cli/` has the
  whole surface.

  | Before | Now |
  |---|---|
  | `cluster list` | `get clusters` |
  | `cluster status NAME` | `get cluster NAME` |
  | `cluster get NAME` | `describe cluster NAME` |
  | `machine list` | `get nodes` |
  | `machine get NAME`, `node get NAME` | `describe node NAME` |
  | `machine approve`, `machine reject`, `machine reclaim` | `approve node`, `reject node`, `reclaim node` |
  | `node reboot`, `node shutdown`, `node remove` | `reboot node`, `shutdown node`, `delete node` |
  | `node restart-service NODE SERVICE` | `restart service NODE SERVICE` |
  | `node logs`, `node pcap`, `node support-bundle` | `logs node`, `pcap`, `support-bundle` |
  | `cluster shutdown` | `shutdown cluster` |
  | `patch list`, `patch get NAME` | `get patches`, `describe patch NAME` |
  | `patch create -f FILE`, `patch update -f FILE` | `apply -f FILE` |
  | `patch preview`, `patch delete` | `preview`, `delete patch` |
  | `operation list`, `operation get` | `get operations`, `describe operation` |
  | `operation pause`, `operation resume`, `operation delete` | `pause operation`, `resume operation`, `delete operation` |
  | `config apply` | `rollout config` |
  | `config effective` | `describe config` |
  | `etcd backup now` | `backup etcd` |
  | `etcd snapshot`, `etcd defrag`, `etcd forfeit-leadership` | `snapshot etcd`, `defrag etcd`, `forfeit-leadership etcd` |
  | `restore --live`, `restore --recover-cluster` | `restore etcd`, `restore cluster` |
  | `cni bootstrap`, `cni upgrade`, `cni kube-proxy` | `bootstrap cni`, `upgrade cni`, `set kube-proxy` |
  | `cni values get`, `set`, `edit`, `diff` | `get cni-values`, `apply cni-values -f FILE`, `edit cni-values`, `diff cni-values -f FILE` |
  | `settings get`, `settings set` | `get settings`, `set settings` |
  | `user list`, `get`, `create`, `update`, `delete` | `get users`, `describe user`, `create user`, `edit user`, `delete user` |
  | `cluster rotate-ca`, `cluster rotate-keys` | `rotate ca`, `rotate keys` |
  | `cluster bundle-export` | `export bundle` |
  | `cluster credentials`, `cluster talosconfig`, `auth kubeconfig` | `create credentials`, `create talosconfig`, `create kubeconfig` |
  | `auth status` | `auth whoami` |
  | `cluster discovery add`, `list`, `remove` | `discovery add`, `discovery list`, `discovery remove` |

- **Breaking: `restore` has two subcommands and no mode flags.** `restore
  etcd` rewinds the running cluster to a snapshot, as `--live` did, and
  `restore cluster` recovers a cluster whose control planes and etcd data are
  gone, as `--recover-cluster` did. The flags `--live` and `--recover-cluster`
  no longer exist, and `ichor restore` alone prints help. Each subcommand
  takes only the flags that apply to it.
- **Breaking: `-o FILE` is now `--out FILE`** on `snapshot etcd` (before:
  `etcd snapshot`), `pcap` and `support-bundle`. `-o` is the global output
  format (`yaml` or `json`), and these three used it for a file name, so the
  same letter meant two things. A script that still passes `-o FILE` to one of
  them fails with `invalid --output`.
- **Breaking: an unknown word under a verb or a group is an error.**
  `ichor get noedes` prints `unknown command "noedes" for "ichor get"` with the
  nearest names and exits non-zero. It used to print the help page and exit
  0, so a mistyped command in a script looked as if it had worked.
- `apply -f FILE` is one command for what `patch create` and `patch update`
  did. It creates the patch when none of that name exists in the namespace and
  replaces its spec when one does, shows the impact first and asks for
  confirmation as before. A caller who may create patches but not read them
  gets the create; if the patch exists, the create is refused as already
  existing.
- `describe node` shows the node's record and, below it, its hardware, usage
  and Kubernetes node when ichor-ui is reachable. It replaces `machine get`
  and `node get`; without ichor-ui it shows the record and says why the rest
  is missing.
- `ichor --help` groups the commands (Get Started, Inspect, Change, Node and
  Cluster Lifecycle, Maintenance, Troubleshooting, Access, Other), and every
  command has an example. The reference pages list the commands in the same
  groups, and a group's usage line reads `ichor get [command]`, since the group
  does nothing without one.
- `ichor init` asks for what is missing when standard input is a terminal: the
  cluster name, the endpoint type (`vip` or `node`) and address, the first node,
  its address, and the install disk, picked from the disks the node reports,
  before anything is written. The first node can be given as a network, which
  starts as the workstation's own: init scans it for nodes in maintenance mode
  on port 50000 and lists each one's address, hostname, Talos version, disk and
  link to pick from. A node that already has a configuration is listed with the
  reason it cannot be picked. An address in place of a network skips the scan.
  The node's address is then asked with its prefix length, starting from the
  address it was found at; an empty answer leaves the node on DHCP. Without a
  terminal the flags are required, as before. Esc or Ctrl-C cancels with nothing
  written. Enter alone does not pick the disk, which is wiped, and a disk row
  never calls a disk the system disk, since device order does not say which one
  is the install target.
- `ichor init --dry-run` reads the first node, then prints the settings, the
  disk and link it would use and each step it would take, and stops. It saves
  nothing, applies nothing, bootstraps nothing and never dials Kubernetes.
- **`ichor init` turns sign-in on by default.** Without `--ui-url` the issuer is
  `https://localhost:8443`, where `ichor ui` reaches ichor-ui, and init creates
  the first admin as before. `--allow-unauthenticated`, or `ui.allowUnauthenticated`
  in the `-f` file, is refused. `--ui-url` is optional and only sets the issuer. A change
  to `spec.authentication.ichor.url` on the cluster reaches ichor-ui and the
  controller at the next install or upgrade, without running init again.
- `ichor init` reads the first node's Talos version with its identity, and
  refuses a node older than the pinned release before it writes the secrets
  bundle, naming both versions and the upgrade or wipe the node needs. Before,
  such a node rejected a field of the configuration at apply time with a parser
  error that named no version.
- `ichor init` names the stock installer image on the first node. It wrote an
  install disk and no image, which Talos reads as the installer of the booted
  schematic, so a node booted from anything but an Image Factory asset could
  not install. Approving a node and setting its install target, in the UI and the
  API, name the cluster's installer image the same way, the SecureBoot variant
  when the cluster's SecureBoot mode is not Off; an install target used to be
  left without an image.
- `ichor init` takes the first node's network: `--nic`, `--address`, `--gateway`,
  `--dns` and `--vlan`, with the matching keys `firstNode.nic`,
  `firstNode.addresses`, `firstNode.gateway`, `firstNode.dns` and
  `firstNode.vlan` in the `-f` file. `--address` takes a prefix length, such as
  `192.168.1.10/24`, and may repeat. They apply to every endpoint type. Without
  one the first node keeps Talos's DHCP on every link, and with
  `--endpoint-type vip` init also picks the node's primary link for the address.
- `--nic` takes a link name as well as a MAC, on `ichor init` and on
  `ichor approve node`. A name such as `enp1s0` is looked up among the links the
  node reports, and only its permanent MAC is stored and rendered. A name that
  matches no link, or more than one, is refused, and the refusal lists every
  link with its name and MAC.
- `ichor init --cert-san` and the `cluster.certSANs` key of the `-f` file set
  `spec.certSANs`, the extra names that the Talos API and Kubernetes API
  certificates both carry. The flag may repeat. Changing the list after init
  reissues the certificates and reboots every node, workers included, so set it
  at init.
- `ichor init --kube-proxy enabled|replaced`, `--gateway-api`,
  `--l2-announcements` and `--l2-pool CIDR` (keys under `cluster.network.cni` in
  the `-f` file) choose the Cilium features at install. `--gateway-api` and
  `--l2-announcements` need `--kube-proxy replaced` and are refused without it;
  `--l2-announcements` needs `--l2-pool`, which init refuses when it overlaps an
  address Ichor owns or lies outside the first node's prefix. The prefix comes
  from `--address`, so a pool is refused when the first node has no static
  address. `--gateway-api`
  applies the seven Gateway API CRDs through the API server under their own
  field manager, so no Talos configuration carries them and the Cilium
  hand-over cannot drop them. The `CiliumL2AnnouncementPolicy` and
  `CiliumLoadBalancerIPPool` are yours to write. These act at init only; an
  existing cluster is not changed by them.
- `ichor delete node --wipe-mode system-disk-and-user-disks` erases the whole
  system disk and every user disk, leaving out CD-ROM drives and read-only
  disks. The UI's "What to wipe" offers it, and its plan and next steps say that
  user disks are erased. The existing `all` mode erased the whole system disk
  only, whatever its help said, and still does.
- Reclaiming a node: `ichor reclaim node --wait 0` says the reset is not
  confirmed, instead of reading as a finished one; the encrypted-volume warning
  says the reset wipes STATE, which holds the salt a `lockToState` key needs; and
  the refusal for a node that cannot be reinstalled names the missing META
  partition as the condition.
- `--skip-signature-check` (`ichor upgrade ichor`, `ichor init`) now says in its
  help, the CLI reference, the UI and the installer-image guide that it is not
  one-shot: it records `spec.installerSignature.mode: skip` in the
  `IchorInstallation`, where it stays, for the Cilium Helm Job too, until an
  upgrade without the flag writes the check back or you delete the field. The
  help gives the undo. What the flag does is unchanged (ADR 0122).
- **A Cilium Helm Job that runs without a signature check now leaves a record.**
  When `spec.installerSignature.mode` is `skip` and the Job's image was not
  already recorded by an upgrade, the controller writes the image digest and
  `verification: Skipped` to `IchorInstallation` `status.installerImage` and
  raises the `InstallerSignatureSkipped` Warning Event, as an Ichor upgrade
  does. The record is replaced by the next upgrade, so it does not outlast one
  (ADR 0122).
- **`ichor init` finishes when the controller seeded the installation first.** The
  controller's `Seed` creates the `IchorInstallation` singleton with no installer digest
  or signature mode, and `init` used to fail on `AlreadyExists`, leaving the object
  unpinned. `init` now accepts that object and fills in the pin it was given. It refuses,
  before it writes anything, an object that names another version or manager, or a
  digest or signature mode that differs from the pin, rather than re-pointing the cluster.

### Adopt

- **`ichor adopt` turns Ichor's sign-in on by default.** After the plan is
  written and every hold is released, in the Ichor it installs, adopt creates the
  first admin (`--admin-user`, default `admin`), switches ichor-ui to sign-in at
  `--ui-url` (default `https://localhost:8443`) and sets
  `spec.authentication.ichor` on the `TalosCluster`, with the same code as
  `ichor init`. The authenticator marks each control plane
  `APIServerRestartRequired`; kube-apiserver restarts on each control plane, one
  at a time, when you run `ichor rollout config`, and not before. The consent
  prompt names this before anything is written. The generated password is
  printed once, last before the next steps. `--allow-unauthenticated` is refused.
  Before this, adopt installed ichor-ui open. A Helm-managed Ichor is not touched.
- **`ichor adopt` takes `ichor init`'s TLS and kubeconfig flags**:
  `--ui-tls-secret`, `--ui-tls-cert` with `--ui-tls-key`, `--kube-api-server-url`
  and `--kube-api-tls-server-name`. They are checked in the plan, before anything
  is written, and refused where adopt would not apply them: a Helm-managed Ichor
  (use the chart values) and an ichor-ui whose users are already on.
- **A second `ichor adopt` or `--add-machine` is no longer refused for Ichor's
  own issuer.** The refusal of a node's jwt authenticators now counts only the
  issuers the cluster's own render does not have.
- **`ichor adopt` takes Talos v1.14 and later.** A version contract below v1.14,
  named with `--version-contract` or chosen by the probe, is refused in the plan
  phase, before anything is written, with the instruction to upgrade Talos.
  Sign-in is always on and only v1.14 carries its issuer. The floor for each node
  of an adoption moved from v1.12 to v1.14 with it, and the refusal says why; a node
  added with `--add-machine` keeps v1.12.
- A rerun after a partial write finishes the job. When the `TalosCluster`
  already exists, adopt now discovers the members again, as a first run does,
  and writes every member that has no `TalosMachine`, which a first run that
  stopped part-way never created and nothing reported. This needs `--node`;
  without it a rerun keeps the machines already written.
- **`ichor adopt` refuses a `--namespace` that is not the namespace the
  controller runs in**, before it writes anything, naming both. The controller
  reads the secrets bundle Secret from its own namespace, which a Helm install
  may set to something other than `ichor-system`; adopt used to write the
  Secret to `--namespace` and report success, and the bundle was never found.
  When adopt cannot tell the controller's namespace, because it may not list
  Deployments and Helm release Secrets cluster-wide, the default namespace goes
  ahead and any other is refused with a message that says adopt could not
  confirm it, what it tried to read, and what to do. `--add-machine` is not
  checked.
- Adopt types every endpoint that is not a virtual IP as `node`, and never
  claims an address Ichor did not configure.
- The refusal for a cluster with no CNI says that KubePrism is required on
  every node in both kube-proxy modes, and that Cilium uses it as its API
  address only when kube-proxy is replaced. When Ichor takes over an existing Cilium
  with kube-proxy enabled, a KubePrism address already set on it is no longer
  reported as a conflict with a value Ichor does not set.

### Cluster

- **Changing a cluster's endpoint to `vip` warns about control planes that cannot
  hold it.** The VIP goes on the link a node's `spec.network` names, so an
  approved control plane without one (the first node of a cluster made with a
  node endpoint) never holds it. The webhook names each such control plane; set
  its `spec.network` (ADR 0092).
- **`gatewayAPI.enabled` is refused when only some of the Gateway API CRDs are installed.** The check read
  the `gateways` CRD alone, so a cluster missing `httproutes` passed it. It now reads all seven and names
  the missing ones; the controller and ichor-ui roles may `get` the six extra CRDs by name.

- **`kubectl get talosclusters` shows the `Transitioning` condition instead of an empty
  `Ready` column.** No controller sets a `Ready` condition on a `TalosCluster`; the
  column now reads `Transitioning`, which is `True` while the control-plane count is
  even during a replacement. The `secretsRef` description says the Secret holds the
  whole Talos secrets bundle, not only the CA certificates.
- **`--endpoint-type vip` puts the endpoint address on the control planes.**
  Ichor renders a `Layer2VIPConfig` for the address on each control plane's
  primary link. A `LinkAliasConfig` selects that link by its permanent MAC
  rather than by the kernel's name for it, so the setting survives the
  interface being renamed or renumbered across a reboot. Once a control plane of
  a `vip` cluster has a primary link, a control plane approved without one is
  refused, and the refusal names `--nic`.
- The endpoint address must fit its type: an IP address for `vip`, an IP
  address or a DNS host name for `node`. A host name is checked label by label,
  and one whose last label is all digits is refused, so that a mistyped IP
  address is not taken for a name. `ichor init` refuses a mismatch before it
  creates anything. The webhook refuses one on create, and on an update that
  changes the type or the address, so an object stored with a mismatched pair
  stays editable until one of them changes.
- A talosconfig that Ichor hands out names the control planes' addresses as its
  Talos API endpoints, or, when no control plane is left, every machine's. It
  never names the cluster endpoint, whatever its type, because that endpoint
  fronts the Kubernetes API. It is refused, with the reason, when no machine has
  an address.
- **Breaking: `dns` is no longer an endpoint type, and `loadBalancer` is
  refused.** `spec.endpoint.type` takes `vip` or `node`; the enum keeps
  `loadBalancer` only so that stored objects still read. A hostname or address
  in front of the control planes is a `node` endpoint, since only `vip` changes
  the configuration Ichor renders. A stored `TalosCluster` that says `dns`
  still reads, and an update that leaves its endpoint alone is accepted,
  because Kubernetes does not re-check a CRD field whose value an update does
  not change. Creating a cluster with `dns`, or changing the type to it, is
  refused by the CRD schema. `loadBalancer` is refused by the admission
  webhook instead, with "use type node": a stored one also reads and an
  endpoint-free update passes, but a create, or an update that changes the type
  or the address, is refused. Move either to `node` when you next change the
  endpoint, and a host name then works as it is. The address stays as it is:

  ```
  kubectl -n NAMESPACE patch talosclusters.ichor.maesh.dev NAME --type=merge -p '{"spec":{"endpoint":{"type":"node"}}}'
  ```

  `ichor init` offers `vip` and `node` only and refuses `loadBalancer` by name,
  and `ichor adopt` types every endpoint that is not a virtual IP as `node`.
- A JSON patch that removes `spec.network` or `spec.cni` from a stored
  `TalosCluster` is refused. The immutability rules did not run on a field that
  was absent, so the removal went through. Setting a field that was unset is
  still allowed.
- An update that leaves a `TalosCluster` with no `spec.cni.cilium` while the
  stored object held Cilium values is refused, and the message says so. Sending
  `cni: {}`, or a `cni` without the `cilium` block, reads the same after the
  defaults fill in `preset` and `kubeProxy`, and used to discard the values.
  `spec.cni.cilium: {}` clears them on purpose, which is what `ichor apply
  cni-values` with an empty file writes. A cluster with no values is not
  affected (ADR 0163).
- **With kube-proxy enabled, you can set Cilium's API address.**
  `k8sServiceHost` and `k8sServicePort` in `spec.cni.cilium.values` used to be
  refused in both kube-proxy modes, although Ichor sets them only when
  kube-proxy is replaced, so an `enabled` cluster could not change the chart's
  default. They are now accepted when kube-proxy is `enabled` and still refused
  when it is `replaced`, where the refusal says that Ichor sets them because
  `spec.cni.kubeProxy` is replaced. The Cilium page of the UI lists them as
  Ichor-owned only on a `replaced` cluster. A value set on an `enabled` cluster
  is refused if the cluster later switches to `replaced` (ADR 0159).
- An update that makes `spec.cni.preset` `unmanaged` is refused unless the
  object carries the adoption annotation, as create already was. A JSON patch
  could do it to a stored cluster without one, and it stops Ichor's Cilium
  hand-over for that cluster.
- In the first two minutes after the controller starts, a call to its own
  admission webhook that fails (`failed calling webhook`) is logged at info
  level and says it is expected while the webhook starts; it used to be logged as
  `Reconciler error`, for about fifteen seconds after a fresh install. After two
  minutes the same failure is logged as an error again. A write that conflicts
  with a newer version (`the object has been modified`) is logged at info level
  and says it is expected, at any time. Both are retried as before. A conflict
  that never resolves no longer counts in `controller_runtime_reconcile_errors_total`,
  which the `IchorReconcileErrorsPersisting` alert watches; the new counter
  `ichor_reconcile_expected_errors_total`, by `controller` and `reason`
  (`conflict` or `webhook_startup`), counts them instead.

### Nodes

- **A control plane with a static address now tells etcd to advertise from that
  subnet** (`cluster.etcd.advertisedSubnets`, rendered from `spec.network.addresses`).
  Without it Talos advertises the node's first routable address, which on a
  control plane approved on DHCP and then given a static address could be a
  DHCP lease still on the link, and became etcd's peer URL. A control plane
  approved on DHCP gets a warning that says so; give it a static address
  (`--address`). A machine already running etcd keeps its peer URL; the change
  applies when etcd next starts.
- The CLI, the controller, the admission webhooks and ichor-ui say node where
  they said machine: confirmations, plans, errors, command hints, Kubernetes
  event and condition messages, admission denials and the screens of the UI. The
  `TalosMachine` kind, its fields and the names of the CRDs are unchanged.
  Condition types and event reasons are unchanged too, so something that
  matches on a `type` or a `reason` keeps working. Something that matches on
  the text of a message, a condition's `message` or the words of a denial, does
  not.
- **A failed removal no longer leaves a node that a plain `kubectl delete`
  would wipe.** A removal arms the `allow-delete` guard and the reset opt-ins
  to delete the `TalosMachine`. The controller now arms it only immediately
  before each delete attempt and disarms it when the attempt fails, before the
  operation pauses, and fails instead of pausing if the disarm fails. ichor-ui's
  removal puts the guard annotations back when its own delete is refused, and
  says the guard may still be armed if that restore fails. A process that dies
  between the arm and the restore still leaves it armed.
- A removal's reset completes only once the node answers in maintenance mode
  with its UUID. A Node going NotReady was taken as proof of the wipe, but a
  machine that rebooted with its disk intact goes NotReady too. After
  `nodeDownWait` the removal completes and reports the wipe as not confirmed.
  A removal that powers the machine off afterwards cannot be confirmed that way,
  so it completes when the Node goes NotReady, without waiting out
  `nodeDownWait`, and records a Warning event that says why the wipe is
  unconfirmed: the same type as the other unconfirmed wipes, since the disk is
  as unverified. A default removal powers the machine off, so it records that
  Warning.
- A removal that waits for the cluster lock after Talos accepted its Reset no
  longer sends the Reset again to the same node, and the accepted Reset stays on
  the Removal condition. The condition written when Talos accepts the
  Reset says the wipe is unconfirmed until the node answers in maintenance mode.

### Operations
- **A drain is refused while a Longhorn replica still names the node, even when
  Longhorn is installed outside `longhorn-system`.** Since v0.1.2 the evacuation
  step found no Longhorn Node in that case and read it as nothing to evacuate. It
  counted replicas only in `longhorn-system`, reported the node evacuated, and the
  drain went ahead with replicas still on it. The step now counts replicas in every
  namespace and refuses while one names the node. With none naming it, the node is
  still nothing to evacuate. Ichor does not request eviction outside
  `longhorn-system`, so a replica still on the node has to leave it some other way
  before the drain goes ahead.
- **A registry that cannot be read is no longer reported as an unsigned image.**
  An unreachable registry, or one that refuses the signature manifest, used to
  pause with `InstallerImageUnverified` and a message saying the image "is not
  signed by" its identity — telling an operator their supply chain was
  compromised when their network was down. It now pauses with
  `InstallerImageUnresolved`, which ADR 0122 already assigned to that case, and
  says the signature could not be checked rather than that it failed. A
  signature that was read and does not verify is unchanged.
- **The pause for an installer image that did not resolve names the way past it.**
  It said to check the registry or pin `spec.installerDigest`; it now gives
  `ichor upgrade ichor VERSION --installer-digest sha256:... --skip-signature-check`,
  the route for a registry the controller cannot read.
- `imagesig` gained `VerifyBlob`, which checks a Sigstore bundle of a release
  blob offline, for artefacts that are not OCI images.
- **An operation paused because the controller cannot tell which machine it runs
  on no longer tells the operator to resume it.** That pause lifts itself when the
  machine can be named again, so `ichor resume operation` had nothing to do.
- **Every error path that can persist now gives the cluster lock back.** A
  shutdown or node operation that cannot load the secrets bundle pauses for a
  person instead of returning while holding the lease, a failed stop request
  releases before its retry, and a CA or key rotation reports itself blocked. A
  shutdown with no Talos client now writes the reason on the operation rather
  than only to the log. Paths whose failure cannot persist are unchanged.
- **The rest of the controller's error paths are sorted the same way: a failure that
  can persist pauses for a person, one that clears by itself is retried.** An Ichor
  upgrade or Cilium chart upgrade that meets an API server or network error is retried
  instead of paused. A Cilium chart upgrade started before the hand-over made its release
  waits for it. A CA rotation the cluster's version contract cannot run, or whose bundle has
  nothing to finish from, pauses at once and frees the cluster lock, where it waited out
  the thirty-minute give-up window holding it. An operation paused because the control-plane
  count is even, or a shutdown or rotation is under way, gives back a cluster lock an earlier
  reconcile took, so the removal that ends the transition is not kept waiting.
- **A key rotation the cluster cannot run pauses at once and frees the cluster lock.** A
  version contract below v1.14, a bundle with no secretbox secret, a `keyrotation.yaml` that
  holds other keys than the operation names, a `keyrotation.yaml` gone mid-rotation, a
  `TalosConfigPatch` that overrides the key documents and a bundle with nothing to finish from
  waited out the thirty-minute give-up window holding the lock; they now pause on a failed step.
- **The installer and Cilium Job pods are found by the `batch.kubernetes.io/job-name` label.** The
  controller selected them by `job-name`, which Kubernetes deprecated in 1.27.
- **A node action whose reboot, shutdown, service restart or leadership hand-over
  call failed is sent again when the paused operation is retried.** The retry used to wait
  for an effect of a call that never reached the node, until the action timed out.

- **The Kubernetes upgrade's plan states the order the run follows, stage by stage**
  (ADR 0087). When the controller runs on one of the cluster's nodes, the plan listed
  that node at its sorted place; the run visits it there in the first three stages and
  holds it back to last only in the kubelet stage. Each stage now names its machines in
  order, with the controller's node marked, and the machine list is the kubelet stage's.
  The plan of a config operation is unchanged.
- **Which machine runs the controller is decided the same way by the run and the plan,
  and a name that cannot be read stops the run.** A config or Kubernetes operation and a
  cluster shutdown used to treat a machine whose node name could not be read as not the
  controller's, which could put the controller's own machine in the wrong place and restart
  the controller in the middle of the operation. They now stop with an error that names the
  node, as the etcd restore does, unless another machine is already known to be the
  controller's. The controller and the UI backend also find a machine's Node by the same
  addresses (the one it answers on, then the one `spec.endpoint` names). A forbidden
  removal of the kube-proxy confirmation after a CNI operation is created is now reported
  as permission denied, not as an internal error.
- **An operation that cannot tell which machine runs the controller pauses, frees the
  cluster lock and resumes by itself.** The stop used to return an error that kept the
  lock, and the retry renewed it, so no other disruptive operation, machine join or removal,
  CNI handover or etcd restore could start while it lasted, and nothing on the operation
  said why. The operation now pauses with the reason `OwnNodeUnread` in its `Paused`
  condition, which carries the message `cannot tell which machine runs this controller`
  and why the name could not be read, writes one Event, frees the lock and tries again
  every 30 seconds. It resumes by itself once a machine answers, its Node appears or the
  machine is removed; no retry request is needed. A shutdown that stops on this does the
  same, and a shutdown that resumes from any pause no longer keeps the `Paused` condition.
  ADRs 0067, 0080 and 0087 said the controller's own node goes last and not that the
  operation stops when a machine's node name cannot be read and the controller's machine
  is not among those that can be named; each now carries a dated amendment that says when
  it stops, which operations it covers, that the etcd restore already refused so, and what
  the operator sees.
  `ichor get operation` and the live view of `--follow` now show such an operation as
  `Paused (blocked)` with its message and say it resumes by itself, where they offered
  `ichor resume operation`; the UI shows it as Blocked, with the message, and offers no
  Resume button.
- **A write the UI backend makes through `Patch`, `DeleteAllOf`, `Apply`, `Status()` or
  `SubResource()` on a user's resource now runs as the signed-in user.** Only `Create`,
  `Update` and `Delete` did. The ServiceAccount holds no write verb on those resources, so
  this changes nothing a user could do; it makes the backend's own client say whose
  write it is.
- **ADR 0080 says what the `ConfigRollout` marker does when a rollout ends.** The ADR
  said the rollout clears the marker. It sets the `TalosMachine` condition to `False`
  with reason `Converged` and leaves it there until the next rollout. The reason is not
  renamed, and `kubectl explain talosmachine.status.conditions` now explains it.
- A missing cosign signature of the installer image now says what was looked
  for: the signature referrer, through the referrers API or the
  `sha256-<hex>` tag of the digest. It used to say only that the image had no
  signature (`InstallerImageUnverified`, ADR 0122).
- **A paused operation releases the cluster lock** (ADR 0162), including one that lifts itself. An
  operation paused for a person, by a failed step or by `spec.paused`, used to
  keep the lock, so a node that tried to join was blocked for as long as the
  pause lasted. It releases the lock when it pauses and takes it again when it
  resumes, and a retry that finds another holder waits paused on the lock. A
  pause that resumes by itself keeps the lock.
- **A CA or key rotation resumed after a pause no longer gives up on its first
  blocked pass.** The thirty minutes were counted from before the pause, so a
  rotation paused for longer than that, then resumed, gave up at once. A pause now
  clears the count and the resume starts it again.
- A CA or key rotation blocked on one step for thirty minutes gives up and
  pauses for a person, freeing the lock, instead of holding it.
- A Talos upgrade whose node does not come back within 20 minutes of the
  acknowledged Upgrade fails that node and pauses the operation. It used to wait
  with no limit and hold the cluster lock.
- A resumed Kubernetes upgrade whose node cannot be reached on its first pass waits on a fresh clock. It used
  to fail the step again and pause the operation.
- A Kubernetes upgrade no longer waits on a transient error in its first pass because an earlier, cancelled
  upgrade of the same machine timed out. It fails the step at once, as it should.
- **`ichor resume operation` retries a Talos upgrade whose node booted its previous A/B slot.**
  The operation used to pause again on the same fallback without calling the Upgrade API, so
  the upgrade could not be retried. A machine whose upgrade fell back now shows as failed in
  `ichor`'s output, not as done. Where the version is the same and only the schematic differs,
  the fallback message names both schematic values.
- **`ichor resume operation` retries a Talos upgrade whose node did not come back in time.**
  The step failed at the verify timeout with the upgrade still marked outstanding, so a resume
  failed again on the same expired clock without calling the Upgrade API. The machine now shows
  as failed in `ichor`'s output, and a resume issues the upgrade again. A node that a resume
  then finds on the target shows as converged, after a timeout or after a fallback.
  If its node name cannot be read at that moment, the step waits instead of pausing again.
  That wait, and the waits for the node to be Ready and for a control plane to rejoin etcd, now
  end after 20 minutes, also on a node with no upgrade recorded: the step fails as `VerifyTimedOut`, the operation pauses and the cluster
  lock is released, where before it waited and renewed the lock for ever.
- **`ichor resume operation` retries a Kubernetes upgrade whose node did not converge in time.**
  The step failed at the verify timeout with the upgrade still marked outstanding, so a resume
  failed again on the same expired clock, and the cluster lock stopped being renewed from that
  clock. The machine now shows as failed in `ichor`'s output, and a resume starts a fresh verify
  window. A node that a resume then finds on the target shows as converged.
- The lock is renewed while an operation's Advance loop runs, every minute and
  only up to the step's own bound, and during a cluster or node stop pass; a
  step or pass that is stuck past its bound lets the lease lapse and pauses the
  operation instead of holding the lock for ever. The Upgrade call can run for
  ten minutes against a five-minute lease, which could let another operation
  take the cluster mid-step.
- The Kubernetes upgrade plan warns that the endpoint is a single control
  plane's only when the endpoint is the address recorded for one of the
  approved control planes. It used to warn for every `node` endpoint, so a name
  in front of several control planes drew it. A name or a virtual IP does not
  warn now, even a name that resolves to one control plane (ADR 0161).
- The etcd restore plan and the create that goes through it return the error
  when they cannot read the operations, instead of reporting that nothing blocks
  the restore and skipping the CA and key rotation guards. The aggregator CA
  rotation guard likewise fails on a version contract it cannot parse.

- **A self-upgrade no longer fails on a permission the installer grants itself.**
  `ichor upgrade ichor` runs the installer under the role of the release that is running,
  and that role has no right to list `TalosClusters`. The installer read the issuer URL
  before it applied its own RBAC, so the upgrade stopped with `talosclusters.ichor.maesh.dev
  is forbidden` before the new role could take effect. The issuer URL is now read after the
  installer's namespaced Role is in effect, and that Role grants `list` on `TalosClusters`
  in the release's namespace only.
- **A failed installer Job pauses with the installer's own error.** On failure the
  installer writes its error to the container's termination message file,
  `/dev/termination-log`, so the reason survives the pod. The controller reads that message
  from the Job's pod status, not the pod log, and puts it in the paused operation. A Job
  whose pod has no termination message keeps the pointer to the pod log.
- **A failed Cilium Helm Job pauses with its own error too.** The Job's pod carries the
  run's error as its termination message, and the paused operation names it in place of the
  pointer to the pod log. A pod with no message keeps the pointer.
- **The hand-over's failure condition names the same error as the operation.** It read the
  Job's last log line, while the operation's message carried the termination message. The
  condition now prefers the termination message, falls back to the last log line, then to the
  Job's own reason.

### Storage

- **`TalosStorage` takes a second disk preset, `rook-v1`, for Rook/Ceph.** A node
  that `rook-v1` governs gets no Talos volume, no kubelet mount and no node label.
  Talos writes no disk configuration for it, and the disk is left for Rook to take
  as a raw device. Ichor writes no `UserVolumeConfig` for it, because Talos formats
  what that document provisions and Rook cannot use a formatted disk. The preset
  has no evacuation hook, because Rook's own operator blocks the eviction of its
  OSDs during a drain while the chart's `managePodBudgets` is on, as it is by
  default. A raw partition is still available, written by you as a
  `TalosConfigPatch` (ADR 0167).
- **A `rook-v1` `TalosStorage` no longer reports `StorageSystemConsistent`.** It read
  Longhorn's settings, so with Rook it showed `Unknown (NotInstalled)` for a Longhorn
  namespace and never became True. Only `longhorn-v1` has a setting to check, so
  the condition is now absent for `rook-v1`, and a stale one is removed.
- **`ExtensionsPresent` counts the nodes the `TalosStorage` governs.** While nodes were
  NotReady after a reboot it counted only those whose Kubernetes Node it could find, so it
  said "2 nodes" where `Ready` said "3". A node with no Node yet now makes it `Unknown`
  ("2 of 3 governed nodes reported") instead of `True`. A `rook-v1` `TalosStorage` has no
  extension to check, so the condition is absent there.
- **A node that `rook-v1` governs drains, upgrades or is removed without an evacuation
  step.** Ichor moves nothing off such a node first, since the preset has no evacuation
  hook; the drain itself runs, and Rook's disruption budgets decide when each OSD may
  leave. A node that no `TalosStorage` governs, or that `longhorn-v1` governs, is
  unchanged.
- **`diskSelector` is required for `longhorn-v1` and refused for `rook-v1`.** A
  `rook-v1` object that sets it is refused with "rook-v1 writes no disk config, so
  a diskSelector would do nothing: remove spec.diskSelector. To move a TalosStorage
  from longhorn-v1 to rook-v1, change diskPreset and remove diskSelector in one
  update". To move a node from `longhorn-v1` to `rook-v1`, change `diskPreset` and
  remove `diskSelector` in one update: either change alone is refused.
- **The Storage page creates a `rook-v1` `TalosStorage`.** It takes a name, a cluster
  and a node selector, and writes the object as the signed-in user, so only the
  `ichor-admin` role can create one. The page writes no disk selector, because
  `rook-v1` takes none. It lists `longhorn-v1` as unavailable, and does not create it,
  because `longhorn-v1` needs a disk selector and the page writes none: the API server
  checks only that the selector is not empty, not that it is a valid Talos expression.
  Apply that object yourself with `kubectl`, as an `ichor-admin`, after checking the
  selector against the disks of the nodes it will prepare.
- **`TalosStorage.spec.nodes` and `TalosConfigPatch.labelSelector` read different
  labels.** Rendering matches a machine's `spec.nodeLabels`, and a `TalosConfigPatch`
  selector reads only that. Evacuation, which a `TalosStorage` also drives, matches the
  labels on the live Kubernetes Node. A selector that names only labels set through
  `spec.nodeLabels` picks the same nodes in both. A label the Node carries from
  elsewhere, such as a kubelet preset label or one set with `kubectl`, makes a
  `TalosStorage` evacuate a node it renders nothing for, where there is no volume to
  evacuate. The other direction can lose data: a node that the selector matches through
  `spec.nodeLabels` is rendered before its live Node carries the label, and evacuation
  does not run for it until the label is there.

- **A removal held by a failed storage evacuation is retried.** The Removal condition
  named the failed evacuation, but nothing returned to the removal until the controller's
  periodic resync, so a machine could stay in deletion until then. The removal now asks to
  be tried again after ten seconds.
- **The storage calls of an evacuation step keep their own 15-second bound.** They were
  bounded by `etcdDialTimeout`, so tuning the etcd dial bound moved the storage bound too.
  They now use `storageCallTimeout`, which is still 15 seconds.

### Etcd backups and restore

- Retention never deletes the snapshot a run just uploaded. It ranks snapshots
  by the object store's modified time and uses the time in the name only to
  break ties. Before, it ranked by the name, so a clock that stepped backwards,
  or one object named for the far future, made the run delete the new snapshot
  or push every real snapshot out of the retained set.
- A restore marker is trusted only when it verifies with the current or a
  retired key. A key derived from `Cluster.Secret`, which every worker's machine
  configuration holds, no longer verifies one, so someone with a worker's
  configuration and write access to the bucket cannot forge a marker the
  controller acts on.
- A restore waits instead of failing when a node is Unavailable during the
  upload, when the snapshot download fails or breaks mid-stream, and when the
  check of the pinned copy after the wipe meets a transport failure. A checksum
  mismatch, a damaged chunk and a missing object still fail it.
- A restore fails at once when the backup storage refuses its credentials or
  the bucket or object is gone, naming the bucket, the object and the
  storage's answer, instead of waiting sixty minutes with the node's etcd
  already wiped. Any other storage failure still waits. Before, a missing
  object after the check passed waited as well.

- **A `retry` on a failed etcd restore now resumes it.** A person's `retry` on a
  paused restore used to be consumed and then pause the restore again at once
  with the old failure, because the marker in the backup storage kept its
  failure and its start time. A retry now clears the marker's failure and
  counts both of the restore's time limits from the retry; the marker's step
  and which control planes already did their own part are kept, so nothing
  already done is done again. The marker is written before the retry request is
  consumed, so a failed write to the backup storage leaves the request in place
  for the next pass instead of swallowing the retry.
- A retry that cannot be resumed, because the marker is gone from the backup
  storage or records a restore by another operation, is consumed and the failure
  says why and what to do: create a new etcd-restore operation, after the other
  restore has finished or been cleared. A request left on the object unread
  would look like one still waiting.

### UI

- **The Storage page lists each `TalosStorage` with the nodes it governs and the
  disk preset it uses.** It is read-only: a `TalosStorage` is still changed as a
  custom resource, and the page writes nothing.
- **The UI asks you to reload when the server has moved on.** ichor-ui sends its
  version in an `X-Ichor-Version` header on the responses to signed-in calls, and
  the page compares it with the version its own bundle was built from. When they
  differ, a prompt offers to reload, and it stays until you act on it. Pages and
  refused calls carry no version, so someone who is not signed in cannot read the
  running version from the server. A build with no version (`dev`) is never
  compared. The check runs on signed-in API responses, so a page that makes no
  call after an upgrade is not prompted until it does.
- The Configuration page edits a patch's `spec.config` through a field search, with no
  document kind to pick. Search settings opens a dialog over every setting of
  `spec.config`, by name, path or description; a row shows the setting's path, its type
  and Talos's description. Choosing one puts it in the patch, and a setting in a map
  asks for the key it is under (the volume's name, the module's name); choosing the
  same row again adds another entry, so a patch can hold two user volumes or two kernel
  modules. A setting is edited with an `Input`, a `Switch`, a `Select` where Talos
  lists the allowed values of a string field, or a JSON `Textarea` for an object,
  array or map, and the whole `spec.config` is one JSON `Textarea` under them that
  edits the same object. A setting `spec.config` does not carry is refused by its path
  when you save. The kind `Select`, the six hand-written kinds and the sysctl rows are
  gone. The search no longer matches a Talos document's kind name, since a row has no
  document. The index is built by the UI's make targets (`make ui-fieldindex`) and
  ships in the UI bundle, 1,197 rows and no longer carrying the document of a row:
  ichor-ui's role and the CRDs are unchanged. `KubeStaticPodConfig` has a row for the
  collection and one for its key, and none for the fields of the `Pod` inside it.
  The Config column of the patch list shows the sections a patch sets.
- The Configuration page has a tree of `spec.config` beside the search. Each level
  opens under its parent, a map asks for the name of each entry, and a list takes
  its first item, with a note that a second item goes in the JSON box.
- Choosing a setting in the patch editor names what its parent still needs. The
  Impact card lists the required settings the parent lacks, and Save stays off until
  each one is set. Before, a missing parent was created silently. A value that stands
  in a setting's way, such as a parent that is not an object, is kept, and the setting
  is refused with its path instead of replacing the value. The required check reads
  the schema's `required` lists only, so a rule that Talos checks at render time is
  not checked here; the server still checks on save.
- `PatchService` carries a patch's `spec.config` as JSON in `Patch.config`, and no
  longer converts to and from Talos documents. `Patch.documents` is deprecated and
  never set (see the upgrade notes). `ichor get patches` lists the sections of a patch's
  config and `ichor describe patch` prints it.
- The nodes tile badge counts the nodes that are not ready, and the node rows
  and the node page name a Ready condition's reason, as the list does.
- An API server restart is not counted as a reboot waiting.
- A key Ichor owns but does not set in the cluster's mode shows an empty value,
  where it showed `null`.
- Approving a node or setting its install target on a cluster ichor-ui cannot
  read is reported as not found, or as forbidden, with the cluster's name and
  the way out (`--install-image` names the image yourself), not as an internal
  error.
- ichor-ui's ServiceAccount no longer holds `delete` on `talosmachines`, in the
  Helm chart's role and in `config/rbac`. Removing a node asks for the override
  verb before it deletes, which the ServiceAccount does not hold, and with
  sign-in the signed-in user deletes, so the grant was never reachable. The rule
  keeps `get`, `list` and `update`.
- ichor-ui's ServiceAccount no longer holds `start` on `talosoperations`
  (`shutdown`, `ca-rotation`, `key-rotation`, `etcd-restore`, `node-reset`,
  `node-shutdown`), in the Helm chart's role and in `config/rbac`. Only the open
  mode used it, and it let anyone who could reach ichor-ui start a CA rotation, a
  key rotation, a cluster shutdown and a node shutdown. The open mode is gone
  (see the upgrade notes). The validating webhook asks Kubernetes about whoever
  wrote the `TalosOperation`, so it passed the ServiceAccount whatever the chart
  gave it; the grant was the only thing in the way, and a chart test now fails if
  any of the six is granted again. The backend refuses a start with nobody to ask
  for all six kinds, where it used to leave the decision to the webhook, and the
  checks the settings, Cilium values and reclaim handlers make about the caller
  answer no when there is no caller, where they answered yes.
- **ichor-ui's ServiceAccount holds no write verb on the resources a signed-in user
  changes.** It no longer holds `create`, `update` or `delete` on `talosoperations`
  and `talosconfigpatches`, `update` on `talosclusters`, `ichorinstallations`,
  `talosmachines` and `talosetcdbackups`, `update` on `ichorinstallations/settings`
  and on the `ichor-user-credentials` Secret, or `create`, `update` and `delete` on
  `machinereclaims`, in the Helm chart's role and in `config/rbac`. Each of those
  writes was already made as the signed-in user, so what a user can do does not
  change: the write is judged as that user, and the ServiceAccount's grant was never
  the identity behind it. The ServiceAccount keeps its reads, and the writes it
  makes for itself: the Events an override or a download records, the certificate
  and cluster-fact requests it sends the controller, and the patch to the credentials
  Secret.
- ichor-ui refuses `--allow-unauthenticated` and `ICHOR_ALLOW_UNAUTHENTICATED=true`
  at startup, and does not start with neither `--users-enabled` nor `--oidc-issuer`.
  Its read-only calls for an anonymous caller (`readOnlyProcedures`) are gone: an
  interceptor with no authenticator refuses every call, and a test sends every
  method of every service to it. `/auth/config` no longer says `anonymous`, and the
  UI shows an error, not a "Read-only" banner, when no provider is listed.
- The chart fails a render with `ichorUI.allowUnauthenticated` set to true, and
  one of `ichorUI.enabled` with neither `ichorUI.users.enabled` nor
  `ichorUI.oidc.enabled`, naming what to set. The installer drops the removed
  flag from a live ichor-ui with a notice and stops at one that has no sign-in,
  saying to run `ichor upgrade ichor`, which sets it up (see the upgrade notes).
- A read or write that Kubernetes RBAC refuses to the signed-in user is answered
  `PermissionDenied`, where it was `Internal` and so reached the browser as "the
  server could not complete the request (reference ...)". `ListClusters`,
  `ListMachines` and `ListPatches` (which list across every namespace, so a user bound
  in one namespace is refused), the patch list `GetEffectiveConfig` reads, the machine
  list `ApproveMachine` reads for a VIP cluster, and the `ApproveMachine`,
  `RejectMachine` and `SetMachineInstall` writes now say so, as `GetMachine` already
  did. A write that hits a conflict is `Aborted`, one the API server finds invalid is
  `InvalidArgument`, and a failure of the server's own stays `Internal`.
- The docs site's dialogs, menus and sheets now carry the fixes the admin UI had: a
  tap outside a layer on a touch screen closes it when the finger lifts (iOS sends no
  click for a tap on a page that is not interactive), a layer opened over another
  closes alone, and a modal no longer pads the page to make up for the scrollbar it
  hides. The two apps vendor the same Radix port, and a UI test now fails when a file
  of `ui/src/lib/radix` and `site/src/lib/radix` differs.
- **A forced first-login password change is refused for a sign-in older than ten
  minutes.** The forced change takes no current password, so it needs a sign-in within
  the last ten minutes. The refusal says so and asks the person to sign in again, and
  the must-change screen now has the account menu's sign-out, so that can be done from
  it. A session that its refresh token kept open no longer makes the change on its own.
- **ID tokens carry `auth_time`, the time of the sign-in.** This is a token-format
  addition: an existing client that does not read it is unaffected, and discovery lists
  it under `claims_supported`. A refresh keeps `auth_time` and re-issues `iat`. A token
  issued before this release has no `auth_time`, so a forced change refuses it until
  the person signs in again.

### Typed configuration

- `TalosConfigPatch` has a new field, `spec.config`: a patch written in Ichor's
  own schema. It is one object with a `machine` and a `cluster` section, as the
  `v1alpha1` document has, and the settings Talos moved into documents of their
  own are nested where they belong by meaning: the kernel modules are
  `machine.kernel.modules`, the user volumes `machine.volumes.user`, the cluster
  network `cluster.network`. A setting that Talos spells in a document and in
  the `v1alpha1` document is one field, with the spelling of the document, so
  `machine.time.enabled` replaces `disabled`, and `cluster.coreDNS.enabled`
  replaces `disabled`. A kind that is a named thing is a map from its name. The
  controller converts it to Talos documents when it renders a machine.
  It is the only place a patch carries Talos configuration: `spec.documents[]`
  is removed (see Upgrade notes and ADR 0152). The schema is generated from the
  Talos mirror types (`internal/tools/ichorgen`), so a field Talos adds appears in
  it when `make generate` runs. The links are the exception: the `v1alpha1`
  interfaces and the link documents are not joined yet (see Typed configuration).
- A `TalosConfigPatch` can now carry the settings of all 65 Talos document kinds
  Ichor accepts, up from 7 (`SysctlConfig`, `KernelModuleConfig`, the
  `v1alpha1` document, `VolumeConfig`, `UserVolumeConfig`, `RawVolumeConfig`
  and `ResolverConfig`). The 58 added cover network links and routing (bond,
  bridge, VRF, veth, dummy, Ethernet, BGP, blackhole routes, routing rules,
  Layer 2 VIP, DHCPv6, static hosts, KubeSpan, probes, time sync), storage
  (existing, external and swap volumes, LVM, RAID, filesystem scrub and trim,
  zswap, the image cache), the node (sysctl, sysfs, udev, environment, etc
  files, extension services, containers, event sinks, kernel log, OOM,
  watchdog, PCI driver rebind, trusted roots, registry mirrors, image
  verification) and Kubernetes (the API server, controller manager, CoreDNS,
  network, node, KubePrism, Talos API access, inline and external manifests,
  audit policy, authorizer, credential provider, kubelet, kube-proxy,
  kube-scheduler and static pods). `docs/reference/crds/talosconfigpatch.md` lists every setting.
- **A bond cannot be written in both spellings.** A `TalosConfigPatch` that sets a bond
  as a `BondConfig` in `spec.config` and also as an entry of `machine.network.interfaces`
  in the `v1alpha1` document is refused, unless that entry carries only `primary` and
  `deviceSelectors`. The refusal names the bond and the settings the `v1alpha1` entry
  carries, and says the `BondConfig` owns every setting of the bond, the eight only it
  spells (`hardwareAddr`, `nsIp6Targets`, `adLACPActive`, `missedMax`, `up`, `multicast`,
  `addresses[].routePriority`, `routes[].table`) and each setting both spell alike, while
  the `v1alpha1` entry owns `primary` and `deviceSelectors`, which a `BondConfig` cannot
  carry. The reason: Talos loads both without an error, gives each setting the `BondConfig`
  carries its value, and resets a setting the `BondConfig` leaves unset to zero, so the
  `v1alpha1` value of the same bond is lost. The check covers bonds only, and runs at
  admission.
- **A `BondConfig` enumeration must name a value of its table.** `bondMode`,
  `xmitHashPolicy`, `lacpRate`, `arpValidate`, `arpAllTargets`, `failOverMac`, `adSelect`,
  `primaryReselect` and `adLACPActive` are refused above the largest value their Talos
  table has, and the message names the field and that value. `{"bondMode": 9}` used to
  pass, and the node then ran a mode no table names.
- The other 25 kinds Talos registers are not available, on purpose. 10 carry a
  credential (registry authentication and TLS, the CA and signing keys,
  the encryption key, WireGuard, SideroLink, Hetzner Cloud and the discovery
  identity), and a custom resource is stored unencrypted in etcd. 12 are
  rendered by Ichor from its own spec (links, VLANs, DHCPv4, the hostname,
  the firewall, discovery, the security profile, the install, Flannel (Cilium
  is the only CNI), and the API server's authentication configuration, whose
  `configuration` replaces rather than merges, so a patch would drop Ichor's
  own issuers). 3 have no schema, being free-form text or one configuration
  per plugin (`CRIBaseRuntimeSpecConfig`, `CRICustomizationConfig` and
  `KubeAdmissionControlConfig`); Pod Security exemptions and versions go in
  `spec.security.podSecurity`.
- `KubeStaticPodConfig` carries its pod as a Kubernetes `Pod` object: the
  `apiVersion`, `kind`, `metadata` and `spec` of the document, without the
  `status` the kubelet reports. A static pod needs a security context, host
  networking, host paths and capabilities, so its spec is carried whole. The
  schema is large, and `TalosConfigPatch` is one of the largest CRDs: `make
  install` and `make deploy` therefore apply the CRDs server-side, and so must
  anyone who applies them with `kubectl apply`: client-side apply keeps the
  whole object in its last-applied annotation, which is limited to 262,144
  bytes, and refuses them.
- `KubeletConfig`, `KubeProxyConfig` and `KubeSchedulerConfig` carry a subset
  of the upstream configuration: every field a CRD schema can express and Talos
  accepts, which is all but a handful. The upstream types contain floating point
  numbers, a quantity type and a list with no list type, which a CRD schema
  cannot express, and the scheduler's is large. A Talos document that sets a field the subset does
  not carry is refused, at any depth, naming the field, and so is one that
  sets a field Talos protects in the kubelet (`authentication`,
  `authorization`, `cgroupRoot`, `port`, `resolvConf`, `staticPodPath` and
  others) or the `image` and `enabled` fields that Ichor owns (the image
  follows `spec.kubernetesVersion`, kube-proxy's `enabled` is
  `spec.cni.kubeProxy`, and a patch cannot switch the kube-scheduler off). The
  scheduler's plugin arguments (`profiles[].pluginConfig`) cannot be set.
  `kubectl` refuses a manifest with a field outside a subset, naming its path,
  because it validates strictly by default; a client that does not ask for
  strict validation has the field removed by the API server without being
  told. The tables of omitted fields in `internal/tools/mirrorgen/omits.go`
  are the list. A field the subsets carry cannot be taken back: a
  field that leaves the mirror fails a test unless its line is also removed from
  `pkg/typedconfig/testdata/carried_fields.txt`.
- `KubeAuditPolicyConfig`, `KubeAuthorizerConfig` and
  `KubeCredentialProviderConfig` carry their payload as the upstream
  Kubernetes type, so nothing it can express is lost.
  `KubeExternalManifestConfig` has no `headers` field: it fetches a manifest
  from a URL that needs no authentication, and a Talos document that carries
  `headers` loses them when it is converted to the typed form.
- **Breaking for a patch that uses them:** the `v1alpha1` document no longer
  has `machine.registries.config.<host>.tls.clientIdentity` or
  `cluster.extraManifestHeaders`, in the `TalosConfigPatch` and `TalosCluster`
  CRDs alike. They hold a private key and an `Authorization` header, and v0.2.4
  only refused a change that set them. A stored patch that has either loses it
  when the API server prunes it, and a node that relied on it loses the setting
  at the next apply. `kubectl apply` of a manifest that still sets one is
  refused. Nothing replaces them yet; set them on the node outside Ichor until
  a reference to a Secret exists.
- **Breaking for a manifest that sets it:** `TalosConfigPatch` loses
  `spec.documents`: the union of document kinds as a list. Ichor's own schema,
  `spec.config`, writes every setting once, so the union is a second copy of
  what the schema holds, and it took the chart's Helm release record to 950,492
  of the 1,048,576 bytes allowed while both were there. It is retired as a third
  exception to the append-only rule (ADR 0152), and the record with `spec.config`
  alone is 580,420 bytes, 468,156 free. `kubectl apply` of a manifest that still
  sets `documents` is refused as an unknown field; a client that does not ask for
  strict validation has it pruned. `ichor patch apply` names `spec.config` in its
  refusal.
- **Breaking for a manifest that sets it:** `TalosCluster` loses
  `spec.cni.custom.documents`, a second copy of the whole `TalosConfigPatch`
  document union that sat under the deprecated, refused `custom` CNI and that
  nothing rendered. It is retired as an exception to the append-only rule
  (ADR 0152), and it frees 392,672 bytes, about 40%, of the Helm release
  record: the record for the chart goes from 991,560 to 598,888 bytes of the
  1,048,576 allowed. `spec.cni.custom` and `spec.cni.custom.source` stay, still
  refused with the same message. A stored object that has `documents` stops
  serving it as soon as the CRD is upgraded; nothing used it, so no node
  changes.

### Go module

- `go.mod` retracts v0.1.0, v0.1.1, v0.2.0, v0.2.1 and v0.2.2. `go get` and
  `go mod tidy` do not move a module to one of them, a build that already
  requires one keeps working, and `go list -m -u` reports the retraction.

### Licensing
- **`THIRD-PARTY-NOTICES` names every vendored copy, not only the first of
  each.** The documentation site's component ports, both copies of the sonner
  and next-themes shims, and `ui/src/lib/shims/internal/sonner-styles.ts` were
  carried without attribution. Each now has a test keyed on something in the
  copied code, because the previous check matched only a shim's own header name
  and so could not see an upstream that is named nowhere in the file.

- `NOTICE` names `api/v1alpha1/zz_generated.talosconfigpatchdocument.go`,
  which holds the generated union and carries the MPL-2.0 header, with the
  other files derived from Talos.
- Every page of the docs site links the licences in its footer, and the site
  publishes `NOTICE`, `LICENSE` and `THIRD-PARTY-NOTICES` as text files.
- `THIRD-PARTY-NOTICES` lists the Sigstore public-good trusted root that the
  binary embeds, and the build refuses a dependency under the GPL, LGPL, AGPL,
  SSPL or BUSL.
- The Gateway API CRDs that `ichor init --gateway-api` applies are embedded with
  their upstream copyright notice, and `THIRD-PARTY-NOTICES` names them.
- `THIRD-PARTY-NOTICES` names both vendored copies of Radix UI Primitives, in
  `ui/src/lib/radix` and in `site/src/lib/radix`.
- `THIRD-PARTY-NOTICES` names cmdk, which `ui/src/lib/shims/cmdk.tsx` and
  `site/src/lib/shims/cmdk.tsx` port, with its MIT copyright line.
- `THIRD-PARTY-NOTICES` names Superhuman's command-score, whose scoring function
  the same two cmdk ports carry at their end, with its MIT copyright line.

### Docs
- **A new guide, Prepare Talos nodes for Rook/Ceph, covers the Talos side of a
  Rook/Ceph node.** It says which disk Rook needs unformatted, how the `rook-v1`
  preset differs from the raw-partition alternative, what the Pod Security exemption
  is for, and what Rook's drain gate does. Naming the devices Rook takes is yours.
  Rook and Ceph themselves are out of scope.
- **ADR 0167 records Rook/Ceph as a second storage preset.** ADRs 0023 and 0025 are
  superseded in part by it, and the storage design section names `rook-v1`. Its
  amendment states the two `diskSelector` rules as the API enforces them.
- **Twenty amendment paragraphs across twelve ADRs carry a dated heading and the
  index marker.** The index reads amendments from the heading, so these were
  recorded but invisible to it. Each kept its original date.
- **ADRs 0055 and 0006 say that the Argo CD Application they describe is gone.**
  ADR 0101 removed it; both still described it in the present tense, and 0006
  named a chart example that no longer exists.
- **ADR 0023 says when a second storage preset needs more than a new file, and ADR
  0025 gives the real reason it does not read `ScheduledReplica`.** The claim that a
  preset is "a new `Preset` implementation and one new `case`, nothing else" does
  not hold for `longhorn-v2`, whose disk is named per node, an input the `Preset`
  interface does not take. ADR 0025 now says that `ScheduledReplica` is emptied when a
  disk is not Ready, so it cannot tell a finished eviction from a disk that went away,
  and that Longhorn's node webhook judges the object a write leaves, not the number
  of writes. Ichor still writes the two eviction values as two updates, in order.
- **ADR 0025 and the storage design no longer say that a hook can evict one disk.**
  Only whole-node eviction is built: nothing writes a Longhorn disk entry, and
  per-disk eviction is recorded as not built, with no promise that it will be.

- The guides, concepts, getting started, the design document, the diagrams and
  the README name the verb-first commands, and say that `adopt` takes Talos
  v1.14 and later, that sign-in is the `init` default, and that a load
  balancer's name or address is a `node` endpoint.
- `create-the-cluster` is one page per endpoint, the virtual IP first, then the
  single node endpoint, which also covers a load balancer in its own section.
  Each page shows the interactive, `init.yaml` and flag forms as its sections.
  Getting started is one page per step, and the architecture reference is one
  page per section.
- Four guides are sections now, not single long pages, and each has a diagram.
  Install with Helm, Adopt a cluster, Hardening and Users and login keep an
  orientation page with the overview, the list of their pages and the path drawn:
  the install path, the five adoption phases, what each hardening option does to the
  nodes, and the sign-in path. The steps and options are pages under
  `install-with-helm/`, `adopt-a-cluster/`, `hardening/` and `users-and-login/`. The
  anchors other pages link to stay on the orientation pages: `#network-policies` and
  `#the-installer-image-on-an-upgrade` on Install with Helm,
  `#what-you-have-in-front-of-you` and `#add-a-machine-that-is-already-in-the-cluster`
  on Adopt a cluster, and `#client-addresses-behind-a-load-balancer` on Users and
  login. Another heading of those guides is now on a page of its own, so a link to it
  no longer reaches it.
- A new page, Certificate names, states what each serving certificate holds by
  default and what changing the names costs.
- The CNI concept page and `ichor bootstrap cni --help` say that Cilium reaches
  the API through KubePrism only when kube-proxy is replaced.
- The users-and-login and hardening guides, `ichor rotate ca --help` and ADRs
  0091, 0127 and 0129 say what `ichor create credentials --bundle` needs: the
  bundle, the API server's address (`--kube-server`) and a Talos endpoint
  (`--talos-endpoints`). The bundle holds the CAs and no addresses.
- `ichor --help`, the CLI and UI page and the architecture overview name the
  three commands that write to a Talos node, `init`, `restore cluster` and
  `reclaim node --talosconfig`. `adopt` only reads from nodes. ADR 0014 is
  amended: its list of exceptions named `adopt` and left out `reclaim`.
- The alt text of the architecture diagram, in the overview and on the
  documentation home page, says "in steady state only the controller configures
  the Talos machines", so it no longer contradicts the caption under it, which
  names the three CLI commands that also write to a node.
- The Helm install guide no longer calls the controller "the only writer to
  Talos nodes" when it explains the priority class.
- ADR 0158 points at the omit tables in `internal/tools/mirrorgen/omits.go`
  instead of enumerating fields and sizes, which had gone stale.
- ADRs 0158 and 0165 point at `internal/tools/releaserecord/` as the procedure
  that measures the Helm release record, which each had only in part. The
  figures in them are of earlier trees; the tool re-measures them.
- `CONTRIBUTING.md` names `GATE_CONTEXT` and `GATE_NAMESPACE`, the variables
  `make remote-gate` takes its cluster and namespace from.
- The guide on adopting a cluster is `guides/adopt-a-cluster.md`, no longer
  `adopt-and-reclaim.md`: resetting a machine has its own guide. A link to the
  old address stops working.
- ADRs name `ichor delete node`, `ichor resume operation` and `ichor upgrade
  ichor` where they named the removed `ichor node reset`, `ichor operation
  retry` and `ichor self-upgrade`, and a test keeps those three out. The same
  test keeps `ichor gitops export`, `gitops handoff`, `images list`, `stack
  apply` and `machine reprovision` out of every ADR except those that decided,
  built or recorded the removal of that command. Another test applies the same
  list to the guides, the README and `CONTRIBUTING.md`. The architecture diagram
  labels the controller the writer to nodes in steady state. Commit
  references in ADRs and tests point at commits on `main`, not at the earlier
  copies of them.
- The documentation home page, the architecture overview, the security concept
  page and `CLAUDE.md` say that the controller is the only writer to a node
  apart from `init`, `restore cluster` and `reclaim node --talosconfig`, and a
  test fails for any document that claims a sole writer without naming all
  three. ADRs 0014, 0047, 0076, 0085, 0089, 0106 and 0118 are amended where
  they name `restore --recover-cluster` or `restore --live`.
- A test in `pkg/release` fails when a guide states a Talos, Kubernetes or
  Cilium version that is not the one `pkg/release` pins.
- The architecture page on nodes states the removal rules and no longer carries
  the notes taken from Talos's source: ADR 0010 holds them, with ADR 0042. ADR
  0053 says its command is `ichor restore cluster`, and ADR 0096 says that
  `secretsRef` names the whole secrets bundle, not only the CAs.

- **A code fence can name its file and number its lines.** After the language, a fence
  takes `title="…"` and `lineNumbers`, in either order, and the docs site renders the
  block with that title and those numbers. Any other word after the language, and a
  `{…}` line list, which is not supported yet, fails the docs build with the file and
  line. The contributing guide shows both options.

### CI
- **An envtest suite that cannot find its binaries now says how to get them.**
  The six suites failed with `fork/exec /usr/local/kubebuilder/bin/etcd`, a path
  nobody configured, and said nothing about `make setup-envtest` or that
  `KUBEBUILDER_ASSETS` wants the version directory rather than `bin/k8s` itself.
  They still fail rather than skipping, so a run with no environment cannot
  report success.
- **The envtest suites start the Kubernetes minor the Makefile names.** A
  binary directory for an older minor under `bin/k8s` was picked by name order,
  and `KUBEBUILDER_ASSETS` hid the mismatch. The minor now comes from the
  `k8s.io/api` version in `go.mod`, the same source as `ENVTEST_K8S_VERSION`.
- **The four chart tests that compare against `dist/install.yaml` no longer skip
  when it is absent.** The file is generated by `make build-installer`, which
  `make test` depends on, so the skip was hiding a missing build step rather
  than a condition that legitimately does not apply. One of them had never run
  its seven chart-side subtests at all.
- Renovate's configuration covers the Go modules, both npm trees, the workflow
  actions, `flake.lock` and the distroless digest, grouped per ecosystem and
  proposed weekly with majors held on the dashboard. The release-set modules are
  proposed in their own labelled group and never merged automatically, as ADR
  0151 decides; `make renovate-validate` checks the file.

- `make cluster-gate` runs the full gate as one Kubernetes Job. A second run
  reuses the cache claim of the first, and stops with a message instead of
  replacing it when `GATE_STORAGE_CLASS` or `GATE_CACHE_SIZE` differs.
- The target that embeds the install manifest in the CLI refuses the
  placeholder images `controller:latest` and `ichor-ui:latest`, naming the
  variable that is still one, because a binary that embeds them names images no
  cluster can pull.
- The release workflow takes the notes from this file, and CI fails if a
  top-level `changelog:` block returns to the goreleaser configuration, which
  would make goreleaser ignore them. After a patch release cut from a
  `release-X.Y` branch, a workflow opens a pull request that brings its notes
  and README version to `main`. A dependency scan runs on `main` every day.
- `make check-release-record` fails when the chart's Helm release record, which
  Helm stores in a Secret limited to 1,048,576 bytes, is over that limit, and
  prints the size and the headroom on every run. CI runs it with the tests.
- `make remote-gate` fails when `GATE_NAMESPACE` is unset. The Makefile supplied
  a default, which made the script's own check unreachable.
- `make docs-check` runs `make docs-audit` first, which fails on a high or
  critical advisory in the docs site's dependencies, the `shadcn` tool included.
  `npm ci` no longer prints its own count for the site. The site pins
  `postcss-selector-parser` and `minimatch` to their patched versions with
  `overrides`, which cleared five of the eight advisories `npm ci` reported; the
  rest are `deepmerge`, which has no fixed version, and two packages that depend on
  it, all reached only through the `shadcn` command line tool.
- The Makefile's `go-install-tool` rebuilds an installed tool when the Go that
  built it is not the Go in use. Outside `nix develop`, where the shell's Go is
  older, the first `make` after this rebuilds all four installed tools.
- `make docs-check` fails when a branch or group of the docs sidebar has no check
  of its own, and checks that the phone drawer locks the page scroll and that
  Escape or a press beside it closes it. The site pins Preact to 11.1.1, as the UI
  pins 10.29.8, because the vendored portal reads Preact's internals, and a test
  fails if either app's pin becomes a range. The UI's field search tests skip,
  naming `make ui-fieldindex`, when the index has not been written, where they
  failed with `ENOENT`. A test fails for a `t()` call that passes its key through
  a type assertion.
- `make test` fails when a diagram in `docs/images` has a label that is HTML rather
  than plain SVG text, naming the file and the cell. ADR 0132 records the rule and
  its cause, and the contributing guide's Diagrams section says how to fix a cell.
- `make test` fails when a write the ichor-ui ServiceAccount makes through its own
  client has no grant in `config/rbac/ichor-ui/role.yaml`. The one write the role
  does not grant is an exception that names its reason, and the check fails once it
  matches nothing. A grant's `resourceNames` and namespace are not compared.

- **`make ui-shots` can photograph pages behind sign-in, and fails rather than write a
  picture of a sign-in form.** It signs in through a stand-in backend. A run writes no
  screenshot and fails when the page it reaches is the sign-in form, a page that has
  dropped its token, or one still loading when the wait cap is hit.

## v0.2.4 (2026-11-13)

Fixes to removing a node and to how Ichor addresses one, and a check on config
patch documents. The Talos, Kubernetes and Cilium versions a first install gets
are the same as in v0.2.2, and no CRD field changed.

### Nodes

- Before Ichor wipes a node, makes it leave etcd, applies a configuration to
  it, upgrades it or runs a node action on it, it reads the node's machine UUID
  over the same connection and compares it with `spec.identity.uuid` (ADR
  0153). A UUID that differs, an empty one and an Identity call that fails each
  refuse, and nothing is sent. An address recorded for one machine can answer
  as another control plane that trusts the same CA; a removal used to take the
  Kubernetes Node and etcd member to remove from whoever answered. It now takes
  the name from `spec.hostname`, then `status.nodeName`, and from the node's own
  report only once that node has passed the check.

### Removal

- A control plane removal's drain ends after 90 seconds, records `DrainFailed`
  in the Removal condition and is tried again after 15 seconds. It used to run
  without a time limit and ask again at once after every refused eviction, so
  a PodDisruptionBudget that allows no disruption held the single worker and
  the cluster lock until the pod restarted. A refused eviction is now retried
  after 5 seconds, in the rollout's drain too, and a drain no longer waits for
  a pod that has been terminating for a minute.
- A removal that is waiting for the cluster lock or for a shutdown to finish
  asks to be tried again after 10 seconds. Nothing woke it when the lock was
  released, so it stayed deferred.
- The removal of a dead control plane finishes. The decision to go on below the
  declared count is recorded in the Removal condition's reason, and the reset
  step replaces that reason; the next round then counted the control planes
  again, refused, and left the finalizer on a node that was already wiped. A
  reset that Talos accepted now skips the count check, and the next attempt
  after a failed reset still counts as going on for the dead member.

### Typed configuration

- A `TalosConfigPatch` document whose `kind` names a field that is not the one
  set, or that has a second field set, is refused with an error naming the kind
  and the field. It used to convert to no document at all and no error, which
  panicked the first caller to use it. The CRD's own validation already refuses
  such a document at admission; this covers one that is stored without it or
  built in process, and the admission webhook's dry render gives the same
  answer.
- A `TalosConfigPatch` document's `config` (the `v1alpha1` document) can no
  longer set `machine.registries.config.<host>.tls.clientIdentity`, a private
  key, or `cluster.extraManifestHeaders`, which in practice holds an
  `Authorization` header. A custom resource is stored unencrypted in etcd and
  readable by anyone who can read it; credentials belong in the secrets bundle.
  A create or update that sets or changes either is refused, naming the field.
  A patch that already has them keeps them, can still be edited for something
  else, and can have them removed. The fields stay in the CRD.

## v0.2.3 (2026-11-13)

Licence notices. The Talos, Kubernetes and Cilium versions a first install gets
are the same as in v0.2.2, and no CRD field changed.

### Licensing

- `NOTICE` says where the source of the MPL-2.0 covered files is obtained, at
  the Git tag that matches the version of the artifact, and names the files
  that hold schema derived from Talos and cannot carry a header: the two CRD
  manifests, their copies in the Helm chart and their reference pages.
- The deep copy methods of `TalosConfigPatchDocument` are written next to the
  type, so `zz_generated.deepcopy.go`, which carries the Apache-2.0 header,
  names no Talos type.

## v0.2.2 (2026-11-12)

Fixes for `ichor-ui`'s log stream and for `ichor init`. The Talos, Kubernetes
and Cilium versions a first install gets are the same as in v0.2.1, and no CRD
field changed.

### ichor-ui

- A `resume_token` that decodes to nothing no longer ends the process. The
  token is client-supplied, and a request carrying one could stop every
  `ichor-ui` replica, so anyone running an earlier release should move to this
  one.
- A request for node logs is limited to 32 machines and its list is
  de-duplicated, and the number of log streams the backend serves at once is
  bounded, the way downloads already were.
- The kernel log is masked before it is streamed, so a `talos.config` URL in
  the kernel command line does not reach a reader. The support bundle masks the
  same line when the kernel writes it without a colon.

### ichor init

- `init` refuses to replace a secrets bundle that already exists for the
  cluster. The bundle may be the only way to reach a node configured with it,
  and a rerun used to overwrite it before asking anything.
- A run that fails before the node is sent anything removes the bundle it just
  wrote, so the usual first-run failures can simply be run again.

## v0.2.1 (2026-11-12)

Fixes for `ichor adopt` on a real cluster, clearer command output, and
documentation. The Talos, Kubernetes and Cilium versions a first install gets
are the same as in v0.2.0, and no CRD field changed.

### Adopt

- `spec.certSANs` holds only the SANs that every node has, both in its
  `machine.certSANs` and in its kube-apiserver SANs. Before, adopt could put a
  SAN there that every node then had to carry, and a cluster whose nodes
  differed in their SANs stayed blocked. What a node has beyond the shared list
  goes into that node's own patch, so no certificate changes.
- When the captured configuration still cannot reproduce a node, the plan names
  the paths that differ, not everything it captured.
- When adopt finishes it says what the machines are now (managed by Ichor, so
  change their configuration through Ichor and not on the node) and what to run
  next: `ichor cluster status`, `ichor machine list`, `ichor ui`.
- A run that stops part-way says so and that the same `ichor adopt` can be run
  again; it continues where it stopped, and machines that are still held have
  had nothing applied.
- A machine that needs a reboot to apply a pending change is reported at once,
  with the `ichor config apply` command to run, and adopt goes on with the other
  machines. Before, it waited out the 10-minute timeout and stopped there.

### CLI

- Notices wrap to the width of the terminal. A word longer than the line, such
  as a path or a field name, is not cut.
- A warning is separated from a following note by a blank line.
- The short machine name that `ichor machine list` shows (the first eight
  characters of the UUID) works in the commands that take a machine name. A
  short name that fits more than one machine is refused, and the candidates are
  listed.

### Helm chart

- The packaged chart no longer carries the chart's own Go tests and their
  fixtures.

### Docs

- Getting started: unpack the whole release archive into a directory of its own,
  explain the sign-in warning and how to log in to ichor-ui, caution that the
  pod and service subnets must not overlap the nodes' network and cannot change
  later, say that `machine list` shows short names and that they work in
  commands, and fix the cosign link.
- Users and login links the design decisions it builds on.
- The contributing guide moved into the development section.

### CI

- The GitHub release body is the release's section of this file.

## v0.2.0 (2026-11-11)

`ichor adopt` now works on an ordinary existing Talos cluster. Before this
release a normal three-control-plane cluster was blocked on six configuration
paths and nothing could be adopted.

### Adopt

- Adopt takes the node's hostname into the `TalosMachine`.
- Adopt captures the node's legacy `machine.network.interfaces`, its
  `ResolverConfig` and its own `machine.certSANs` into the machine's
  `TalosConfigPatch`. The node's own configuration is not changed and no node
  needs a reboot.
- A cluster whose control planes lack Talos's default
  `node.kubernetes.io/exclude-from-external-load-balancers` label keeps lacking
  it. Adopt sets the new `TalosCluster` field
  `spec.controlPlane.omitDefaultNodeLabels` automatically; labels you set in
  `spec.nodeLabels` or in patches still apply.

### Configuration

- A `TalosConfigPatch` can carry a `ResolverConfig` document (DNS nameservers,
  search domains, host DNS).
- New in the API: `TalosCluster` `spec.controlPlane.omitDefaultNodeLabels`.

### Docs

- The site publishes one copy per minor release line (`/v0.1/`, `/next/`), so
  documentation fixes reach readers without a new release. Old per-tag URLs
  redirect.

### CI

- The release workflow verifies and builds in parallel.

### Not covered

- Adopt does not convert a node's legacy network configuration to the
  multi-document form; the node keeps the legacy form until you change it.
- WireGuard devices in a legacy network configuration are refused.
- On the v1.14 contract, kube-apiserver SANs that differ between control planes
  still block adoption.
- A cluster whose control planes disagree about the default node labels is not
  detected; the nodes that lack the label keep blocking.

## v0.1.1 (2026-11-11)

Documentation and release pipeline only. Nothing in the operator, the CLI or
ichor-ui changed, so there are no behaviour changes for users.

### Docs

- The decision records stay in the repository under `docs/adr`, but the docs
  site no longer publishes them.
- Tables on the docs site wrap at word boundaries in every column.

### CI

- The release workflow runs verify and build in parallel and pushes only after
  both pass.

## v0.1.0 (2026-11-07)

First release. Alpha.

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
  `ichor machine approve`. Approval can set the NIC, addressing and disk roles. Discovery reports a configured node at the address of a Kubernetes
  Node that no `TalosMachine` accounts for as `Orphan`, a distinct Event.
- Removing a node wipes it. `ichor node remove NODE` (the UI's "Remove
  node") needs the typed machine name and the override permission. It drains the
  node, removes its etcd member (control plane), wipes STATE and EPHEMERAL, then
  powers the machine off, or reboots it into maintenance mode with `--reboot`
  ("After wiping" in the UI), and deletes its Kubernetes Node. `--wipe-mode
  system-partitions|system-disk|all` (the UI's "What to wipe") chooses what the
  wipe erases: STATE and EPHEMERAL (default), the whole system disk (so a
  SecureBoot node can be installed afresh from the ISO, see the hardening guide),
  or the system disk and every user disk. `--confirm` takes the machine name (the
  TalosMachine name the UI dialog shows), not the hostname; NODE takes the machine
  name, hostname or Node name. A node that does not answer is removed unwiped, its
  Node is deleted and an Event says so; a Node that stays Ready is left with a
  Warning Event naming the `kubectl delete node` step. The controller needs
  `delete` on Nodes.
- Typed machine configuration: patches built on the Talos machinery types, no
  raw YAML. Drift is reported; changes that need a reboot roll out as one
  operation, a node at a time.
- Upgrades of Talos, Kubernetes, Cilium and Ichor itself, one node at a time
  with etcd quorum in mind; a failed step pauses the operation. Cilium moves only to the chart this Ichor embeds (below). A
  TalosOperation that upgrades Ichor records the version it goes to in
  `status.toVersion`, and the UI and CLI show "Upgrade Ichor from X to Y" in every
  phase.
- Automatic patch upgrades, off by default, inside a daily window you set.
- A notice in the UI and in `ichor version` when a newer Ichor release is
  published; it can be switched off.
- CA rotation for the Talos API and Kubernetes API CAs, in passes
  that wait for your new credentials. `--ca etcd` and
  `--ca kubernetes-aggregator` (or `--all`) also replace the etcd CA and the
  aggregator CA. The etcd members load the new CA by a reboot, one
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
  of a snapshot older than a finished rotation is refused ([guide](docs/guides/rotate-keys.md)).
- etcd backups on a schedule or on demand, restore into a running cluster, and
  rebuild from the secrets bundle and a snapshot alone (`ichor restore`).
- Adopt a running Talos cluster with zero drift, and reclaim a machine that
  another cluster configured; install Ichor with Helm.
  `ichor adopt --add-machine ADDRESS` adds a machine that already carries a
  managed cluster's CAs (it joined with your own tooling, or its `TalosMachine`
  was deleted) without a reset; `--cilium-values` carries your Helm values into
  a Cilium takeover; a cluster on another CNI is refused with the options
  written down (Ichor does not migrate it).
- A machine that was removed without a wipe (its `TalosMachine` deleted, the node
  still carrying the cluster's configuration) can be wiped through Ichor: `ichor machine reclaim ADDRESS --cluster NAME` without `--talosconfig`,
  or the UI's Reset and add with "It was removed from this cluster", uses the
  credentials Ichor holds for that cluster, wipes STATE and EPHEMERAL, deletes the
  machine's Kubernetes Node and leaves it as a pending machine. The same quorum
  and last-control-plane refusals apply; a machine that still has an approved
  `TalosMachine` is refused with a pointer to Remove node. Orphans (see Discovery
  and approval) are listed on the Add node page with Reset and add set to the
  cluster's own credentials, and the Nodes page's Reset and add offers "It was
  removed from this cluster". The lists drop addresses that a pending or approved
  machine has, the UI inspection shows the orphan warning the CLI prints, and
  nothing is wiped without the typed UUID. `MachineReclaim` takes
  `spec.clusterCredentials`, and `spec.credentialSecretRef` is optional with it.
  `ichor adopt --add-machine` of a blocked machine says how to wipe it too.
- Cilium as the only CNI, kept as a Helm release; its values, upgrades and
  kube-proxy mode are managed from the UI or `ichor cni`.
  Cilium is pinned to the chart embedded in the running Ichor: a cluster moves
  to that chart and to nothing else, so there is one tested combination per
  release, and a recorded patch newer than the embedded chart is never stepped
  back. A daily CI run opens a pull request when Cilium publishes a
  new patch of the pinned minor, after signature and digest checks; a person
  merges it and cuts a release. The Cilium operator runs as many
  replicas as the cluster has nodes, and a Helm Job that keeps failing with the
  same values backs off.
- ichor-ui: a Preact web UI served over TLS only, and optional Ichor-owned
  users with the roles viewer, operator and admin, personal kubeconfigs and a
  forced password change at first login. The browser session
  renews itself with a refresh token (up to the issuer's refresh lifetime, 12
  hours by default) instead of ending after 5 minutes, a password change keeps
  the tab signed in, and signing out, in the UI or with `ichor auth logout`,
  revokes the refresh token at the issuer's new RFC 7009 endpoint
  `/oidc/revoke`.
- Hardening of Ichor itself and typed, off-by-default hardening options for
  the clusters it creates.
- SecureBoot, UKI and kernel lockdown as typed options
  (`spec.security.secureBoot` `Off|Preferred|Required`, `spec.security.kernelLockdown`):
  approval defaults to the SecureBoot installer, a Talos upgrade picks the right
  installer variant per node, `Required` holds the first configuration of a node
  that did not boot with SecureBoot, and per-node and cluster `SecureBoot` and
  `KernelLockdown` conditions plus the Talos details in the UI show what each node
  reports. Ichor never changes a node's SecureBoot state in place.
- The installer image of an Ichor upgrade runs by digest and its keyless release
  signature is verified, offline against an embedded Sigstore root, before the
  installer Job is created. `ichor upgrade ichor` and `ichor init` take
  `--installer-digest` and `--skip-signature-check`, a bare flag (recorded, shown
  in `ichor version`, the plan and the UI); the API server refuses an installer Job that names a tag. The
  release workflow pushes the controller and ichor-ui images first and builds the
  installer last, so the install manifest embedded in it names both by digest.
- FQDN egress and a closed metrics port: the controller writes a
  `CiliumNetworkPolicy` that lets ichor-ui reach only the OIDC issuer its
  Deployment names (by name, `toFQDNs`), the static policies carry no `world`
  egress, and the controller's metrics port is open only to the namespaces you
  name ([guide](docs/guides/install-with-helm.md#network-policies)).
- The local secrets bundle is encrypted: `ichor init` and `ichor adopt --recover-secrets` seal
  `~/.ichor/clusters/<name>/secrets.yaml` as an armored age file under a
  passphrase (`--bundle-passphrase-file`, `$ICHOR_BUNDLE_PASSPHRASE`, or one
  generated and shown once on a terminal; `age -d` opens it). `ichor secrets
  encrypt` migrates a plaintext file; the retired keys of a key rotation are
  sealed the same way.
- Each operation takes the cluster lock under its own identity, as does a
  control-plane join or removal under the machine's, so two disruptive pieces of
  work never run together even when created with `kubectl` or GitOps; the one
  that waits pauses with `ClusterLockHeld` naming the holder, and the lock of a
  deleted, completed or failed-paused holder is taken over.
- An approved machine that converged and then came back in maintenance mode with
  its approved identity (its disk was wiped or replaced) is re-provisioned by
  the controller, with a Warning Event; a stale etcd member of a control plane
  is removed first. `hold-apply` stops it.
- Kubelet serving certificates signed by the Kubernetes CA
  (`spec.security.kubeletServingCertificates`): the controller approves each
  kubelet's serving-certificate request itself, only for that node's own names
  and addresses, and the kube-apiserver can then verify the kubelets.
- `ichor cluster talosconfig` and a UI download give operators and admins a
  short-lived (1 hour, at most 12) `os:reader` talosconfig for `talosctl`,
  without access to the secrets bundle; each request is an Event with the
  user's name.
- After a node is wiped, discovery looks again every 15 seconds for 3 minutes and
  wakes on the Node, so the machine shows up as pending soon after it is back in
  maintenance mode; a pending machine whose node was wiped has its pinned
  maintenance certificate and inventory refreshed by discovery, so
  `ichor machine reclaim` does not delete it.
- Admission enforces what the UI checks, so a CR written with `kubectl` gets no
  shortcut. The `TalosCluster` webhook validates
  `spec.cni.cilium.values` (schema, unknown and Ichor-owned keys, size, the
  chart's own template checks) on create and when the values, kube-proxy mode or
  tunnel change, and the controller checks the values again before every Cilium
  Helm Job: a refusal starts no Job and shows as `CNIValuesApplied=False`. A new
  `IchorUser` webhook holds the role-grant rule (a requester grants only roles
  below their own, viewer < operator < admin; admin grants any) and refuses to
  remove or demote the last admin. An `operator` can create viewer and operator
  users.
- A failed `TalosOperation` is retried with `kubectl annotate talosoperation NAME
  ichor.maesh.dev/retry=true`, which the controller acts on once and removes.
- Cilium's optional features work through the Helm Job: Gateway API, ingress,
  Hubble UI, clustermesh and BGP control plane run with the rights they need.
  Values that cannot work are refused at admission with the reason:
  `gatewayAPI.enabled` needs kube-proxy replacement, and
  `clustermesh.useAPIServer` needs the `cronJob` certificate method.
- `make remote-gate` runs the full test gate as sharded Jobs in a Kubernetes
  cluster, with infrastructure failures reported apart from test failures.
- Release artifacts are built with `make release VERSION=v0.1.0`.
- `ichor init --merge-kubeconfig` merges the new cluster's admin kubeconfig into
  yours as context `admin@NAME` and makes it current; `init` always
  ends by saying which kubeconfig to use.
- `ichor init -f init.yaml` reads the init settings from an `InitConfig` file
  instead of a long flag line (a flag overrides the file;
  `--print-config-template` prints an example). `ichor init` also
  waits for ichor-ui to roll out before it returns, and does not need the hidden
  `--cni` flag.
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

### UI

- A typed confirmation shows the name it asks for as selectable text with a
  copy button.
- Node action dialogs and operations are titled with the hostname, not the
  machine UID.
- Tables stack their columns on phones instead of scrolling sideways (nodes,
  operations, backups, conditions, disks, network, routing, processes, Cilium
  values and the effective config).
- Select lists anchor to their trigger and keep its width; only the top layer
  reacts to an outside press, and a touch tap outside dismisses it, so selects
  and dialogs work on iOS.
- ichor-ui shows only what your role can do: the Users page, Add node, discovery
  edits, approve, patch and node actions are hidden from roles that cannot use
  them.

### Notes for anyone who ran a pre-release build

Only for people who ran a pre-release build or wrote scripts against one.

- No free-text reason is asked for anywhere. The UI has no reason
  box, the CLI takes no `--override-reason`, `--reason` or reason for
  `--skip-signature-check`, and the CRDs do not require one (`etcdRestore.reason`,
  and `installerSignature.reason` with `mode: skip`). The fields stay in the
  schema, deprecated and unused. The override verb, the typed confirmations and
  the Events that record who did what still apply.
- Changing the Cilium values needs the `update` verb on
  `talosclusters/cilium-values`, which only `ichor-admin` holds. The
  chart accepts privileged pods and host paths, so whoever sets the values has
  root on the nodes. The UI, the CLI and `kubectl` all obey it; an operator sees
  the values read-only, and `ichor adopt --cilium-values` needs the right too.
  The five `*.secretsNamespace.name` values are Ichor-owned and pinned to
  `cilium-secrets`, as is `tls.readSecretsOnlyFromSecretsNamespace=true`; a
  value for any of them is refused. The BGP control plane's secrets namespace
  is `cilium-secrets`: BGP authentication Secrets go there, not in `kube-system`.
- The local secrets bundle is encrypted by default. `ichor init` prints a
  generated passphrase once on a terminal and waits for Enter; `ichor adopt
  --recover-secrets` seals the recovered bundle the same way. With no terminal and
  no `--bundle-passphrase-file` or `$ICHOR_BUNDLE_PASSPHRASE` they refuse before
  they do anything (fail closed); `--bundle-plaintext` opts out
  with a warning at write time and on every later read. A plaintext file still
  loads, with a warning; `ichor secrets encrypt` migrates it. Lose the passphrase
  and the file is unreadable, so keep it apart from the file.
- The controller's metrics port is closed until a namespace is named (chart value
  `networkPolicy.cilium.metricsNamespaces`, plus `metricsPodLabels` to narrow it
  to the Prometheus pods). An installer or `ichor init` install carries no such
  value; add a `CiliumNetworkPolicy` of your own, for which the network policies
  guide has an example. ichor-ui's `/metrics` is protected by its token check
  alone.
- The static policies allow no `world` egress for ichor-ui. An external
  OIDC issuer is reached through an FQDN policy the controller writes from the
  issuer the ichor-ui Deployment names, which needs Cilium's L7 DNS proxy
  (`enable-l7-proxy`); with the proxy off the issuer is unreachable unless
  `dynamicTalosEgress` is turned off.
- `ichor upgrade ichor` verifies the installer image's signature. Development and
  demo builds (`v0.0.N-gSHA`) are not signed and need `--skip-signature-check`, and,
  when the controller cannot resolve the tag in its registry, `--installer-digest sha256:...`. The skip is recorded and never inherited by
  the next upgrade.
- `ichor machine reclaim` needs no `delete` right on `talosmachines`: discovery
  refreshes the stale pending `TalosMachine` of a node it wiped.
- `rotate-keys` refuses a `--token-settle` below 75 minutes, for the webhook, the
  CLI and the UI alike.
- A plain `ichor node remove` does not reinstall a SecureBoot node, which boots its
  installed system again; use `--wipe-mode system-disk`.
- `ichor machine approve` without sign-in on an `--allow-unauthenticated` install
  is refused for lack of the `approve` right; pass `--local` or sign in.

### Notable fixes

Found while building this release.

- An HA cluster's Cilium operator stayed Pending on the one node that existed
  and a failing Helm Job retried once a minute for ever; replicas follow the
  nodes, the bootstrap manifest of `ichor init` renders for the one node it
  starts on, and the retry backs off.
- The etcd CA rotation hung because etcd does not reload its CA on a service
  restart; members reboot. Key rotation dropped the old
  service-account key before pods had refreshed their tokens and crash-looped
  Ichor's own pods; the settle has a minimum and Ichor's pods are rolled first.
- A node removal follows the machine it started on by UID: when discovery
  recreates the pending machine under the same name, the operation does not pause
  with a refused delete.
- `ichor machine reclaim` and `adopt --add-machine` on a node of another cluster:
  a control plane whose etcd is not running does not hang the inspection (every
  node call gives up after 20 seconds), and a node of another cluster gets the
  refusal that names `ichor machine reclaim` instead of a raw TLS error.
- A fresh install's NodeStarting message does not show the transient "STATE:
  error evaluating disk locator" for the first minute, and a stale
  `AdoptionHeld` condition is cleared once `hold-apply` is removed.
- ichor-ui: the Overview actions stay on while an operation runs and show the
  server's refusal.
- An error is printed once, and the plaintext-bundle warning once per run.
- `ichor node remove` takes `--confirm NAME` as the confirmation
  without a second prompt; `ichor cluster rotate-ca --help` says it does
  the credential hand-off with or without `--follow`; `ichor init` creates the
  `cilium-secrets` namespace before the objects that live in it; the follow of
  an Ichor upgrade rides out ichor-ui's denial of requests while it rolls out.
- A control-plane removal is not blocked by an operation that is parked only
  because the control-plane count is even, which would otherwise block
  the cure it needs. Such an operation resumes by itself when the count is odd,
  and planning a new operation while the count is even is refused with the way
  out instead of creating one that is parked at once.
- `ichor version` prints the no-image skip warning on its own line.

Security, found in review:

- Discovery does not move an approved machine's address. A converged machine
  that fell back to maintenance mode followed any address that answered with its
  identity, so a spoofed twin could have been sent the full configuration. Only
  unapproved machines follow a discovery match.
- A control plane that is re-provisioning keeps the cluster lock until it has
  stored its new configuration, so no other actor can take the lock mid-step.
- The Cilium Helm Job's installer is pinned by a verified digest (above), and the
  Job's ServiceAccount `ichor-cni` is scoped: it may change only the objects the
  pinned chart renders, by name, and holds what Cilium's optional features need
  instead of a blanket escalation.
- A CA-rotation credentials acknowledgement set before the operation asks for it
  is dropped, so it cannot skip the gate.

Robustness:

- The controller and ichor-ui have a 512Mi memory limit, `GOMEMLIMIT` and 5 s
  probe timeouts; on a 3+1 node cluster the controller had restarted six times in
  80 minutes (one OOM kill), and the fail-closed admission webhook was down each
  time. `managedFields` are stripped from cached objects and the Cilium values
  schema is compiled once.
- An aggregator CA rotation waits for every kube-apiserver to restart before it
  verifies a stage, and the credential hand-off keeps retrying while the API
  server restarts, including webhook timeouts and API discovery failures.
- A failed Cilium Job of values the user has since changed is deleted, and
  reverting an edit to the applied values clears a stale `CNIValuesApplied=False`.

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
- Admission checks the Cilium values only as far as the object itself tells. The
  checks that need the machine configuration or the live cluster
  (`bpf.masquerade` with `forwardKubeDNSToHost`, KubePrism in replaced mode, the
  Gateway API CRDs) stay in the UI and the CLI, so a `kubectl` edit of such a
  value is caught by the Helm Job's failure instead.
- `ichor adopt --add-machine` has no UI button. Ichor does not migrate a cluster
  from another CNI to Cilium; `adopt --cni-unmanaged` leaves it as it is.
- A pod that asks for a projected service-account token valid for more than about
  80 minutes is refreshed later than the key-rotation settle and is rejected when
  the old key is dropped.
- The signature of the installer image is checked against a Sigstore root built
  into the controller; after Sigstore rotates its keys an older controller needs
  the recorded skip until the next release.

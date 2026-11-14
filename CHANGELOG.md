# Changelog

## v0.3.0 (2026-11-13)

The command line is verb first, which breaks every script that calls it, and a
`TalosConfigPatch` can carry all 65 Talos document kinds Ichor accepts,
up from 7. The Talos, Kubernetes and Cilium versions a first install gets are
the same as in v0.2.4. The CRDs gain fields, one endpoint type leaves the
`TalosCluster` enum, and two credential fields leave them (see Cluster and
Typed configuration). With `--endpoint-type vip`, each control plane now holds
the endpoint address on its primary link, and `ichor init` takes the first
node's network for it.

### CLI

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
- With a terminal on standard input, `ichor init` asks for what is missing: the
  cluster name, the endpoint type and address, the first node's address, and the
  install disk, picked from the disks the node reports, before anything is
  written. Without a terminal the flags are required, as before. Esc or Ctrl-C
  cancels with nothing written. Enter alone does not pick the disk, which is
  wiped.
- `ichor init` takes the first node's network: `--nic`, `--address`, `--gateway`,
  `--dns` and `--vlan`, with the matching keys `firstNode.nic`,
  `firstNode.addresses`, `firstNode.gateway`, `firstNode.dns` and
  `firstNode.vlan` in the `-f` file. `--address` takes a prefix length, such as
  `192.168.1.10/24`, and may repeat. These apply only with
  `--endpoint-type vip`: init refuses them with any other type, and outside the
  VIP pattern the first node takes its address from DHCP.
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

### Cluster

- **`--endpoint-type vip` puts the endpoint address on the control planes.**
  Ichor renders a `Layer2VIPConfig` for the address on each control plane's
  primary link. A `LinkAliasConfig` selects that link by its permanent MAC
  rather than by the kernel's name for it, so the setting survives the
  interface being renamed or renumbered across a reboot. Once a control plane of
  a `vip` cluster has a primary link, a control plane approved without one is
  refused, and the refusal names `--nic`.
- The endpoint address must fit its type: an IP address for `vip` and `node`,
  either for `loadBalancer`. `ichor init` refuses a mismatch before it creates
  anything. The webhook refuses one on create, and on an update that changes the
  type or the address, so an object stored with a mismatched pair stays
  editable until one of them changes.
- A talosconfig that Ichor hands out names the control planes' addresses as its
  Talos API endpoints, or, when no control plane is left, every machine's. It
  never names the cluster endpoint, whatever its type, because that endpoint
  fronts the Kubernetes API. It is refused, with the reason, when no machine has
  an address.
- **Breaking: `dns` is no longer an endpoint type.** `spec.endpoint.type`
  takes `vip`, `loadBalancer` or `node`. A hostname in front of the control
  planes is a `loadBalancer` endpoint, which takes an IP address or a hostname.
  A `TalosCluster` that still says `dns` fails validation the next time anything
  updates it, so move it first. The address stays as it is:

  ```
  kubectl -n NAMESPACE patch talosclusters.ichor.maesh.dev NAME --type=merge -p '{"spec":{"endpoint":{"type":"loadBalancer"}}}'
  ```

  Ichor renders the same for `dns` and `loadBalancer`, since only `vip` changes
  the configuration. `ichor adopt` types a name, or an address that no node
  has, as `loadBalancer`, and the endpoint prompt of `ichor init` offers no
  `dns`.

### Nodes

- The CLI, the controller, the admission webhooks and ichor-ui say node where
  they said machine: confirmations, plans, errors, command hints, Kubernetes
  event and condition messages, admission denials and the screens of the UI. The
  `TalosMachine` kind, its fields and the names of the CRDs are unchanged.
  Condition types and event reasons are unchanged too, so something that
  matches on a `type` or a `reason` keeps working. Something that matches on
  the text of a message, a condition's `message` or the words of a denial, does
  not.
- A node is changed only once it answers as the machine it is named for, by
  its machine UUID. A reset, an etcd departure, a configuration rollout, a
  Talos upgrade or another node action is refused when it does not, rather than
  sent to whichever machine answered.
- A removal deferred by a held cluster lock or a shutdown retries by itself when
  the lock frees. A dead control plane's removal is no longer refused after its
  node has been reset, which kept the finalizer in place.
- A control plane's drain during removal ends after the rollout's step timeout
  and is retried later, so a PodDisruptionBudget that allows no disruption no
  longer holds the cluster lock until the pod restarts.

### Typed configuration

- A `TalosConfigPatch` document can now be all 65 Talos document kinds
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
  kube-scheduler and static pods). A document is `kind` plus one field named after it
  (`SysctlConfig` has `sysctlConfig`). `docs/reference/crds/talosconfigpatch.md`
  lists every kind.
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
- `KubeStaticPodConfig` carries its pod whole, as the Kubernetes `PodSpec`: a
  static pod needs a security context, host networking, host paths and
  capabilities, so a subset would not do what the kind is for. The schema is
  large (about 300 KB of compact JSON in each of the two CRDs that embed the
  union), so `make install` and `make deploy` apply the CRDs server-side.
  Client-side apply keeps the whole object in its last-applied annotation,
  which is limited to 262,144 bytes.
- `KubeletConfig`, `KubeProxyConfig` and `KubeSchedulerConfig` carry a subset
  of the upstream configuration: the fields an operator is documented to set on
  a Talos node. The upstream types contain floating point numbers, a quantity
  type and a list with no list type, which a CRD schema cannot express, and
  the scheduler's is large. A Talos document that sets a field the subset does
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
  are the list.
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
- Admission of a document changed. The CRD now requires `kind` plus one field
  (at least and at most two properties), and the webhook checks that the field
  is the one `kind` names; it runs on create and on an update that changes
  `spec.documents`, and the renderer repeats it. The CEL rules that did this
  overran the API server's cost limit at 12 kinds. The messages are different:
  a document with a second field is refused as
  `Too many: 3: must have at most 2 items`, and one whose field does not match
  its kind as `spec.documents[1]: kind "SysctlConfig" needs the field
  sysctlConfig alone, but the document sets kernelModuleConfig`. With
  `webhook.enabled=false` a mismatched document is stored, and each node it
  reaches fails to render with the same message.

### Go module

- `go.mod` retracts v0.1.0, v0.1.1, v0.2.0, v0.2.1 and v0.2.2. `go get` and
  `go mod tidy` do not move a module to one of them, a build that already
  requires one keeps working, and `go list -m -u` reports the retraction.

### Licensing

- `NOTICE` names `api/v1alpha1/zz_generated.talosconfigpatchdocument.go`,
  which holds the generated union and carries the MPL-2.0 header, with the
  other files derived from Talos.
- Every page of the docs site links the licences in its footer, and the site
  publishes `NOTICE`, `LICENSE` and `THIRD-PARTY-NOTICES` as text files.
- `THIRD-PARTY-NOTICES` lists the Sigstore public-good trusted root that the
  binary embeds, and the build refuses a dependency under the GPL, LGPL, AGPL,
  SSPL or BUSL.

### Docs

- The guides, concepts, getting started, the design document, the diagrams and
  the README name the verb-first commands.
- `create-the-cluster` is one page per endpoint pattern, the virtual IP first,
  then the load balancer and the node address. Each page shows the interactive,
  `init.yaml` and flag forms. Getting started is one page per step, and the
  architecture reference is one page per section.
- A new page, Certificate names, states what each serving certificate holds by
  default and what changing the names costs.
- The version banner is in the page's HTML from the start, so the page does not
  move when the version list loads.
- The CNI concept page and `ichor bootstrap cni --help` say that Cilium reaches
  the API through KubePrism only when kube-proxy is replaced.

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

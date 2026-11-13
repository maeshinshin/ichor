---
sidebar_order: 1
---

# Getting started

This guide takes you from a downloaded `ichor` CLI to a running one-node
cluster managed by Ichor. For an existing Talos cluster see
[Adopt and reclaim](adopt-and-reclaim.md) instead. To build the CLI and the
images yourself, see [Build from source](../development/build-from-source.md).

## Before you begin

- One machine (virtual or bare metal) booted from a Talos ISO into maintenance
  mode, with an address your workstation can reach on port 50000. This guide
  uses `192.168.1.50`. Talos needs at least 2 GiB of memory on a control plane.
  Ichor does not boot machines.
- Network access from the machine to `ghcr.io` (Ichor's images), and for the
  Talos installer image and Cilium's images. Air-gapped installs are out of
  scope.
- A Linux or macOS workstation (amd64 or arm64) that can reach the machine.

## 1. Install the CLI

Each [release](https://github.com/maeshinshin/ichor/releases) has one archive
per platform, `ichor_<version>_<os>_<arch>.tar.gz`, and a checksums file,
`ichor_<version>_checksums.txt`. Download the archive for your platform, check
it against the checksums and put `ichor` on your `PATH`.

Set the release and your platform once:

```sh
VERSION={{version}}   # the release, without the leading v
OS=linux        # linux or darwin
ARCH=amd64      # amd64 or arm64
BASE=https://github.com/maeshinshin/ichor/releases/download/v${VERSION}
```

Download the archive and the checksums file:

```sh
curl -fsSLO "${BASE}/ichor_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fsSLO "${BASE}/ichor_${VERSION}_checksums.txt"
```

Check the archive against the checksums:

```sh
grep " ichor_${VERSION}_${OS}_${ARCH}.tar.gz\$" "ichor_${VERSION}_checksums.txt" | sha256sum -c -
```

Unpack all of it, into a directory of its own, and put `ichor` on your `PATH`.
The archive also holds LICENSE, NOTICE, THIRD-PARTY-NOTICES, README.md and
CHANGELOG.md, so they end up beside the binary:

```sh
mkdir -p "ichor-${VERSION}"
tar -xzf "ichor_${VERSION}_${OS}_${ARCH}.tar.gz" -C "ichor-${VERSION}"
install -m 0755 "ichor-${VERSION}/ichor" ~/.local/bin/ichor
```

Check that it runs:

```sh
ichor version --client
```

On macOS use `shasum -a 256 -c -` in place of `sha256sum -c -`. `~/.local/bin`
must be on your `PATH`; `/usr/local/bin` (with `sudo`) works too.

The checksums file is signed keyless with [cosign](https://docs.sigstore.dev/cosign/system_config/installation/)
by the release workflow. To check the signature as well:

```sh
curl -fsSLO "${BASE}/ichor_${VERSION}_checksums.txt.sigstore.json"
cosign verify-blob "ichor_${VERSION}_checksums.txt" \
  --bundle "ichor_${VERSION}_checksums.txt.sigstore.json" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/maeshinshin/ichor/\.github/workflows/release\.yml@refs/tags/v.*$'
```

The CLI carries the install manifest of its release, which names the
controller and ichor-ui images of that release by digest. `ichor init` applies
it without a network fetch, so the installed Ichor is always the version of the
CLI.

## 2. Create the cluster

`ichor init` makes the node the first control plane of a new cluster. It
prompts before the irreversible step; `--yes` skips that.

```sh
ichor init \
  --cluster demo \
  --node-endpoint 192.168.1.50:50000 \
  --endpoint-type node --endpoint-address 192.168.1.50 \
  --install-disk /dev/sda \
  --preset single
```

- `--cluster`, `--node-endpoint`, `--endpoint-address` and `--install-disk`
  are required. `--node-endpoint` is the node's Talos API (`host:port`); the
  install disk can also be `wwid:ID`, `serial:SERIAL` or a
  `/dev/disk/by-id/` path.
- `--endpoint-type` is `vip` by default, which expects a free address on the
  nodes' network as `--endpoint-address`. `node` uses the node's own address,
  which is simplest for one control plane.
- `--preset` is `standard` by default (three control planes, workers added
  later). `single` is one schedulable node; `compact-ha` is three schedulable
  control planes.
- `--registry-mirror HOST=URL` writes an anonymous mirror into the machine
  config (`spec.registries`), for nodes that cannot reach a registry directly.
  It is repeatable and not needed here.
- On virtual networks that pass geneve but not vxlan, add
  `--cni-tunnel geneve` for Cilium.
- The pod and service subnets (`--pod-subnets` and `--service-subnets`, by
  default `10.244.0.0/16` and `10.96.0.0/12`) must not overlap the network the
  nodes sit on, and they cannot change once the cluster exists.
  `ichor init --help` lists the rest (disk selectors, discovery addresses).

The same settings can come from a file instead of flags. `ichor init -f
init.yaml` reads an `InitConfig`; `ichor init --print-config-template`
prints a commented example. The command above is this file:

```yaml
apiVersion: ichor.maesh.dev/v1alpha1
kind: InitConfig
cluster:
  name: demo
  preset: single
  endpoint:
    type: node
    address: 192.168.1.50
firstNode:
  endpoint: 192.168.1.50:50000
  installDisk: /dev/sda
```

A flag on the command line overrides the file. Paths in the file (the TLS
certificate and key below) are relative to the file. The file never holds the
key itself, only its path.

Init first writes the secrets bundle to `~/.ichor/clusters/demo/secrets.yaml`
(mode 0600), encrypted. It is the cluster's root of trust. On a terminal, init
generates a passphrase, prints it once and waits for Enter: save it in a
password manager, together with a copy of the file kept somewhere off this
machine. Without the passphrase the file cannot be opened (it is an age file;
`age -d` opens it too). To choose the passphrase instead, or to run init
without a terminal, give it a file (`--bundle-passphrase-file ./pass`, which
may be `/dev/fd/3`) or set `ICHOR_BUNDLE_PASSPHRASE` (at least 12 characters);
with neither and no terminal, init refuses before it does anything.
`--bundle-plaintext` stores the file unencrypted, with a warning; do it only
for a throwaway cluster. [Users and login](users-and-login.md) and
[Hardening](hardening.md) say how the file and the passphrase are used later.
It then applies the configuration to the node (which installs Talos and
reboots), bootstraps etcd, saves the kubeconfig next to the bundle, stores the
bundle in the cluster, installs Ichor and waits for Ichor to take over Cilium.
Expect several minutes.

`--merge-kubeconfig` also merges the cluster-admin kubeconfig into your own
(the first existing file of `$KUBECONFIG`, else `~/.kube/config`) as the
context `admin@demo`, and makes it current, so `kubectl` and `ichor` work at
once. Entries already there stay; a name that is taken gets a suffix
(`admin@demo-1`) and init prints the final name.
Without the flag, init leaves your kubeconfig alone and prints the `export
KUBECONFIG=` line to use. The examples below use that line; skip it if you
merged.

### Turn on sign-in (optional, recommended)

Without `--ui-url`, ichor-ui has no sign-in: `ichor init` starts it with
`--allow-unauthenticated` and prints a warning (see
[Users and login](users-and-login.md)). To use Ichor's own users instead, add
the https URL at which your browser will reach ichor-ui, and a certificate for
it:

```sh
  --ui-url https://192.168.1.60 --admin-user admin \
  --ui-tls-cert ui.crt --ui-tls-key ui.key
```

In the file this is a `ui` block, next to `cluster` and `firstNode`:

```yaml
ui:
  url: https://192.168.1.60
  adminUser: admin
  tls:
    certFile: ./ui.crt
    keyFile: ./ui.key
```

The URL is the issuer of every login. Making it reachable (a LoadBalancer, an
Ingress or a Gateway on ichor-ui's port 8443) is your job; init installs none
of them. At the end init prints the generated password of the first admin to
stderr, once. Save it. Because the kube-apiserver then needs new login
settings, apply them:

```sh
export KUBECONFIG=~/.ichor/clusters/demo/kubeconfig
ichor rollout config --cluster demo --follow
```

With one control plane the Kubernetes API is down for about a minute while it
restarts.

## 3. Approve machines

`ichor init` registers the first node itself. Other nodes are found in
maintenance mode by scanning addresses you give, and wait as pending machines
until you approve them. Point the scan at them (`init` takes
`--discover-address` and `--discover-cidr` too):

```sh
ichor discovery add 192.168.1.51 --cluster demo
ichor get nodes --cluster demo
ichor approve node NAME --role worker --install-disk /dev/sda
```

`NAME` is the pending machine from the list. A machine is named after its
hardware UUID, so the list shows the first eight characters of it, as long as
those are unique; pass that short name as it is, or the full UUID. If a short
name fits more than one machine, the command refuses and lists the candidates.
The other commands that take a machine name accept the short name too. The
command asks for confirmation unless `--yes`, then prints one line (`-o yaml` prints the
machine as before):

```text
✓ approved fa70962a as worker in demo
```

If the install disk, a disk role or the NIC you named was not among those
discovery recorded on the node, a `!` line follows: advice, not a refusal.
`approve` also takes a hostname, labels, a static
address and extra disk roles; see `ichor approve node --help`. A machine
with a weak identity (no UUID, serial or permanent MAC) is refused. After
approval the controller renders and applies the configuration; the node
installs Talos, reboots and joins. With the `single` preset there is nothing
to approve, so skip this step.

If you turned sign-in on, the first of these commands prints this line on
stderr, and so do the commands in step 4:

```text
ichor: ichor-ui requires OIDC and there is no usable ichor-ui login; using your own kubeconfig and RBAC in this process instead (to use ichor-ui, run ichor auth login --issuer https://… --audience ichor-ui)
```

It means the CLI could not reach ichor-ui through a login, so it runs the
command itself with your kubeconfig and your Kubernetes permissions. The
command still works, and the steps below run the same way. To go through
ichor-ui and its users instead, sign in once. The `--issuer` is the
`--ui-url` you chose:

```sh
ichor auth login --issuer https://192.168.1.60 --audience ichor-ui
```

This opens your browser at the login page, so the URL must be reachable from
this machine. If ichor-ui has no certificate of its own, it uses one from the
Ichor serving CA, which your machine does not trust. Export that CA and pass it
to the login:

```sh
kubectl -n ichor-system get configmap ichor-ui-ca -o jsonpath='{.data.ca\.crt}' > ichor-ca.crt
ichor auth login --issuer https://192.168.1.60 --audience ichor-ui --ca-file ichor-ca.crt
```

The warning stops once the login is cached. [Users and login](users-and-login.md)
explains the CA and `--ca-file`, which the other `ichor auth` commands and the user
commands take too.

## 4. Check the cluster

The commands below use [`kubectl`](https://kubernetes.io/docs/tasks/tools/) once,
to list the nodes.

```sh
export KUBECONFIG=~/.ichor/clusters/demo/kubeconfig
ichor get cluster demo
ichor describe cluster demo
ichor get nodes --cluster demo
ichor version
kubectl get nodes
```

```console
$ ichor get cluster demo
demo · ichor-system
  Talos        v1.14.5
  Kubernetes   v1.36.6
  Cilium       1.20.4
  Endpoint     192.168.1.50 (node)
  Health       ✓ ready

$ ichor get nodes --cluster demo
NAME      ROLE           APPROVAL  READY  ENDPOINT            NODE     AGE
fa70962a  control-plane  approved  ✓      192.168.1.50:50000  demo-cp  5m

$ ichor version
ichor        v{{version}}
installed    v{{version}}
new installs Talos v1.14.5 · Kubernetes v1.36.6 · Cilium 1.20.4 (contract v1.14)
```

`ichor get cluster NAME` prints versions, endpoint and health in a few lines;
`ichor describe cluster NAME` adds the network, CNI, control plane, certificate expiry and
every condition (with ✓, `!`, ✗ or … in front). Every machine should be Ready
(✓). `ichor version` shows the CLI and the installed Ichor version; they should
both say the release you installed.
With sign-in on, these commands also print the sign-in line from step 3 on
stderr. The output below leaves it out.

The CLI prints for people. In a script, add `-o yaml` or `-o json` to any
command for the full response. A table cell is never cut off, but a long
machine name is shown in short form (see step 3); `-o yaml` prints it whole.
Colour appears only on a terminal (set `NO_COLOR` to turn it off).

The same goes for the operations. `ichor get operations` shows each one with a
glyph for its phase and what it is doing; `ichor describe operation NAME` adds the
state of every machine. `--follow` (on `upgrade`, `rollout config` and the node
actions) and `ichor describe operation NAME --watch` keep one live line with the phase and
message, and end it with the time the operation took. In a pipe or CI log they
print a line per change instead:

```console
$ ichor get operations
NAME                 KIND    PHASE         TARGET  MESSAGE                       AGE
upgrade-talos-x7k2p  talos   … InProgress  demo    upgrading talos-cp2 (2 of 3)  12m
config-r2d9m         config  ✓ Completed   demo    -                             3d
$ ichor upgrade talos --follow --yes
Upgrade Talos v1.14.3 → v1.14.5
operation upgrade-talos-x7k2p · cluster demo
… talos — InProgress · 0/3 machines · upgrading talos-cp1
… talos — InProgress · 1/3 machines · upgrading talos-cp2
✓ talos — Completed (11m52s)
```

A failed step ends the line with ✗ Failed, prints the reason and exits with an
error that names `ichor resume operation NAME`. `ichor upgrade talos --list`
(and `kubernetes`) prints the versions on offer as a table; `-o yaml`
and `-o json` of these commands print what they always printed.

## 5. Open the UI and sign in

If you turned sign-in on, open the `--ui-url` in a browser. The certificate is
yours, so a self-signed one makes the browser warn. Sign in as `admin` with the
password init printed. The first login forces you to choose a new password
(12 to 128 bytes); after that, users, roles and kubeconfigs are as described
in [Users and login](users-and-login.md).

If you did not use `--ui-url`, there is no login: `ichor init` started
ichor-ui with `--allow-unauthenticated`, so anyone who can reach it can make
changes. Open it through a port-forward:

```sh
ichor ui
```

```text
✓ ichor-ui  https://localhost:8443/
  port-forward to ichor-system/ichor-ichor-ui-7d9c5b6f4-x2k8q · Ctrl-C to stop
```

It opens that address in your default browser (`--open-browser=false`
only prints it) and keeps the tunnel up until you press Ctrl-C. The
certificate comes from the Ichor serving CA, which a browser does not trust, so
it warns.

## Where to go next

- [Settings](settings.md), [Hardening](hardening.md) and
  [Users and login](users-and-login.md) for what to configure first.
- [Install Ichor with Helm](install-with-helm.md) and
  [Adopt and reclaim](adopt-and-reclaim.md) for existing clusters.
- [Architecture](../design/architecture.md) for how it works.

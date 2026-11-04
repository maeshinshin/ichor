# Getting started

This guide takes you from a checkout to a running one-node cluster managed by
Ichor, using a local build. There is no published release yet, so the images
are built from your tree and pushed to a registry your nodes can reach. For an
existing Talos cluster see [Adopt and reclaim](adopt-and-reclaim.md) instead.

## What you need

- A checkout of this repository and [Nix](https://nixos.org/) with flakes.
  Everything below runs inside `nix develop` (`nix develop -c <command>` runs
  one command in it). It provides Go, Node, `crane`, `kustomize` and the other
  tools; install nothing else.
- One machine (virtual or bare metal) booted from a Talos ISO into maintenance
  mode, with an address your workstation can reach on port 50000. This guide
  uses `192.168.1.50`. Talos needs at least 2 GiB of memory on a control plane.
  Ichor does not boot machines.
- A container registry that your workstation can push to and that every node
  can pull from. A plain-HTTP registry on your LAN is enough for a first try
  (`registry:2` works; [`test/harness/ui-demo/registry.yaml`](../../test/harness/ui-demo/registry.yaml)
  is a Deployment of one). This guide uses `192.168.1.10:5000`.
- The nodes need internet access for the Talos installer image and for
  Cilium's images. Air-gapped installs are out of scope
  ([ADR 0090](../adr/0090-air-gapped-installs-are-out-of-scope.md)).

## 1. Build the images and the CLI

The install manifest that `ichor init` applies names its images
`ghcr.io/maeshinshin/ichor-controller:VERSION` and
`ghcr.io/maeshinshin/ichor-ui:VERSION`. You build those images yourself, push
them under your registry, and tell the nodes to fetch `ghcr.io` from it (step
2). Pick a semantic version; `dev` is refused.

```
nix develop
make demo-images-build VERSION=v0.1.0
make demo-images-push VERSION=v0.1.0 REGISTRY=192.168.1.10:5000
```

`demo-images-build` builds the UI and the install manifest, then compiles three
linux/amd64 binaries into `bin/img/`. `demo-images-push` layers each on a
pinned distroless base and pushes `ichor-controller`, `ichor-ui` and
`ichor-installer` to `192.168.1.10:5000/maeshinshin/` with `crane`, printing
each digest. It talks plain HTTP to the registry by default; set
`CRANE_INSECURE=` (empty) for one with TLS. `make demo-images VERSION=...
REGISTRY=...` does both steps in one go.

The same build leaves the CLI at `bin/img/ichor/ichor`. It carries the
install manifest for that version inside it (the `installembed` build), which
is what lets `ichor init` install Ichor without a network fetch. Use it
instead of one from `go run ./cmd/ichor`, whose manifest is a placeholder:

```
cp bin/img/ichor/ichor bin/ichor
bin/ichor version --client
```

`make build-ichor-embed` also embeds a manifest, but it takes the image names
from `IMG` and `IMG_UI`, which default to `controller:latest` and
`ichor-ui:latest`; pass them as in the `demo-images-build` recipe if you use it.
`make help` lists the other targets.

To build the release artifacts for a tag instead, run
`make release VERSION=v0.1.0`. It writes `bin/release/ichor` (the CLI with its
install manifest embedded), `bin/release/install.yaml`,
`bin/release/network-policies.yaml` and the chart tgz to `bin/release/`, with
images named `ghcr.io/maeshinshin/<name>:v0.1.0`. `RELEASE_REGISTRY=<registry>/<namespace>`
changes that image prefix (the default is `ghcr.io/maeshinshin`). It pushes
nothing, so the `demo-images` steps above are still how you build and push
the images.

## 2. Create the cluster

`ichor init` makes the node the first control plane of a new cluster. It
prompts before the irreversible step; `--yes` skips that.

```
bin/ichor init \
  --cluster demo \
  --node-endpoint 192.168.1.50:50000 \
  --endpoint-type node --endpoint-address 192.168.1.50 \
  --install-disk /dev/sda \
  --preset single \
  --registry-mirror ghcr.io=http://192.168.1.10:5000
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
  config (`spec.registries`). Here it makes the node fetch
  `ghcr.io/maeshinshin/...` from your registry, so the images you pushed in
  step 1 are the ones that run. It is repeatable.
- On virtual networks that pass geneve but not vxlan, add
  `--cni-tunnel geneve` for Cilium.
- `ichor init --help` lists the rest (pod and service subnets, disk selectors,
  discovery addresses).

Init first writes the secrets bundle to `~/.ichor/clusters/demo/secrets.yaml`
(mode 0600). It is the cluster's root of trust: back it up before anything
else. It then applies the configuration to the node (which installs Talos and
reboots), bootstraps etcd, saves the kubeconfig next to the bundle, stores the
bundle in the cluster, installs Ichor and waits for Ichor to take over Cilium.
Expect several minutes.

### Turn on sign-in (optional, recommended)

Without `--ui-url`, ichor-ui has no sign-in: `ichor init` starts it with
`--allow-unauthenticated` and prints a warning (see
[Users and login](users-and-login.md)). To use Ichor's own users instead, add
the https URL at which your browser will reach ichor-ui, and a certificate for
it:

```
  --ui-url https://192.168.1.60 --admin-user admin \
  --ui-tls-cert ui.crt --ui-tls-key ui.key
```

The URL is the issuer of every login. Making it reachable (a LoadBalancer, an
Ingress or a Gateway on ichor-ui's port 8443) is your job; init installs none
of them. At the end init prints the generated password of the first admin to
stderr, once. Save it. Because the kube-apiserver then needs new login
settings, apply them:

```
export KUBECONFIG=~/.ichor/clusters/demo/kubeconfig
bin/ichor config apply --cluster demo --follow
```

With one control plane the Kubernetes API is down for about a minute while it
restarts.

## 3. Approve machines

`ichor init` registers the first node itself. Other nodes are found in
maintenance mode by scanning addresses you give, and wait as pending machines
until you approve them. Point the scan at them (`init` takes
`--discover-address` and `--discover-cidr` too):

```
bin/ichor cluster discovery add 192.168.1.51 --cluster demo
bin/ichor machine list --cluster demo
bin/ichor machine approve NAME --role worker --install-disk /dev/sda
```

`NAME` is the pending machine from the list. The command asks for
confirmation unless `--yes`. `approve` also takes a hostname, labels, a static
address and extra disk roles; see `ichor machine approve --help`. A machine
with a weak identity (no UUID, serial or permanent MAC) is refused. After
approval the controller renders and applies the configuration; the node
installs Talos, reboots and joins. With the `single` preset there is nothing
to approve, so skip this step.

## 4. Check the cluster

```
export KUBECONFIG=~/.ichor/clusters/demo/kubeconfig
bin/ichor cluster status demo
bin/ichor cluster get demo
bin/ichor machine list --cluster demo
bin/ichor version
kubectl get nodes
```

`cluster status` prints versions, endpoint and CNI in a few lines;
`cluster get` adds conditions, rollout and the Cilium hand-over state. Every
machine should be Ready. `ichor version` shows the CLI and the installed
Ichor version; they should both say `v0.1.0`.

## 5. Open the UI and sign in

If you turned sign-in on, open the `--ui-url` in a browser. The certificate is
yours, so a self-signed one makes the browser warn. Sign in as `admin` with the
password init printed. The first login forces you to choose a new password
(12 to 128 bytes); after that, users, roles and kubeconfigs are as described
in [Users and login](users-and-login.md).

If you did not use `--ui-url`, there is no login: `ichor init` started
ichor-ui with `--allow-unauthenticated`, so anyone who can reach it can make
changes. Open it through a port-forward:

```
bin/ichor ui
```

It opens `https://localhost:PORT` in your default browser (`--open-browser=false`
only prints the address) and keeps the tunnel up until you press Ctrl-C. The
certificate comes from the Ichor serving CA, which a browser does not trust, so
it warns.

## Where to go next

- [Settings](settings.md), [Hardening](hardening.md) and
  [Users and login](users-and-login.md) for what to configure first.
- [Install Ichor with Helm](install-with-helm.md) and
  [Adopt and reclaim](adopt-and-reclaim.md) for existing clusters.
- [Architecture](../design/architecture.md) for how it works.

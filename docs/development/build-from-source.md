---
title: Build from source
---

# Build from source

Use this page to run Ichor from a checkout instead of a release: you build the
`ichor` CLI and the images yourself and push the images to your own registry.
To install a released CLI, see [Getting started](../guides/getting-started.md).
The cluster steps are the same as there, with the differences below.

## The tools

Every command in these docs is written as if its tools are on your `PATH`.
There are two ways to get them.

**Enter the dev shell (recommended).** With a checkout of this repository and
[Nix](https://nixos.org/) with flakes, run `nix develop` in the checkout. It
puts every tool on `PATH` at the versions `flake.nix` and `flake.lock` pin, and
you install nothing else. To run one command in it without entering it, use
`nix develop -c <command>`.

**Or install the tools yourself.** The commands in the guides use these. The
versions are the ones the dev shell resolves today; the exception is Go, which
must be 1.27.4 or newer (`go.mod`).

| Tool | Version | Used for |
|---|---|---|
| Go | 1.27.4 or newer | building the `ichor` CLI and the images (`make demo-images-build`) |
| `ko` | v0.19.1 | building the images, with no container runtime (`make demo-images-build`) |
| Node.js | 24 | building the UI, part of the same build |
| GNU make | any recent | the `make` targets |
| `crane` | v0.22.1 | pushing the images to your registry (`make demo-images-push`) |
| `kubectl` | v1.37.1 | reading the cluster and applying the manifests the guides show |
| `talosctl` | v1.14.3 | the few places a guide talks to a node directly, such as [hardening](../guides/hardening.md) |
| `helm` | v4.3.1 | [installing Ichor with Helm](../guides/install-with-helm.md) |
| `cosign` | v3.1.3 | verifying a release's signatures ([install with Helm](../guides/install-with-helm.md)) |
| `openssl` | 3.x | the certificate check in [hardening](../guides/hardening.md) |

The Makefile fetches its own helpers (`controller-gen`, `kustomize`,
`setup-envtest`, `golangci-lint`) into `./bin` with Go at the versions it pins,
so the first `make` needs network access. Developers of Ichor itself need more,
and [CONTRIBUTING.md](../../CONTRIBUTING.md) lists it.

## What you need

- A checkout of this repository.
- One machine (virtual or bare metal) booted from a Talos ISO into maintenance
  mode, with an address your workstation can reach on port 50000. This guide
  uses `192.168.1.50`. Talos needs at least 2 GiB of memory on a control plane.
  Ichor does not boot machines.
- A container registry that your workstation can push to and that every node
  can pull from. A plain-HTTP registry on your LAN is enough for a first try
  (`registry:2` works; [`test/harness/ui-demo/registry.yaml`](../../test/harness/ui-demo/registry.yaml)
  is a Deployment of one). This guide uses `192.168.1.10:5000`.
- The nodes need internet access for the Talos installer image and for
  Cilium's images. Air-gapped installs are out of scope.

## Build the images and the CLI

The install manifest that `ichor init` applies names its images
`ghcr.io/maeshinshin/ichor-controller:VERSION` and
`ghcr.io/maeshinshin/ichor-ui:VERSION`. You build those images yourself, push
them under your registry, and tell the nodes to fetch `ghcr.io` from it (next section). Pick a semantic version; `dev` is refused.

```sh
make demo-images-build VERSION=v0.1.0
make demo-images-push VERSION=v0.1.0 REGISTRY=192.168.1.10:5000
```

`demo-images-build` builds the UI and the install manifest, then has `ko`
compile three images (linux/amd64 and linux/arm64, on a pinned distroless base,
configured in `.ko.yaml`) into `bin/oci/`. `demo-images-push` pushes
`ichor-controller`, `ichor-ui` and `ichor-installer` to
`192.168.1.10:5000/maeshinshin/` with `crane`, printing each index digest. It talks plain HTTP to the registry by default; set
`CRANE_INSECURE=` (empty) for one with TLS. These two steps leave the install
manifest naming the images by tag. `make demo-images VERSION=... REGISTRY=...`
does both in one go the way a release does: it pushes `ichor-controller` and
`ichor-ui` first, writes their digests to `bin/digests.txt`, renders the install
manifest with `ichor-controller:VERSION@sha256:...` and
`ichor-ui:VERSION@sha256:...`, and only then builds and pushes the installer
image that embeds it.

The installer build also leaves the CLI at `bin/img/ichor/ichor`. It carries the
install manifest for that version inside it (the `installembed` build), which
is what lets `ichor init` install Ichor without a network fetch. Use it
instead of one from `go run ./cmd/ichor`, whose manifest is a placeholder:

```sh
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

Images you build yourself carry no signature. A cluster installed from them can
be upgraded later with `ichor upgrade ichor VERSION --skip-signature-check`,
which Ichor records. `ichor init` takes the same two flags, `--installer-digest sha256:...`
(the digest of the installer image you pushed, from `DIGEST_FILE`) and
`--skip-signature-check`; give them when the cluster's controller cannot reach the
registry the installer image comes from, or the Cilium hand-over finds no digest to
run its Job with (`CNIHandover` `InstallerImageUnresolved`). See
[The installer image on an upgrade](../guides/install-with-helm.md#the-installer-image-on-an-upgrade).

## Create the cluster with your images

Run the steps of [Getting started](../guides/getting-started.md#2-create-the-cluster)
with `bin/ichor` in place of `ichor`, and add `--registry-mirror` to `ichor init`
so the node fetches `ghcr.io/maeshinshin/...` from your registry, where the
images you pushed above are:

```sh
bin/ichor init \
  --cluster demo \
  --node-endpoint 192.168.1.50:50000 \
  --endpoint-type node --endpoint-address 192.168.1.50 \
  --install-disk /dev/sda \
  --preset single \
  --registry-mirror ghcr.io=http://192.168.1.10:5000
```

In an `InitConfig` file the same setting is `cluster.registryMirrors`:
`ghcr.io: http://192.168.1.10:5000`. `--registry-mirror HOST=URL` writes an
anonymous mirror into the machine config (`spec.registries`) and is repeatable.

## Install your own chart

To install Ichor on an existing cluster with the chart from your checkout
(see [Install with Helm](../guides/install-with-helm.md)), point the image
repositories at the images you pushed:

```sh
helm install ichor ./dist/chart --namespace ichor-system --create-namespace \
  --set manager.image.repository=REPO --set ichorUI.image.repository=REPO
```

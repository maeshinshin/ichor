---
title: Build from source
---

# Build from source

Run Ichor from a checkout: build the `ichor` CLI and the images yourself and
push the images to your own registry. For a released CLI see [Getting
started](../guides/getting-started.md); the cluster steps are the same, with the
differences below.

![The build path. make demo-images-build has ko compile three images into bin/oci, make demo-images-push pushes them to your registry, and the same build leaves bin/img/ichor/ichor, the CLI with the install manifest embedded. bin/ichor init with --registry-mirror makes the node pull ghcr.io images from your registry. Your images carry no signature, so init and ichor upgrade ichor take --skip-signature-check. A chart from the checkout is the other way in.](../images/build-from-source-flow.light.drawio.svg)

*Build, push, then point the node at your registry.*

## The tools

Every command in these docs assumes its tools are on your `PATH`. Either run
`nix develop` in a checkout ([Nix](https://nixos.org/) with flakes), which puts
every tool on `PATH` at the versions `flake.nix` and `flake.lock` pin (one
command: `nix develop -c <command>`), or install them yourself:

| Tool | Used for |
|---|---|
| Go, 1.27.4 or newer (`go.mod`) | building the CLI and the images |
| `ko` | the images, with no container runtime |
| Node.js 24 | the UI, part of the same build |
| GNU make | the `make` targets |
| `crane` | pushing the images to your registry |
| `kubectl` | reading the cluster, applying the guides' manifests |
| `talosctl` | the few places a guide talks to a node, such as [hardening](../guides/hardening.md) |
| `helm` | [installing Ichor with Helm](../guides/install-with-helm.md) |
| `cosign` | verifying a release's signatures |
| `openssl` 3.x | the certificate check in [hardening](../guides/hardening.md) |

Take the other versions from `flake.lock`. The Makefile fetches its own helpers
(`controller-gen`, `kustomize`, `setup-envtest`, `golangci-lint`) into `./bin`,
so the first `make` needs network access. Developers of Ichor need more:
[CONTRIBUTING.md](../../CONTRIBUTING.md).

## What you need

- One machine booted from a Talos ISO into maintenance mode, reachable on port
  50000 (here `192.168.1.50`), with at least 2 GiB of memory for a control
  plane. Ichor does not boot machines.
- A container registry your workstation can push to and every node can pull
  from (here `192.168.1.10:5000`). A plain-HTTP registry on your LAN is enough;
  [`test/harness/ui-demo/registry.yaml`](../../test/harness/ui-demo/registry.yaml)
  is a Deployment of one.
- Internet access from the nodes, for the Talos installer image and Cilium's
  images. Air-gapped installs are out of scope.

## Build the images and the CLI

The install manifest that `ichor init` applies names
`ghcr.io/maeshinshin/ichor-controller:VERSION` and
`ghcr.io/maeshinshin/ichor-ui:VERSION`. Build them, push them under your
registry and tell the nodes to fetch `ghcr.io` from it. Pick a semantic version;
`dev` is refused.

```sh
make demo-images-build VERSION=vX.Y.Z
make demo-images-push VERSION=vX.Y.Z REGISTRY=192.168.1.10:5000
```

| Target | Does |
|---|---|
| `demo-images-build` | builds the UI and the install manifest, then `ko` compiles three images (linux/amd64 and arm64, pinned distroless base, `.ko.yaml`) into `bin/oci/` |
| `demo-images-push` | pushes `ichor-controller`, `ichor-ui` and `ichor-installer` to `192.168.1.10:5000/maeshinshin/` with `crane`, printing each index digest. Plain HTTP by default; `CRANE_INSECURE=` (empty) for TLS. The manifest still names the images by tag |
| `demo-images` | both, the way a release does: pushes controller and UI first, writes their digests to `bin/digests.txt`, renders the manifest with `@sha256:` digests, then builds and pushes the installer image that embeds it |
| `release` | `make release VERSION=vX.Y.Z` writes `bin/release/ichor`, `install.yaml`, `network-policies.yaml` and the chart tgz, with images named `ghcr.io/maeshinshin/<name>:vX.Y.Z` (`RELEASE_REGISTRY=<registry>/<namespace>` changes the prefix). Pushes nothing |

The installer build also leaves the CLI at `bin/img/ichor/ichor`, with the
install manifest for that version embedded, so `ichor init` needs no network
fetch. Use it, not `go run ./cmd/ichor`, whose manifest is a placeholder:

```sh
cp bin/img/ichor/ichor bin/ichor
bin/ichor version --client
```

`make build-ichor-embed` also embeds a manifest but takes the image names from
`IMG` and `IMG_UI`; their defaults name no registry, so the build refuses them.
`make help` lists the other targets.

**Releasing.** Before pushing a tag `vX.Y.Z`, write its `## vX.Y.Z (date)`
section in `CHANGELOG.md` (the release notes come from it) and run
`make release-prep VERSION=vX.Y.Z`, which sets the version in `README.md` and
stops if the section is missing. Commit, then tag. A tag cut from a
`release-X.Y` branch leaves that work on the branch: once the release
publishes, the Release workflow opens a pull request against `main` adding the
changelog section and the README version (newest release only); merge it. A tag
cut from `main` needs nothing. That pull request uses the
`RELEASE_SYNC_PR_TOKEN` secret when set; otherwise the default token, which
needs "Allow GitHub Actions to create and approve pull requests" and starts no
checks.

## Unsigned images

Images you build carry no signature. Upgrade a cluster installed from them with
`ichor upgrade ichor VERSION --skip-signature-check`, which Ichor records in
`spec.installerSignature` until an upgrade without the flag (or a spec edit)
removes it. `ichor init` takes the same flag plus `--installer-digest sha256:...`
(the digest of the pushed installer image, from `DIGEST_FILE`); give them when
the cluster's controller cannot reach the registry the installer image comes
from, or the Cilium hand-over finds no digest to run its Job with (`CNIHandover`
`InstallerImageUnresolved`). The `InstallerImageUnverified` condition on the
`IchorInstallation` shows an installer image ran unchecked. See [The installer
image on an
upgrade](../guides/install-with-helm.md#the-installer-image-on-an-upgrade).

## Create the cluster with your images

Follow [Getting started](../guides/getting-started/create-the-cluster.md) with
`bin/ichor` for `ichor`, and add `--registry-mirror` so the node fetches
`ghcr.io/maeshinshin/...` from your registry:

```sh
bin/ichor init \
  --cluster demo \
  --node-endpoint 192.168.1.50:50000 \
  --endpoint-type node --endpoint-address 192.168.1.50 \
  --install-disk /dev/sda \
  --preset single \
  --registry-mirror ghcr.io=http://192.168.1.10:5000
```

`--registry-mirror HOST=URL` writes an anonymous mirror into the machine config
(`spec.registries`) and is repeatable. In an `InitConfig` file it is
`cluster.registryMirrors`.

## Install your own chart

On an existing cluster, with the chart from your checkout ([Install with
Helm](../guides/install-with-helm.md)), point the image repositories at your
images:

```sh
helm install ichor ./dist/chart --namespace ichor-system --create-namespace \
  --set manager.image.repository=REPO --set ichorUI.image.repository=REPO \
  --set ichorUI.users.enabled=true --set ichorUI.users.externalURL=https://localhost:8443
```

The chart refuses an ichor-ui with no sign-in, so it needs `ichorUI.users` (as
here) or `ichorUI.oidc`.

---
sidebar_order: 1
---

# 1. Install the CLI

Each [release](https://github.com/maeshinshin/ichor/releases) has an archive per
platform, `ichor_<version>_<os>_<arch>.tar.gz`, and `ichor_<version>_checksums.txt`.

![Four steps: download the archive and the checksums file, check the archive against the checksums, unpack and install ichor on the PATH, then run ichor version with the client flag. An optional cosign check of the signed checksums sits between the check and the install.](../../images/install-the-cli-steps.light.drawio.svg)

*Four steps. The cosign check is optional.*

Set the release and your platform once:

```sh
VERSION={{version}}   # the release, without the leading v
OS=linux        # linux or darwin
ARCH=amd64      # amd64 or arm64
BASE=https://github.com/maeshinshin/ichor/releases/download/v${VERSION}
```

```sh
curl -fsSLO "${BASE}/ichor_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fsSLO "${BASE}/ichor_${VERSION}_checksums.txt"
grep " ichor_${VERSION}_${OS}_${ARCH}.tar.gz\$" "ichor_${VERSION}_checksums.txt" | sha256sum -c -
```

> [!TIP]
> On macOS use `shasum -a 256 -c -` for `sha256sum -c -`.

Unpack into a directory of its own (it also holds LICENSE, NOTICE,
THIRD-PARTY-NOTICES, README.md and CHANGELOG.md) and put `ichor` on your `PATH`
(`~/.local/bin` must be on it; `/usr/local/bin` with `sudo` works too):

```sh
mkdir -p "ichor-${VERSION}" ~/.local/bin
tar -xzf "ichor_${VERSION}_${OS}_${ARCH}.tar.gz" -C "ichor-${VERSION}"
install -m 0755 "ichor-${VERSION}/ichor" ~/.local/bin/ichor
ichor version --client
```

The checksums file is signed keyless with
[cosign](https://docs.sigstore.dev/cosign/system_config/installation/). Checking
the signature is optional. It needs cosign 2.6.1 or later to read the
`.sigstore.json` bundle; 2.4.1 fails with `bundle does not contain cert for
verification, please provide public key`.

```sh
curl -fsSLO "${BASE}/ichor_${VERSION}_checksums.txt.sigstore.json"
cosign verify-blob "ichor_${VERSION}_checksums.txt" \
  --bundle "ichor_${VERSION}_checksums.txt.sigstore.json" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/maeshinshin/ichor/\.github/workflows/release\.yml@refs/tags/v.*$'
```

The CLI carries its release's install manifest, which names the controller and
ichor-ui images by digest, so `ichor init` installs the version of the CLI with
no network fetch.

## Later: upgrade the CLI

`ichor upgrade cli [VERSION]` verifies the release's signed checksums and the
archive, then replaces the binary (newest release without `VERSION`; `--check`
changes nothing). It reads no cluster, and a development build is never replaced.

---
sidebar_order: 1
---

# 1. Install the CLI

Each [release](https://github.com/maeshinshin/ichor/releases) has one archive
per platform, `ichor_<version>_<os>_<arch>.tar.gz`, and a checksums file,
`ichor_<version>_checksums.txt`.

![Four steps: download the archive and the checksums file, check the archive against the checksums, unpack and install ichor on the PATH, then run ichor version with the client flag. An optional cosign check of the signed checksums sits between the check and the install.](../../images/install-the-cli-steps.light.drawio.svg)

*Four steps. The cosign check is optional.*

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

> [!TIP]
> On macOS use `shasum -a 256 -c -` in place of `sha256sum -c -`.

Unpack all of it, into a directory of its own, and put `ichor` on your `PATH`.
The archive also holds LICENSE, NOTICE, THIRD-PARTY-NOTICES, README.md and
CHANGELOG.md, so they end up beside the binary:

```sh
mkdir -p "ichor-${VERSION}" ~/.local/bin
tar -xzf "ichor_${VERSION}_${OS}_${ARCH}.tar.gz" -C "ichor-${VERSION}"
install -m 0755 "ichor-${VERSION}/ichor" ~/.local/bin/ichor
```

> [!NOTE]
> `~/.local/bin` must be on your `PATH`. `/usr/local/bin` (with `sudo`) works too.

Check that it runs:

```sh
ichor version --client
```

> [!NOTE]
> The checksums file is signed keyless with [cosign](https://docs.sigstore.dev/cosign/system_config/installation/)
> by the release workflow. Checking the signature is optional:

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

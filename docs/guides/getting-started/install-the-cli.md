---
sidebar_order: 1
---

# 1. Install the CLI

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

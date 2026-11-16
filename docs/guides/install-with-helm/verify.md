---
sidebar_order: 4
---

# Verify a release

Every release image (`ichor-controller`, `ichor-ui`, `ichor-installer`) and the
chart is signed keylessly with cosign by the release workflow, and carries an
SPDX SBOM and a build provenance attestation. Verify by digest, with
the identity of this repository's workflow:

```sh
cosign verify ghcr.io/maeshinshin/ichor-controller:vX.Y.Z \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/maeshinshin/ichor/\.github/workflows/release\.yml@refs/tags/v.*$'
```

The same command verifies `ichor-ui`, `ichor-installer` and the chart
(`ghcr.io/maeshinshin/charts/ichor:X.Y.Z`). The provenance attestation is
checked with `gh attestation verify oci://ghcr.io/maeshinshin/ichor-controller:vX.Y.Z --repo maeshinshin/ichor`.
An Ichor upgrade made by the installer Job (`ichor upgrade ichor`) checks the
installer image's signature itself; see
[The installer image on an upgrade](installer-image.md). A Helm
install is upgraded with `helm upgrade`, so there verify the chart and images
yourself as above, or enforce signatures with an admission policy of your own.
The install manifest embedded in the release's installer image names
`ichor-controller` and `ichor-ui` by digest (`ghcr.io/maeshinshin/ichor-controller:vX.Y.Z@sha256:...`),
and the chart's default images carry their digests the same way, so the
installer a verified signature covers deploys exactly the images the release
built, not what a tag points at later.
The base image of each release is pinned by digest and named in the image's
`org.opencontainers.image.base.digest` annotation. The images are reproducible
from the tag: the same commit and the same Nix shell build the same binaries
(`-trimpath`, stripped, no build timestamps).

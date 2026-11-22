---
sidebar_order: 5
---

# The installer image on an upgrade

`ichor upgrade ichor VERSION` makes the controller run a one-shot installer Job whose ServiceAccount holds nearly cluster-admin rights, so the controller checks the image first.

![The controller resolves the installer tag to a digest, verifies its cosign signature, then runs the Job by digest. A tag that does not resolve pauses the operation with InstallerImageUnresolved; a signature that fails pauses it with InstallerImageUnverified.](../../images/ih-installer-image.light.drawio.svg)

*Resolve, verify, run by digest; either failure pauses the operation.*

1. Resolve `<installerRepository>:VERSION` to a digest, or take `spec.installerDigest` (`--installer-digest sha256:...`).
2. Verify that digest's keyless cosign signature, offline against a Sigstore trusted root built into the controller, for the identity `https://github.com/maeshinshin/ichor/.github/workflows/release.yml@refs/tags/VERSION` issued by GitHub Actions (the identity of [`cosign verify`](verify.md)).
3. Run the Job as `<installerRepository>@sha256:...`. The API server refuses an installer Job that names a tag.

On failure the operation pauses with `InstallerImageUnresolved` (tag not resolved, or the signature could not be read because the registry could not be) or `InstallerImageUnverified` (signature wrong or absent). No Job is created and nothing is applied. `ichor describe operation` says why; after you fix the registry or mirror, `ichor resume operation NAME` re-runs the check. The digest and result are in `ichor version` and `IchorInstallation` `status.installerImage`.

## A registry the controller cannot read

The pause message names the route for its case.

| Failed read | Command |
|---|---|
| The tag does not resolve: give the digest you pushed (`DIGEST_FILE` of `make demo-images`) and skip the check, as the signature is read from the same registry | `ichor upgrade ichor VERSION --installer-digest sha256:... --skip-signature-check` |
| The tag resolves, the signature cannot be read (for example a blob redirects to a host that is not allowed): the digest is known | `ichor upgrade ichor VERSION --skip-signature-check` |

A fresh install, and the Cilium hand-over when `CNIHandover` pauses with `InstallerImageUnresolved`, use the same split on `ichor init`. Both routes record the skip ([below](#the-flag-is-not-one-shot)).

## Mirrors and air gaps

- The signature belongs to the digest, so a mirror verifies if it holds the image under the same digest and its OCI referrers. Copy with `oras copy --recursive`; `crane copy` and `docker push` do not copy referrers.
- The controller reads `installerRepository` anonymously, over an egress rule it keeps for that host (for `ghcr.io` also `pkg-containers.githubusercontent.com`). No other service is contacted.
- A registry that needs credentials, or redirects blobs to a disallowed host, has no way through except the pin plus the recorded skip.

## Builds nobody signs

Development and demo images (`v0.0.N-gSHA`) carry no signature. Skip the check; Ichor records it:

```sh
ichor upgrade ichor v0.0.1900000001-g1a2b3c4 --skip-signature-check
```

- The plan warns before you confirm; the controller emits Warning Event `InstallerSignatureSkipped`.
- `status.installerImage.verification` becomes `Skipped`, and the condition `InstallerImageUnverified` becomes True on `IchorInstallation`. It is sticky: only a person clears it, by removing it from `status.conditions`.
- `ichor version` says "signature NOT checked"; the UI shows an "Installer not verified" badge in the header of every page (hidden on a narrow window) and an alert in the attention list of the Overview page.
- No controller flag or chart value turns the check off. A fork that signs its own images cannot yet name its own signer; it skips the same way.

## The flag is not one-shot

> [!WARNING]
> `--skip-signature-check` (on `ichor upgrade ichor` and `ichor init`) writes `spec.installerSignature.mode: skip` into the `IchorInstallation` `ichor`, and it stays after the upgrade. While it stands, the check is off for the next Ichor upgrade and for the Cilium Helm Job. The Job still runs by digest: without `--installer-digest` the flag clears `spec.installerDigest`, the controller resolves the tag itself, and a tag the registry lacks pauses with `InstallerImageUnresolved`.

The next `ichor upgrade ichor` without the flag writes the check back. To undo the skip without upgrading, delete `spec.installerSignature` (or set `mode: required`), and set `spec.installerDigest` for a pin. The webhook accepts this from a user who may `update` `ichorinstallations/version`, and refuses it from one who may only `update` `ichorinstallations`:

```sh
kubectl edit ichorinstallation ichor
```

## Residual risk

The first upgrade onto a release with this check is made by the previous controller, which creates the Job by tag. The controller and UI images the installer then applies are still tags, not digests.

---
sidebar_order: 5
---

# The installer image on an upgrade

## How the check works

`ichor upgrade ichor VERSION` makes the controller run a one-shot installer Job
whose ServiceAccount holds nearly cluster-admin rights, so the image it runs is
checked first. Before it creates the Job the controller:

1. resolves `<installerRepository>:VERSION` to a digest (or takes
   `spec.installerDigest`, a pin you set with `--installer-digest sha256:...`),
2. verifies that digest's keyless cosign signature, offline against a Sigstore
   trusted root built into the controller, for the identity
   `https://github.com/maeshinshin/ichor/.github/workflows/release.yml@refs/tags/VERSION`
   issued by GitHub Actions (the identity of the `cosign verify` command above),
3. runs the Job with the image `<installerRepository>@sha256:...`, so what was
   verified is what runs. The API server refuses an installer Job that names a tag.

If the tag does not resolve or the signature does not verify, the operation is
paused with the reason `InstallerImageUnresolved` or `InstallerImageUnverified`,
no Job is created and nothing is applied. `ichor describe operation` shows why.
After you fix the registry or the mirror, `ichor resume operation NAME` runs the check
again. The digest and the result are in `ichor version` and in
`IchorInstallation` `status.installerImage`.

## Mirrors and air gaps

The signature belongs to the digest, not to a
registry, so a mirror verifies as long as it holds the image under the same
digest and the image's OCI referrers (copy with a tool that copies referrers,
for example `oras copy --recursive`; `crane copy` and `docker push` do not).
The controller reads `installerRepository` anonymously, over an egress rule it
maintains for that host (for `ghcr.io` also `pkg-containers.githubusercontent.com`,
where GHCR redirects blobs). No other service is contacted. A registry that
needs credentials or redirects blobs to a host that is not allowed has no way
through except the pin plus the recorded skip below.

## Builds nobody signs

Development and demo images (`v0.0.N-gSHA`) carry no
signature. Skip the check, which Ichor records:

```sh
ichor upgrade ichor v0.0.1900000001-g1a2b3c4 --skip-signature-check
```

The plan prints a warning before you confirm, the controller emits a Warning
Event `InstallerSignatureSkipped`, `status.installerImage.verification` becomes
`Skipped`, `ichor version` says "signature NOT checked", and the
UI shows an "Installer not verified" badge on every page and an alert on the
cluster page. There is no controller flag or chart value that turns the check off.

## The flag is not one-shot

> [!WARNING]
> `--skip-signature-check` (on `ichor upgrade ichor`
> and `ichor init`) writes `spec.installerSignature.mode: skip` into the
> `IchorInstallation` `ichor`, and the skip stays there after the upgrade has
> finished. While it stands, the check is off for the next Ichor upgrade and for
> the Cilium Helm Job too. The Job still runs by digest: unless you also pass
> `--installer-digest`, the flag clears `spec.installerDigest` and the controller
> resolves the version's tag to a digest itself, so a tag the registry does not
> have pauses the operation with `InstallerImageUnresolved`.

The CLI and the UI write the installer settings exactly as given, so the next
`ichor upgrade ichor` without the flag writes the check back. To undo the skip
without upgrading, edit the object and delete `spec.installerSignature` (or set
`mode: required`), and set `spec.installerDigest` if you want a pin. The
admission webhook accepts the edit from a user who may `update`
`ichorinstallations/version`, the right an upgrade needs, and refuses it from
one who may only `update` `ichorinstallations`:

```sh
kubectl edit ichorinstallation ichor
```
A fork that signs its own images has no way to name its own signer yet; it
skips the check the same way.

## Residual risk

This protects the upgrade after the one that installs it: the first upgrade onto
a release with this check is made by the previous controller, which creates the
Job by tag. The controller and UI images that the installer then applies are
still tags, not digests (residual risk).

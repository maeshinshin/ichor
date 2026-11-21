# 0171. `ichor upgrade cli` replaces the running binary only with a release whose signed checksums verify

Status: Accepted · Date: 2026-11-22 · Design section: CLI and UI · Touches: 0122

Decided: `ichor upgrade cli [VERSION] [--check] [--yes]` downloads the release archive of the chosen version from the GitHub release, verifies the signed checksums file with `imagesig.VerifyBlob` and the archive against that file, and renames the unpacked binary over the running one. It refuses on any mismatch and has no way to skip the check.

## Context

The update notice (`internal/cli/update_notice.go`) tells the operator that the `ichor` binary is older than the newest release and gives no command to change that. The prerequisite, `imagesig.VerifyBlob`, verifies a Sigstore bundle of a release blob offline against the embedded trusted root.

What a release publishes (`.goreleaser.yaml`, the `cli` job of `release.yml`): one archive per OS and architecture, `ichor_<version>_<os>_<arch>.tar.gz` for linux and darwin on amd64 and arm64, and `ichor_<version>_checksums.txt` signed keyless by cosign with the bundle `ichor_<version>_checksums.txt.sigstore.json` beside it. There is no per-archive signature and no attestation of the archive. The identity is `imagesig.IchorIdentity(tag)`. The release workflow therefore already produces what the command needs; it is unchanged.

The chart tag is pushed by the `publish` job, and the GitHub release assets are created later by `cli`, so the newest chart tag can briefly have no assets.

## Decision

1. **Source.** The assets of the GitHub release for the version, under `https://github.com/maeshinshin/ichor/releases/download/<tag>/`. Without VERSION the version is the newest final release tag of the chart repository (`releasecheck.RegistryLister` and `releasecheck.Latest`, the list the controller uses), so the command needs no cluster. With `VERSION` (vX.Y.Z) that release is used, older ones included. When the newest is not newer than the running binary the command says so and stops.
2. **Verification, before anything touches the executable.** Download the checksums file and its bundle. `VerifyBlob` checks the bundle against the sha256 of the checksums file for `IchorIdentity(tag)`. Read the archive's sha256 from the verified file, hash the downloaded archive and compare. Any failure (no bundle, a bad signature, a missing line, a different hash) stops the command with the manual steps. Nothing is written to the destination until all of it passed. There is no `--skip-verify`: a self-updater with a bypass is a backdoor. When the embedded trusted root is out of date after a Sigstore key rotation the verification fails the same way; the message points at the manual steps, which `cosign verify-blob` carries out with a current root.
3. **Replacing.** Resolve the executable with `filepath.EvalSymlinks(os.Executable())`, so the link is not replaced. Extract only the `ichor` member into a file in the same directory (a rename across filesystems fails), mode 0755, then `rename` it over the executable; the running process keeps its old inode. The staging file is created before the confirmation, so a directory that is not writable (root-owned `/usr/local/bin`, a read-only filesystem) is reported before a download, with the directory named and the suggestion to run with the permissions that directory needs. No `.old` copy is kept and nothing is rolled back: the previous version is one `ichor upgrade cli VERSION` away.
4. **Refusals.** A build whose `version.Version` is `dev` is never replaced; that variable, not the build info, is the signal. An OS or architecture the release does not ship (anything other than linux and darwin on amd64 and arm64) is refused with the archive name that would be needed. A 404 on the assets says the release may still be publishing and to retry shortly.
5. **Confirmation.** The command shows the running version, the target and the executable path, then asks `Proceed? [y/N]`; `--yes` skips only the question. `--check` shows the same and changes nothing, including no download of the archive.
6. **Offline and air-gapped.** The command reaches ghcr.io (the version) and github.com (the assets). When a request cannot be made it says so, names the URL, and prints the manual steps: download the archive, the checksums and the bundle, `cosign verify-blob` with the release's identity, `sha256sum -c`, and copy the binary. A machine that cannot reach either host can pass VERSION to skip the registry and download the three files elsewhere.

## Consequences

- No new dependency and no new signing scheme: sigstore-go is already linked through `internal/imagesig`.
- The CLI that verifies a release is the one being replaced, which is the usual limit of a self-updater; the manual steps use a separate tool.
- The command writes to one file on the operator's machine, never to a node or a CR; the writer rule of the design is untouched.
- Windows is not supported because the release ships no Windows archive.

## Alternatives considered

- Sign each archive. Rejected: the checksums file already covers every archive, and a second signature is a second thing to rotate.
- Keep the old binary as `ichor.old`. Rejected: about 100 MB per upgrade and a rollback nobody asked for.
- A `--skip-verify` flag for unsigned development builds. Rejected: a dev build is refused instead, and a flag that installs unverified code is the thing the command exists to prevent.

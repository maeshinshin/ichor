# 0053. `ichor restore --recover-cluster`: full-loss etcd recovery from the bundle and a snapshot

Status: Accepted · Date: 2026-10-26 · Design section: Backup, recovery, certificates

Decided: `ichor restore --recover-cluster` runs standalone (no Kubernetes, no Ichor): it reads the local secrets bundle, takes S3 credentials from environment variables, picks the newest snapshot that decrypts and passes the etcd checksum (or the one named by `--snapshot`, which is refused if broken), asks for confirmation, then uploads it with `EtcdRecover` to one freshly installed control plane and calls `Bootstrap{RecoverEtcd: true}`; restoring into a live cluster is a separate controller-run operation (ADR 0089)

## Context

Goal 4: restore from the secrets bundle and a snapshot alone. ADR 0047 fixed the stored format and left restore to
this ADR. After a total loss the controller is gone too, so the command cannot lean on any CR, Secret or the
Connect API (ADR 0049); it is one of the CLI operations that predate the cluster, like `init` and `adopt`.

## Decisions

1. **Talos side.** The sequence is `talosctl bootstrap --recover-from`
   (`cmd/talosctl/cmd/talos/bootstrap.go`, Talos 1.14.3): `Client.EtcdRecover` streams the snapshot, then
   `Client.Bootstrap` with `RecoverEtcd: true`. In the server (`v1alpha1_server.go`), `EtcdRecover` writes
   `/var/lib/etcd.snapshot` atomically and needs a control plane (`CheckControlplane`); `Bootstrap` needs
   `IsBootstrapAllowed` (the `cri` service healthy), a machine config with an etcd CA, time in sync, and an
   **empty** `/var/lib/etcd` (`AlreadyExists` otherwise); the etcd service then runs `recoverFromSnapshot`
   (`services/etcd.go`), which restores with `manager.Status` and the etcd hash check (we do not pass
   `RecoverSkipHashCheck`: our snapshots always carry the trailer). So the target is **one control plane that is
   installed and configured (not in maintenance mode) with a machine config made from the same bundle, with empty
   etcd data**; the CLI dials it with the bundle's admin identity (the `dialAdmin` recipe) and exactly that one
   node. Other control planes join by themselves afterwards. Applying the machine config to the replacement node
   is **not** done here (the CRs that render it lived in the lost etcd); the operator installs the node first, for
   example with `talosctl apply-config --insecure` from the config they kept. Deferred: the design's "controller
   re-reconciles every node the way `adopt` does and waits for confirmation before applying anything" is the
   restored controller's job and is not built.
2. **Bundle.** Same file `ichor init` writes (`internal/cli/bundlefile.go`): `~/.ichor/clusters/<cluster>/secrets.yaml`,
   or `--secrets-bundle` for a copy kept elsewhere (the machine doing the recovery is often not the one that ran
   `init`). Decoding errors carry the path only, never content. The derived passphrase and the bundle are never
   printed: none of the bundle's scalars, the passphrase or the S3 keys appear in
   stdout, stderr or any error, including when an S3 or Talos error string contains the credentials (every such error
   passes through a scrubber holding them).
3. **S3 credentials.** Non-secret addressing is flags (`--s3-endpoint`, `--s3-bucket`, `--s3-region`,
   `--s3-prefix`, `--s3-ca-file`). Secrets are **only** environment variables, `ICHOR_S3_ACCESS_KEY_ID`,
   `ICHOR_S3_SECRET_ACCESS_KEY`, `ICHOR_S3_SESSION_TOKEN`, with the standard `AWS_*` ones as fallback; no flag
   exists for them, so they never appear in `ps`. (Environment is readable by the same user via `/proc`, which is
   the same exposure as the bundle file; a credentials-file option is deferred.)
4. **Which snapshot.** Default newest. Each candidate, newest first, is downloaded, decrypted and run through
   `etcdbackup.Verifier` (size and sha256 trailer) **before anything is sent or even confirmed**; one that fails
   is skipped, reported on stderr, and never sent. Falling back to an older snapshot loses data, so the
   confirmation names the snapshot actually chosen and its time. `--snapshot <name>` is exclusive: broken means
   an error, never a substitute. A bundle that is not the cluster's fails age's authentication on every object,
   and the final error says nothing was sent. The plaintext is kept in memory, not on disk (it holds every
   Secret); `shortcut:` the snapshot stays in memory, which a multi-GB etcd outgrows; switch to a protected spool file then.
5. **Confirmation.** The shared `confirm` and the global `--yes`; the prompt states the node, the snapshot and its
   time, that state reverts, and that it cannot be undone. Declining contacts nothing.

## Locking

The cluster lock (`acquireDisruptiveLock`, `clusterlock.Acquire`) is a Kubernetes object, and in full loss there
is no Kubernetes. The command is the only actor by construction, and it is refused by Talos unless etcd data is
empty. This code never calls `Acquire`. The live-cluster restore, which
does have a cluster, must take the lock.

## Not done

- ~~Restore into a live cluster (UI and CLI)~~ is built, in ADR 0089: `ichor restore --live` and the UI create an
  `etcd-restore` operation; the controller takes the lock, wipes etcd on every control plane, recovers the snapshot
  on one with the calls above, lets the others rejoin and reconciles every machine again; it reuses `fetchVerified`
  (now `etcdbackup.FetchVerified`). `--recover-cluster` is still the way back when that operation cannot finish,
  for example when the controller ran on the only control plane that was wiped.
- A restore round trip against a real Talos (e2e) is the design's regular proof and is not covered here.

- Amendment 2026-10-27: `loadBundleFile` now decodes through `secretsbundle.Decode`, which fills `Bundle.Clock`; before, `--recover-cluster` panicked in `dialRecover` on a nil Clock.

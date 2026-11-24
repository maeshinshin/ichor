# Upgrade Ichor with the installer

`ichor upgrade ichor VERSION` moves an Ichor that `ichor init` or `ichor adopt` installed. An Ichor that Helm installed is upgraded with [`helm upgrade`](install-with-helm/upgrade.md).

![Five steps in a row: the checks (downgrade, stored fields, installer check, plan), which may refuse; sign-in, set up first if ichor-ui has none; the installer Job, which writes the CRDs, controller and ichor-ui; the TalosConfigPatch migration, spec.documents to spec.config; and ichor rollout config, which you run.](../images/ih-upgrade-ichor.light.drawio.svg)

*Refusals come first; the migration is the last write.*

## Steps

| Step | What happens | If it fails |
|---|---|---|
| Version | `VERSION` must be a semantic version, and the CLI may be at most one minor from the running Ichor | refused, nothing written |
| Helm | An install owned by Helm is refused and the Helm command is printed | refused |
| Downgrade | A `VERSION` older than the running Ichor is refused | refused, nothing written |
| Stored fields | A `TalosConfigPatch` that still holds `tls.clientIdentity` or `extraManifestHeaders` refuses ([below](#credential-fields)) | refused, nothing written |
| Installer check | The target's installer image runs a check-only Job: a CRD that would lose a version or schema property is refused | refused, nothing written |
| Plan | The server's plan for the upgrade may block it (an unfinished operation, for example) | refused, nothing written |
| Sign-in | An ichor-ui with no sign-in gets one, after every check above ([below](#sign-in)) | stops before the upgrade |
| Write | The installer Job applies the CRDs, the controller, then ichor-ui, and rolls back from its snapshot on failure | rolled back |
| Migrate | Every `TalosConfigPatch` that sets `spec.documents` is moved to `spec.config` ([below](#talosconfigpatch-migration)) | logged; the upgrade stays done |

An ichor-ui with no sign-in is not a refusal of the check, because this command turns sign-in on right after it; `installer run` still refuses one. The check Job needs an installer image that knows `installer check`; one that does not fails the check with a message saying so. The CLI itself resolves and verifies the installer image from your machine, and the upgrade then runs that same digest; if the registry is not reachable from your machine, run the CLI from a host that reaches it, or pin the image you pushed with `--installer-digest sha256:... --skip-signature-check`, which reads no registry ([installer image](install-with-helm/installer-image.md)).

## Version rule

- One minor at a time: write commands, this one included, are refused when the CLI and the running Ichor are more than one minor apart. Use the CLI of the running version, or step through each minor.
- No downgrades: an older `VERSION` is refused, and so is a CRD that would take anything away (the installer check).

## Sign-in

An Ichor from before sign-in became the default has an ichor-ui without one, and the new release does not start without it. Once every check has passed, the upgrade asks (`--yes` skips the question) and:

1. creates the first administrator and switches ichor-ui to sign-in at `--ui-url`;
2. sets `spec.authentication.ichor` on each `TalosCluster` that can carry it (Talos v1.14 or later);
3. prints the generated password once, at the end.

Step 2 marks every control plane `APIServerRestartRequired`. kube-apiserver restarts only when you run `ichor rollout config`. With sign-in already on, the `--ui-url`, `--admin-user`, `--ui-tls-*` and `--kube-api-*` flags are refused.

## Credential fields

`tls.clientIdentity` and `extraManifestHeaders` are retired (ADR 0152), and the new CRD would drop them silently. While either is stored, the upgrade refuses. Move it out of Ichor and set it on the node outside Ichor, then upgrade.

## TalosConfigPatch migration

`TalosConfigPatch.spec.documents` is deprecated, and `spec.config` replaces it. The last step of the upgrade rewrites each patch in one update and sets `Migrated=True`.

| Patch | Result |
|---|---|
| Sets `spec.documents` only | `spec.config` written, `spec.documents` cleared, `Migrated=True` |
| A setting has no place in `spec.config` (for example `cluster.extraManifests`) | left as stored, `Migrated=False` naming the setting; it keeps rendering from `spec.documents` |
| Sets both `spec.documents` and `spec.config` | left as stored, listed; remove `spec.documents` by hand |

For a `Migrated=False` patch, write the setting in `spec.config` and clear `spec.documents` yourself. If this step fails, run `ichor migrate patches`; it is idempotent.

## Reaching the nodes

The controller writes configuration to nodes only through `ichor rollout config`. A migration alone changes nothing on a node: a setting that only moved to its own document renders the same. Run it after the upgrade for a setting whose value changed, and for the API server restart that sign-in needs.

## Compared with `helm upgrade`

| | `ichor upgrade ichor` | `helm upgrade` |
|---|---|---|
| Moves | An installer-made Ichor | A Helm-made Ichor |
| Sign-in | Set up for you, after the checks | Set in the chart values first |
| Patch migration | Last step | Not run: run `ichor migrate patches` yourself |

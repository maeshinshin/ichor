# Upgrade Ichor with the installer

`ichor upgrade ichor VERSION` moves an Ichor that `ichor init` or `ichor adopt` installed. An Ichor that Helm installed is upgraded with [`helm upgrade`](install-with-helm/upgrade.md).

![Five steps in a row: the checks, which may refuse; sign-in, set up first if ichor-ui has none; the installer Job, which writes the CRDs, controller and ichor-ui; the TalosConfigPatch migration, spec.documents to spec.config; and ichor rollout config, which you run.](../images/ih-upgrade-ichor.light.drawio.svg)

*Refusals come first; the migration is the last write.*

## Steps

| Step | What happens | If it fails |
|---|---|---|
| Version | `VERSION` must be a semantic version, and the CLI may be at most one minor from the running Ichor | refused, nothing written |
| Helm | An install owned by Helm is refused and the Helm command is printed | refused |
| Sign-in | An ichor-ui with no sign-in gets one first ([below](#sign-in)) | stops before the upgrade |
| Downgrade | A `VERSION` older than the running Ichor is refused | the operation fails |
| CRDs | The new CRDs may only add: a removed version or schema property refuses the whole upgrade | refused before any write |
| Write | The installer Job applies the CRDs, the controller, then ichor-ui, and rolls back from its snapshot on failure | rolled back |
| Migrate | Every `TalosConfigPatch` that sets `spec.documents` is moved to `spec.config` ([below](#talosconfigpatch-migration)) | logged; the upgrade stays done |

Before the Job runs, the controller also checks the installer image ([installer image](install-with-helm/installer-image.md)).

## Version rule

- One minor at a time: write commands, this one included, are refused when the CLI and the running Ichor are more than one minor apart. Use the CLI of the running version, or step through each minor.
- No downgrades: an older `VERSION` is refused, and so is a CRD that would take anything away.

## Sign-in

An Ichor from before sign-in became the default has an ichor-ui without one, and the new release does not start without it. The upgrade then asks first (`--yes` skips the question) and:

1. creates the first administrator and switches ichor-ui to sign-in at `--ui-url`;
2. sets `spec.authentication.ichor` on each `TalosCluster` that can carry it (Talos v1.14 or later);
3. prints the generated password once, at the end.

Step 2 marks every control plane `APIServerRestartRequired`. kube-apiserver restarts only when you run `ichor rollout config`. With sign-in already on, the `--ui-url`, `--admin-user`, `--ui-tls-*` and `--kube-api-*` flags are refused.

## TalosConfigPatch migration

`TalosConfigPatch.spec.documents` is deprecated, and `spec.config` replaces it. The last step of the upgrade rewrites each patch in one update and sets `Migrated=True`.

| Patch | Result |
|---|---|
| Sets `spec.documents` only | `spec.config` written, `spec.documents` cleared, `Migrated=True` |
| A setting has no place in `spec.config` (for example `cluster.extraManifests`) | left as stored, `Migrated=False` naming the setting; it keeps rendering from `spec.documents` |
| Sets both `spec.documents` and `spec.config` | left as stored, listed; remove `spec.documents` by hand |

For a `Migrated=False` patch, write the setting in `spec.config` and clear `spec.documents` yourself. If this step fails, run `ichor migrate patches`; it is idempotent.

## Reaching the nodes

The controller writes configuration to nodes only through `ichor rollout config`. After the upgrade, run it for the migrated settings, and for the API server restart that sign-in needs, to reach the nodes.

## Compared with `helm upgrade`

| | `ichor upgrade ichor` | `helm upgrade` |
|---|---|---|
| Moves | An installer-made Ichor | A Helm-made Ichor |
| Sign-in | Set up for you | Set in the chart values first |
| Patch migration | Last step | Not run: run `ichor migrate patches` yourself |

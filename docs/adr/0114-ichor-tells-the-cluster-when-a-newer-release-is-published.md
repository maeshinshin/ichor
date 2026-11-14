# 0114. Ichor tells the cluster when a newer release is published

Status: Accepted · Date: 2026-11-03 · Design section: Upgrades (Ichor) · Touches: 0059, 0100, 0105, 0108

Decided: the controller (leader only, at start and every 6 hours with jitter) lists the chart repository's tags on ghcr.io with Helm's registry client, takes the highest final semver release, and records it in `IchorInstallation.status.latestRelease`; the UI shows "v<X> available" on the Ichor row and in the attention list, `ichor version` names it, and the check is switched by `spec.updateCheck.enabled` or the chart's `updateCheck.enabled` (default on), with a controller-only egress policy to the registry host that exists only while the check is on

## Context

An installed Ichor does not know that a newer release exists. The user asked for the dashboard to show it.
Releases are published by the `v*` tag workflow (ADR 0108): the chart goes to
`oci://ghcr.io/maeshinshin/charts/ichor` last, after the images. The chart tag is the chart version
(`0.1.0`), the image tag carries a `v` (`v0.1.0`).

The check is a notification about Ichor itself. It sends one anonymous tag listing to the registry and
nothing about the cluster. It must not upgrade anything (the controller never upgrades itself, ADR 0059),
and an installation that does not want the network call must be able to turn it off.

## Decision

1. **Source: the chart repository's tags on the registry.** The controller lists `ghcr.io/maeshinshin/charts/ichor`
   with Helm's registry client (`helm.sh/helm/v3/pkg/registry`, `Client.Tags`), which is already a direct
   dependency and already pages through `tags/list` with the anonymous token flow. No GitHub API (rate
   limits, token), no new OCI library. The chart is pushed after the images, so a tag there means the release
   is complete.

2. **Which tags count.** A tag that parses as semver with a `v` added and has no prerelease part is a
   release (`0.1.0` is `v0.1.0`). Prereleases and anything else are ignored. The highest release by
   `golang.org/x/mod/semver` is the latest.

3. **Which running versions get a prompt.** The running version is compared with `semver.Compare`. A
   version that is not valid semver (a dev build, `dev`) never prompts, because nothing can be compared. A
   demo build (`v0.0.<N>-g<sha>`) is a prerelease of its core and compares by the same rule: a published
   final release above its core is newer and is shown. No special case.

4. **When it runs.** The controller runs it as a leader-only manager Runnable: once when it becomes leader,
   then every 6 hours with a jitter of up to 10 percent, so replicas of many installations do not poll at
   the same instant.

5. **Where the result lives.** `IchorInstallation.status.latestRelease` with `version` (the highest release
   found), `checkedAt` and `error`. On a failed check `version` keeps the last good value and `error` is set; a
   later success clears `error`. It is written with a merge patch of the status, so the version and
   condition fields the other reconciler writes are not lost. A Helm install has the IchorInstallation too
   (ADR 0105), so no other place is needed. The fields are new and append-only.

6. **The switch.** `spec.updateCheck.enabled` (settings group, `ichorinstallations/settings` RBAC, ADR 0105)
   overrides the fallback, which is the controller flag `--update-check` (default true). The chart value
   `updateCheck.enabled` passes the flag. `settings.readOnly` ignores the spec, as for every setting. When the
   check is off the controller clears `status.latestRelease`, and it deletes the egress policy below.
   Default on: the request contains nothing about the cluster.

7. **Egress.** The controller is the only pod that makes the request, so the egress is a separate
   CiliumNetworkPolicy `ichor-controller-release-check` (name fixed, in `ichor-system`) with
   `endpointSelector` on `control-plane: controller-manager` and one rule: `toFQDNs` of the registry host
   on 443/TCP. It is written only while the check is enabled, and only when the controller's static
   policy is installed, for the same reason as the Talos egress policy (ADR 0100): a lone egress rule would
   cut every other flow of the controller. It is deleted when the check is off, which needs the `delete`
   verb on `ciliumnetworkpolicies` in the controller's Role. ichor-ui gets no rule: it shows the result, it
   does not fetch it. Helm and installer installs both get the same behaviour, because the policy is written
   by the controller and not rendered by the chart.

8. **Display.** `GetIchorInstallation` gains `latest_version` and `release_available` (the server compares
   with `installed_version` with semver), `release_check_error` and `release_checked_at`. The Overview's Ichor row shows
   "v<X> available" next to Upgrade, and the attention list gets an item. An error shows nothing on the
   Overview; the Settings page shows a muted line with the error and the time of the last check.
   The Upgrade flow is unchanged: it still moves to `spec.version`, and nothing installs a release by itself.

9. **CLI.** `ichor version` prints the newer release when the cluster knows one. `ichor set settings
   updateCheck.enabled=false` writes the switch.

## Consequences

- An installation that runs a dev build never prompts. A demo build prompts for a published release.
- A registry that cannot be reached shows nothing on the dashboard, and the error is visible in Settings.
- The controller's egress grows by one FQDN on 443, and only when the check is on. The repository is the
  controller flag `--update-check-repository` (default the chart repository above), so a mirror is a flag,
  and the policy follows the host of that repository.
- The check does not verify signatures; the chart is signed by the release workflow (ADR 0108), and a
  "newer" tag is only a prompt to look at the release notes and verify it as the install guide says.

## Amendment: the CLI notice (2026-11-03)

Decision 9 is extended. After every command that reaches a cluster, the CLI prints one line on stderr when
`status.latestRelease` is newer than the installed version, or newer than the running `ichor` binary (that line
says to update the binary). The CLI makes no request of its own: it reads the IchorInstallation through the client
the command already built, with one Get and a 3-second timeout. A failed check (`error` set) shows nothing.

- It runs from `Execute` after the command returns, so a failed command gets it too (cobra skips
  `PersistentPostRun` on error).
- It is shown only on a terminal, not when `-o` is typed (the output is for a script), and never for `help` or
  shell completion. The default output format does not count as typed, so the default human output shows it.
- At most once per 24 hours per cluster, keyed by the API server host, in `~/.ichor/update-notice.json` (mode
  0600). Nothing is recorded when nothing is shown.
- `ICHOR_NO_UPDATE_NOTICE=1` switches it off.

## Not decided

- Notifications outside the dashboard (email, webhook). Not part of this ADR.
- Pinning the check to a channel (stable only, or release candidates). Only final releases are offered.

---
sidebar_order: 2
---

# Updates

The controller checks whether a newer Ichor release is published, and shows it.
The check installs nothing.

![How the release check works. The controller lists the chart tags on start and every six hours, and keeps the newest final release in status.latestRelease. The dashboard shows v<X> available next to Upgrade and names it in the attention list; the CLI prints one stderr line on a terminal, at most once a day per cluster. Nothing is installed by the check: Upgrade, or helm upgrade, still moves Ichor.](../../images/settings-update-check.light.drawio.svg)

*The release check, what it shows and what it never does.*

The controller lists the tags of the chart repository (`ghcr.io/maeshinshin/charts/ichor`) on start and every
six hours, and keeps the newest final release in `status.latestRelease`. Prereleases and versions that
are not semver are ignored. The dashboard then shows "v<X> available" next to Upgrade on the Ichor row, and the
attention list names it. A dev build is never told about a release.

> [!NOTE]
> Nothing is installed by the check. Upgrade is still how Ichor moves, and a
> Helm install is upgraded with `helm upgrade`.

## What the check sends

The request is one anonymous tag listing; it carries nothing about the cluster.

Turning the check off (`ichor set settings updateCheck.enabled=false`, or the
chart's `updateCheck.enabled: false`, which passes the flag) stops the requests
and removes the controller's egress rule for them (`ichor-controller-release-check`).
`status.latestRelease` keeps the last result and its error: a failed check shows
nothing on the dashboard, and Settings says why.

## The CLI

The CLI shows the same result. After a command that reaches a cluster, `ichor` prints one line on stderr when a
newer release is known, or when the `ichor` binary itself is older than it. It does this on a terminal only, at most
once a day per cluster, and not with `-o`.

Set `ICHOR_NO_UPDATE_NOTICE=1` to turn the line off. The CLI makes no request of
its own: the line comes from the `IchorInstallation` the command already reads.

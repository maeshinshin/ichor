---
sidebar_order: 2
---

# Updates

The controller checks whether a newer Ichor release is published and shows it.
The check installs nothing: Upgrade, or `helm upgrade` on a Helm install, still
moves Ichor.

![How the release check works. The controller lists the chart tags on start and every six hours, and keeps the newest final release in status.latestRelease. The dashboard shows v<X> available next to Upgrade and names it in the attention list; the CLI prints one stderr line on a terminal, at most once a day per cluster. Nothing is installed by the check: Upgrade, or helm upgrade, still moves Ichor.](../../images/settings-update-check.light.drawio.svg)

*The release check, what it shows and what it never does.*

- **Check.** The controller lists the tags of `ghcr.io/maeshinshin/charts/ichor`
  on start and every six hours and keeps the newest final release in
  `status.latestRelease`. It lists the Talos installer
  (`ghcr.io/siderolabs/installer-base`) and kubelet (`ghcr.io/siderolabs/kubelet`)
  image tags the same way, for `status.upgradeTargets`. Prereleases and non-semver tags are ignored. A dev
  build is never told about a release.
- **Sent.** Three anonymous tag listings (the chart and the two image repositories); nothing about the cluster.
- **Off.** `ichor set settings updateCheck.enabled=false` (or chart
  `updateCheck.enabled: false`) stops the requests and removes the controller's
  egress rule for them (`ichor-controller-release-check`). `status.latestRelease`
  keeps the last result and error; a failed check shows nothing on the
  dashboard, and Settings says why.
- **CLI.** After a command that reaches a cluster, `ichor` prints one stderr
  line when a newer release is known or the binary is older than it. Terminal
  only, at most once a day per cluster, never with `-o`. It makes no request of
  its own. `ICHOR_NO_UPDATE_NOTICE=1` turns it off. `ichor upgrade cli` replaces
  the binary with a verified release.

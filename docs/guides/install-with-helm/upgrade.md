---
sidebar_order: 9
---

# Upgrade and uninstall

![ichor upgrade ichor is refused on a Helm install and prints the Helm command; helm upgrade upgrades it. helm uninstall keeps the CRDs and every cluster object.](../../images/ih-upgrade.light.drawio.svg)

*Helm owns upgrade and uninstall.*

- Upgrade with `helm upgrade`; the CRDs are in the chart's templates and upgrade with it.
- After an upgrade from v0.2.x, run `ichor migrate patches`. The controller applies a change that needs no reboot by itself. `ichor rollout config` rolls out only what waits for you: nodes at `RebootRequired`, and control planes at `APIServerRestartRequired` (the restart that sign-in needs). A migration alone changes nothing on a node, because a setting that only moved renders the same.
- `ichor` commands that would run the installer Job refuse and print the Helm command. The UI's Helm detection is limited, so some of its upgrade prompts may still describe the installer path.

> [!IMPORTANT]
> `helm uninstall` keeps the CRDs by default (`crd.keep: true`), and so every cluster object. Cilium is a separate release and stays.

For an Ichor the installer made, see [Upgrade Ichor with the installer](../upgrade-ichor.md).

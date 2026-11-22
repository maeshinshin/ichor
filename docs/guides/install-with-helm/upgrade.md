---
sidebar_order: 9
---

# Upgrade and uninstall

![ichor upgrade ichor is refused on a Helm install and prints the Helm command; helm upgrade upgrades it. helm uninstall keeps the CRDs and every cluster object.](../../images/ih-upgrade.light.drawio.svg)

*Helm owns upgrade and uninstall.*

- Upgrade with `helm upgrade`; the CRDs are in the chart's templates and upgrade with it.
- `ichor` commands that would run the installer Job refuse and print the Helm command. The UI's Helm detection is limited, so some of its upgrade prompts may still describe the installer path.

> [!IMPORTANT]
> `helm uninstall` keeps the CRDs, and so every cluster object. Cilium is a separate release and stays.

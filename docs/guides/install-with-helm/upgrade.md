---
sidebar_order: 9
---

# Upgrade and uninstall

Upgrade with `helm upgrade`; the CRDs are in the chart's templates and upgrade
with it. `ichor` commands that would run the installer Job refuse on a Helm
install and print the Helm command. The UI server's detection of a Helm install
is limited, so some of its upgrade prompts may still describe the installer
path.

> [!IMPORTANT]
> `helm uninstall` keeps the CRDs and therefore every cluster object, and
> does not touch Cilium, which is a separate release.

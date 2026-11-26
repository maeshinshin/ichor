---
sidebar_order: 9
---

# Upgrade and uninstall

![ichor upgrade ichor is refused on a Helm install and prints the Helm command; helm upgrade upgrades it. helm uninstall keeps the CRDs and every cluster object.](../../images/ih-upgrade.light.drawio.svg)

*Helm owns upgrade and uninstall.*

- Upgrade with `helm upgrade`; the CRDs are in the chart's templates and upgrade with it.
- An upgrade from v0.2.x moves each patch's `spec.documents` to `spec.config` by itself: a post-upgrade hook Job runs the controller image's `migrate-patches` under a ServiceAccount that exists only while it runs. A patch it cannot move is left as stored with `Migrated=False`, and the upgrade still succeeds; `ichor migrate patches` does it again by hand. The controller applies a change that needs no reboot by itself. `ichor rollout config` rolls out only what waits for you: nodes at `RebootRequired`, and control planes at `APIServerRestartRequired` (the restart that sign-in needs). A migration alone changes nothing on a node, because a setting that only moved renders the same.
- `ichor` commands that would run the installer Job refuse and print the Helm command. The UI's Helm detection is limited, so some of its upgrade prompts may still describe the installer path.

> [!IMPORTANT]
> `helm uninstall` keeps the CRDs by default (`crd.keep: true`), and so every cluster object. Cilium is a separate release and stays.

> [!WARNING]
> The namespace Ichor runs in carries the label `ichor.maesh.dev/protected` (the controller sets it at start-up) and cannot be deleted until you say so. Deleting it takes the controller down while the clusters it manages are still there, and the namespace then stays `Terminating` for good. To remove Ichor on purpose:
>
> 1. Export each bundle: `ichor export bundle NAME`.
> 2. Remove the clusters: set the annotation `ichor.maesh.dev/allow-delete=true` on each TalosCluster and TalosMachine, then delete them.
> 3. Annotate the bundle Secret `ichor.maesh.dev/allow-delete=true`.
> 4. Annotate the namespace `ichor.maesh.dev/allow-delete=true`, then `kubectl delete namespace ichor-system`.
>
> If a namespace is already stuck, see [Recover a namespace stuck in Terminating](../troubleshooting.md#recover-a-namespace-stuck-in-terminating).

For an Ichor the installer made, see [Upgrade Ichor with the installer](../upgrade-ichor.md).

---
sidebar_order: 3
---

# Automatic patching

Off by default. When on, the controller starts upgrades to **patch releases of
the running minor** by itself: Talos, then Kubernetes, one component at a time
and at most one operation per cluster. Cilium is never offered: it moves only to
the chart the running Ichor embeds, which a person starts. It never moves to a
new minor and never starts anything that needs `--override`.

Turn it on with the Settings page's "Automatic patching" card, with
`ichor set settings autoPatch.enabled=true`, or with the chart's
`autoPatch.enabled`.

> [!WARNING]
> If a tool keeps your clusters in Git and reverts edits, leave this off. The
> controller writes the new version into the `TalosCluster` spec, and such a
> tool would undo it.

## When it starts an operation

It offers only what the release check listed (so the update check must be on)
and what the compatibility table accepts for the cluster.

![The conditions that start an automatic operation. Four checks must all hold: inside the window, every approved machine Ready, no operation of the cluster unfinished, and the cluster lock free. The operation is an ordinary TalosOperation annotated ichor.maesh.dev/auto-patch. If it fails or pauses, automatic patching halts everywhere and never retries. If it completes or is deleted, the halt ends by itself.](../../images/settings-auto-patch-gates.light.drawio.svg)

*It starts one only when all four checks hold.*

The window is UTC, `HH:MM-HH:MM`, may wrap midnight; none means any time. The
operation is an ordinary `TalosOperation`, annotated
`ichor.maesh.dev/auto-patch: "true"`, shown as "Started by: Automatic patching",
with an `AutoPatchStarted` Event on the cluster for each start.

## How the version is written

For Talos or Kubernetes the new version goes into the `TalosCluster` spec before
the operation is created. The controller impersonates the ServiceAccount
`ichor-auto-patch`, which holds only `get` and `update` on `talosclusters` and
`create` on `talosoperations`. The webhook refuses anything it writes except a
newer patch of the same minor in the two version fields. The audit log shows
those writes as that user.

## When it stops

If an automatic operation fails or pauses, automatic patching stops everywhere:
condition `AutoPatchHalted`, a warning Event, an item in the Overview's
attention list. This includes the wait for an operation's own automatic retry
([Operations](../../concepts/operations.md)); the halt clears if that retry
succeeds.

Otherwise look at the operation, resume it with `ichor resume operation` or
delete it, then clear the halt: the Settings page's "Clear the halt" button, or
`ichor set settings autoPatch.acknowledgeHalt=NAMESPACE/NAME` (allowed under
read-only mode). The halt also ends when the operation completes or is deleted,
or when a later manual upgrade of that cluster completes.

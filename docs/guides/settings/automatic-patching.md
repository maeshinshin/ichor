---
sidebar_order: 3
---

# Automatic patching

Off by default. When you turn it on, the controller starts upgrades to **patch
releases of the running minor** by itself: Talos, then Cilium, then Kubernetes,
one component at a time and at most one operation per cluster. It never moves to
a new minor and never starts anything that would need `--override`.

Turn it on with the Settings page's "Automatic patching" card, with
`ichor set settings autoPatch.enabled=true`, or with the chart's
`autoPatch.enabled`.

> [!WARNING]
> If your clusters are kept in Git by a tool that reverts edits, leave automatic
> patching off. The controller writes the new version into the `TalosCluster`
> spec, and such a tool would undo it.

## When it starts an operation

It only offers what the release check listed (so the update check must be on),
what the compatibility table accepts for the cluster, and for Cilium only images
whose signatures verified.

![The conditions that start an automatic operation. Four checks must all hold: inside the window, every approved machine Ready, no operation of the cluster unfinished, and the cluster lock free. The operation is an ordinary TalosOperation annotated ichor.maesh.dev/auto-patch. If it fails or pauses, automatic patching halts everywhere and never retries. If it completes or is deleted, the halt ends by itself.](../../images/settings-auto-patch-gates.light.drawio.svg)

*It starts one only when all four checks hold.*

It starts one only when all of this holds: it is inside the window (UTC,
`HH:MM-HH:MM`, may wrap midnight; no window means any time), every approved
machine is Ready, no operation of the cluster is unfinished, and the cluster lock
is free.

The operation is an ordinary `TalosOperation` that the normal executor runs. It
carries the annotation `ichor.maesh.dev/auto-patch: "true"`, shows "Started by:
Automatic patching" on its page, and each start leaves an `AutoPatchStarted`
Event on the cluster.

## How the version is written

For a Talos or Kubernetes patch the new version goes into the `TalosCluster` spec
before the operation is created. The controller never writes a spec on its own
account: it impersonates the ServiceAccount `ichor-auto-patch`, which holds only
`get` and `update` on `talosclusters` and `create` on `talosoperations`. The
webhook refuses anything it writes except a newer patch of the same minor in the
two version fields. The audit log shows those writes as that user.

## When it stops

If an automatic operation fails or pauses, automatic patching stops everywhere
(condition `AutoPatchHalted`, a warning Event, an item in the Overview's
attention list) and never retries.

Look at the operation, resume it with `ichor resume operation` or delete it, then
clear the halt: the Settings page's "Clear the halt" button, or
`ichor set settings autoPatch.acknowledgeHalt=NAMESPACE/NAME`.

The halt also ends by itself when the operation completes or is deleted, or when
a later manual upgrade of that cluster completes. Clearing the halt is allowed
under read-only mode.

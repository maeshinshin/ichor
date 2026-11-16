---
sidebar_order: 1
---

# Before you start

## What you need

- Every node you adopt runs Talos v1.14 or later, and the version contract of the
  `TalosCluster` adopt creates is v1.14 or later. Sign-in cannot be turned off, and
  only v1.14 can carry Ichor's issuer. A node you add with `--add-machine` to a
  cluster that was adopted before needs only Talos v1.12 or later: the cluster's
  sign-in and issuer exist already, `--add-machine` sets up neither (it takes none
  of the sign-in flags), so the reason for v1.14 does not apply to it. The two
  floors differ on purpose. The contract is probed on the first control plane only (a node
  that differs shows up in its own plan), from its running version down to v1.12 so
  that a configuration generated for an older contract is refused by name, and
  `--version-contract` names one instead. Either refusal is made before anything is
  written. When the cluster runs a Talos below v1.14, upgrade it first, outside
  Ichor, with `talosctl upgrade` or your usual tool (`ichor upgrade talos` works
  only on a cluster Ichor already manages, and there is no `TalosCluster` yet),
  then adopt again. When the cluster runs v1.14 but an older contract was probed,
  adopt it with `--version-contract v1.14`.
- Your own `talosconfig` for the cluster (`--talosconfig`, `$TALOSCONFIG` or
  `~/.talos/config`) and a kubeconfig for its Kubernetes API (`--kubeconfig`,
  `$KUBECONFIG` or `~/.kube/config`). Adopt reads nodes with the talosconfig
  and never sends it to the controller.
- The cluster's secrets bundle, either as a file or recovered from a control
  plane (below).
- `--namespace` (`-n`, default `ichor-system`) can only be the namespace Ichor's
  controller runs in, because the controller reads the secrets bundle Secret from its
  own namespace. Adopt checks this first, before it reads or writes anything, and
  refuses any other value naming the right one. When it cannot find the controller's
  Deployment or Helm release (an operator who may not list them cluster-wide), it
  cannot confirm the namespace: the default goes ahead and any other value is
  refused with "could not confirm". `--add-machine` does not make this check, so
  give it the controller's namespace yourself.

Adopt never applies anything to a node itself. The controller does, in the
last phase, and only after a zero-drift check.

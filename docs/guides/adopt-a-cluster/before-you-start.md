---
sidebar_order: 1
---

# Before you start

## What you need

- **Talos.** Every node you adopt runs Talos v1.14 or later, and the version
  contract of the `TalosCluster` adopt creates is v1.14 or later. Sign-in cannot
  be turned off, and only v1.14 can carry Ichor's issuer.

  ![How the first control plane's Talos version decides the step. v1.14 or later: adopt it, with --version-contract v1.14 when an older contract is probed. Below v1.14: upgrade it first, outside Ichor, then adopt again. Adding a node with --add-machine to an adopted cluster needs v1.12 or later.](../../images/adopt-talos-version.light.drawio.svg)

  *The contract is probed on the first control plane. A node that differs shows up in its own plan.*

  > [!WARNING]
  > When the cluster runs a Talos below v1.14, upgrade it first, outside Ichor,
  > with `talosctl upgrade` or your usual tool. `ichor upgrade talos` works only
  > on a cluster Ichor already manages, and there is no `TalosCluster` yet. Then
  > adopt again.

  A node you add with `--add-machine` to a cluster that was adopted before needs
  only Talos v1.12 or later: the cluster's sign-in and issuer exist already, and
  `--add-machine` sets up neither (it takes none of the sign-in flags), so the
  reason for v1.14 does not apply to it. The contract is probed from the running
  version down to v1.12, so a configuration generated for an older contract is
  refused by name, and `--version-contract` names one instead. Either refusal is
  made before anything is written.

- **Credentials.** Your own `talosconfig` for the cluster (`--talosconfig`,
  `$TALOSCONFIG` or `~/.talos/config`) and a kubeconfig for its Kubernetes API
  (`--kubeconfig`, `$KUBECONFIG` or `~/.kube/config`). Adopt reads nodes with the
  talosconfig and never sends it to the controller.
- **The secrets bundle**, either as a file or recovered from a control plane
  (below).
- **The namespace.** `--namespace` (`-n`, default `ichor-system`) can only be the
  namespace Ichor's controller runs in, because the controller reads the secrets
  bundle Secret from its own namespace. Adopt checks this first, before it reads
  or writes anything, and refuses any other value naming the right one.

  > [!NOTE]
  > When adopt cannot find the controller's Deployment or Helm release (an
  > operator who may not list them cluster-wide), it cannot confirm the namespace:
  > the default goes ahead and any other value is refused with "could not
  > confirm". `--add-machine` does not make this check, so give it the
  > controller's namespace yourself.

Adopt never applies anything to a node itself. The controller does, in the
last phase, and only after a zero-drift check.

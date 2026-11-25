---
sidebar_order: 1
---

# Before you start

![How the first control plane's Talos version decides the step. v1.14 or later: adopt it, with --version-contract v1.14 when an older contract is probed. Below v1.14: upgrade it first, outside Ichor, then adopt again. Adding a node with --add-machine to an adopted cluster needs v1.12 or later.](../../images/adopt-talos-version.light.drawio.svg)

*The contract is probed on the first control plane. A node that differs shows up in its own plan.*

## What you need

- **Talos v1.14 or later** on every node, and a version contract of v1.14 or
  later on the `TalosCluster` adopt creates. Sign-in cannot be turned off, and
  only v1.14 can carry Ichor's issuer.

  > [!WARNING]
  > Below v1.14, upgrade first, outside Ichor, with `talosctl upgrade` or your
  > usual tool: `ichor upgrade talos` works only on a cluster Ichor manages.
  > Then adopt again.

  A node added with `--add-machine` to an already adopted cluster needs only
  v1.12 or later: sign-in and the issuer exist already and `--add-machine`
  takes none of the sign-in flags. The contract is probed from the running
  version down to v1.12; an older one is refused by name (`--version-contract`
  names one instead). Either refusal comes before anything is written.
- **Credentials.** Your `talosconfig` (`--talosconfig`, `$TALOSCONFIG` or
  `~/.talos/config`) and a kubeconfig (`--kubeconfig`, `$KUBECONFIG` or
  `~/.kube/config`). Adopt reads nodes with the talosconfig and never sends it to
  the controller.
- **The secrets bundle**, as a file or recovered from a control plane (see
  [Plan and the secrets bundle](plan-and-secrets.md)).
- **The namespace.** Adopt uses the namespace the controller runs in, because the
  controller reads the bundle Secret from its own namespace; you do not pass one.
  An old `--namespace` (`-n`) still works when it names that namespace; any other
  value is refused before adopt reads or writes anything. `--add-machine` makes no
  check: the `TalosCluster` is looked up in that namespace, and the bundle Secret is
  read from the namespace the controller runs in (found automatically), which needs
  the Role `ichor-bundle-secret-role` bound to you with a RoleBinding there.

Adopt never applies anything to a node itself. The controller does, in the last
phase, after a zero-drift check.

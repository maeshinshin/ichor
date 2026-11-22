---
sidebar_order: 7
---

# When Ichor was installed with Helm

![How adopt treats Ichor. With no Helm release of the chart ichor and no chart labels on the controller Deployment, adopt installs Ichor, or fails with --require-ichor. With one, adopt says Ichor is installed by Helm and does not touch it: a version that differs from the CLI is refused with the helm upgrade command, and Cilium policies, ichor-ui and sign-in are left to the chart values.](../../images/adopt-with-helm-decision.light.drawio.svg)

*A Helm-installed Ichor is never modified by adopt.*

With a Helm release of the chart `ichor` (or the chart's labels on the
controller Deployment), adopt skips its install step and creates no
`IchorInstallation`.

- Ichor's version must equal the CLI's exactly; otherwise adopt refuses and
  prints the `helm upgrade` command.
- Ichor's Cilium network policies are not applied. If they apply to your
  cluster, set `networkPolicy.cilium.enabled=true` and run `helm upgrade`.
- ichor-ui stays as the chart has it and sign-in is not turned on. Set
  `ichorUI.users.enabled=true` and `ichorUI.users.externalURL` in the chart
  values (the plan says so).
- `--require-ichor` makes adopt fail when Ichor is not installed instead of
  installing it.

See [Install Ichor with Helm](../install-with-helm.md).

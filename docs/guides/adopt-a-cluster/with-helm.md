---
sidebar_order: 7
---

# When Ichor was installed with Helm

If a Helm release of the chart `ichor` is deployed (or the controller
Deployment carries the chart's labels), adopt skips its install step, creates
no `IchorInstallation`, and the plan says "Ichor is installed by Helm; not
touching it".

- Ichor's version must equal the CLI's exactly. On a mismatch adopt refuses and
  prints the `helm upgrade` command.
- Adopt does not apply Ichor's Cilium network policies on a Helm install. If
  they apply to your cluster it tells you to set
  `networkPolicy.cilium.enabled=true` and run `helm upgrade`.
- Adopt leaves ichor-ui as the chart has it and does not turn sign-in on; set
  `ichorUI.users.enabled=true` and `ichorUI.users.externalURL` in the chart's
  values (adopt says so in the plan).
- `--require-ichor` makes adopt fail when Ichor is not installed, instead of
  installing it.

See [Install Ichor with Helm](../install-with-helm.md).

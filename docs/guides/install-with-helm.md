# Install Ichor with Helm

This guide installs Ichor with `helm install`, then registers an existing Talos cluster with `ichor adopt`.

The chart installs Ichor, not a cluster: CRDs (kept on uninstall), the controller and its RBAC, the webhooks, the UI and the optional Cilium policies. It does not ship Cilium. Registering a cluster needs your talosconfig and secrets bundle, so that stays with `ichor adopt`.

![How a Helm install becomes a managed cluster. If there is no CNI yet, ichor bootstrap cni installs Cilium first. The chart installs Ichor. ichor-ui refuses requests until an admin exists, so you create the first admin with --bootstrap. ichor adopt then registers the cluster with your talosconfig and secrets bundle, and it is managed by Ichor. Upgrades use helm upgrade, because ichor upgrade refuses on a Helm install.](../images/install-with-helm-path.light.drawio.svg)

*The chart installs Ichor but registers no cluster. `ichor adopt` does that, once the first admin exists.*

Until `ichor adopt` runs there is no `TalosCluster`: the controller reads no credential, contacts no node and writes nothing to one, and ichor-ui shows no cluster. `ichor adopt` without the secrets bundle refuses before it writes anything.

## The pages

1. [What the install needs](install-with-helm/requirements.md): Kubernetes and pod network requirements, and what the chart never takes.
2. [Install the chart](install-with-helm/install.md): the chart's source, the install command and its values.
3. [Network policies](install-with-helm/network-policies.md): on a Cilium cluster, default-deny in both directions with one allow per flow, and letting Prometheus scrape the controller.
4. [Verify a release](install-with-helm/verify.md): cosign signatures and the provenance attestation.
5. [The installer image on an upgrade](install-with-helm/installer-image.md): `ichor upgrade ichor` runs its Job only from a verified image or an explicit skip; an unverified one pauses with `InstallerImageUnverified`.
6. [Hardening the install](install-with-helm/hardening-the-install.md): Pod Security, priority, replicas, the release gate.
7. [ichor-ui is HTTPS only](install-with-helm/ichor-ui-https.md): the certificate, and moving an existing release.
8. [A cluster with no CNI](install-with-helm/no-cni.md): bootstrapping Cilium before the chart.
9. [Upgrade and uninstall](install-with-helm/upgrade.md): `helm upgrade`, and what `helm uninstall` keeps.

## The installer image on an upgrade

`ichor upgrade ichor` runs its Job only from a verified image or an explicit skip; an unverified one pauses with `InstallerImageUnverified`. See [the installer image](install-with-helm/installer-image.md).

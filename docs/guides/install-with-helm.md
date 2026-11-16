# Install Ichor with Helm

This guide installs Ichor itself with `helm install` and then registers an
existing Talos cluster with `ichor adopt`.

The chart installs Ichor, not a cluster: CRDs (kept on uninstall), the
controller and its RBAC, the webhooks, the UI and the optional Cilium policies.
Registering a cluster needs your talosconfig and secrets bundle, so that stays
with `ichor adopt`. The chart does not ship Cilium.

## How the install goes

![How a Helm install becomes a managed cluster. If there is no CNI yet, ichor bootstrap cni installs Cilium first. The chart installs Ichor. ichor-ui refuses requests until an admin exists, so you create the first admin with --bootstrap. ichor adopt then registers the cluster with your talosconfig and secrets bundle, and it is managed by Ichor. Upgrades use helm upgrade, because ichor upgrade refuses on a Helm install.](../images/install-with-helm-path.light.drawio.svg)

*The chart installs Ichor but registers no cluster. `ichor adopt` does that, once the first admin exists.*

Until you run `ichor adopt` no cluster is registered with Ichor. The
controller runs, but it reacts only to its own custom resources, and there is
no `TalosCluster` yet, so it reads no credential, contacts no node and writes
nothing to one. ichor-ui is up and shows no cluster. The same holds if you run
`ichor adopt` without the secrets bundle: adopt refuses before it writes
anything, so Ichor stays installed but unable to manage the nodes.

## The pages

Read them in order, or go to the one you need.

1. [What the install needs](install-with-helm/requirements.md): the Kubernetes and pod network requirements, and what the chart never takes.
2. [Install the chart](install-with-helm/install.md): where the chart comes from, the install command and its values.
3. [Network policies](install-with-helm/network-policies.md): the default-deny policies, and letting Prometheus scrape the controller.
4. [Verify a release](install-with-helm/verify.md): the cosign signatures and the provenance attestation.
5. [The installer image on an upgrade](install-with-helm/installer-image.md): how the installer image's signature is checked, and the skip flag.
6. [Hardening the install](install-with-helm/hardening-the-install.md): Pod Security, priority, replicas and the release gate.
7. [ichor-ui is HTTPS only](install-with-helm/ichor-ui-https.md): the certificate, and moving an existing release.
8. [A cluster with no CNI](install-with-helm/no-cni.md): bootstrapping Cilium before the chart.
9. [Upgrade and uninstall](install-with-helm/upgrade.md): `helm upgrade`, and what `helm uninstall` keeps.

## Network policies

On a cluster whose CNI is Cilium, the chart renders Ichor's network policies: default-deny in both directions, with one allow per flow. [Network policies](install-with-helm/network-policies.md) names each of them, and how a Prometheus scrapes the controller.

## The installer image on an upgrade

`ichor upgrade ichor` runs its installer Job only from an image whose signature was verified, or one you explicitly skipped. [The installer image on an upgrade](install-with-helm/installer-image.md) says how the check works.

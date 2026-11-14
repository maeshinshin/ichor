---
sidebar_order: 4
---

# Deployment

The default is self-managed: Ichor runs inside the cluster it manages. The
controller and UI run with two replicas, a PodDisruptionBudget and anti-affinity,
except on clusters with a single schedulable node, where the installer drops to
one replica and no PDB (otherwise a drain never finishes).

Ichor's own users and login are on by default for `ichor init`, with the
issuer `https://localhost:8443` unless `--ui-url` gives another (`--allow-unauthenticated`
turns them off). The Helm chart keeps them off. Turning them on in the chart
needs three things a default install lacks: an https URL that browsers and the
CLI can reach (`ichorUI.users.externalURL`, the value `ichor init --ui-url` sets), a
Talos v1.14 contract or later (earlier contracts can carry only one issuer),
and an `ichor rollout config` rollout of the control planes. ichor-ui is the
issuer. The kube-apiserver reads discovery and keys from a ClusterIP listener
on port 8444, signed by an Ichor-made CA, so the external URL does not have to
be reachable from the control planes. Ichor installs no ingress. Expose
ichor-ui with an Ingress, a Gateway or a LoadBalancer. ichor-ui is HTTPS only on
8443, so an edge re-encrypts to it and verifies the backend against
`ichor-ui-ca`, or a plain L4 load balancer passes through the certificate the
operator supplied with `ichorUI.tls.secretName`.

A Helm install gets an `IchorInstallation` created once by the
controller with `spec.deployment.manager: helm`; Helm then owns upgrades and
`ichor upgrade ichor` refuses.

Network policy. Cilium is guaranteed, so
`ichor-system` gets a `CiliumNetworkPolicy` default
deny plus one allow per flow, using Cilium's entities (`kube-apiserver`, `host`,
`remote-node`, `world`) for peers that run on the host network. They cannot ship
in `dist/install.yaml` because Cilium's CRD does not exist yet, so `ichor init`
applies Ichor's after Cilium is Ready, the installer Job re-applies them on
every upgrade when the CRD exists, and the chart renders them behind
`networkPolicy.cilium.enabled`. The webhook and OIDC ingress rules also admit `remote-node`,
because the apiserver's calls to a pod on another node arrive from the
control plane's `cilium_host` address. No policy allows
`world` egress: what lies outside the cluster (Talos nodes, S3 stores, registries,
ichor-ui's OIDC issuer) is allowed by policies the controller writes from the
objects and the Deployment that name it, with `toFQDNs` for names, and the controller's metrics port is open to a namespace
only when the operator names one.

A host-network kube-apiserver reaches a
Service ClusterIP; Talos applies the issuer document without a reboot but the
kube-apiserver restarts (15 to 45 s without `/readyz` per node), which is why
the rollout is one control plane at a time. Talos does not validate
the document, so Ichor validates it before rendering.

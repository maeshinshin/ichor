---
sidebar_group: "Running and interfaces"
sidebar_order: 4
---

# Deployment

![Ichor runs self-managed in ichor-system: the controller and ichor-ui with a network policy, and a ClusterIP listener on 8444 that the kube-apiserver reads for discovery and keys. An Ingress, Gateway or LoadBalancer you provide fronts ichor-ui. Install paths differ in how users and login are switched on.](../images/deployment.light.drawio.svg)

*Ichor runs inside the cluster it manages.*

## Replicas

The controller and UI run two replicas with a PodDisruptionBudget and anti-affinity. On a single schedulable node the installer drops to one replica and no PDB, or a drain never finishes.

## Users and login

Always on: ichor-ui makes no change without a sign-in and does not start without one. The Helm chart has no default and refuses an ichor-ui with neither `ichorUI.users.enabled` nor `ichorUI.oidc.enabled`.

| Install | Issuer |
|---|---|
| `ichor init` | `https://localhost:8443`, or `--ui-url` |
| `ichor adopt` | the same, for the Ichor it installs |
| Helm | `ichorUI.users.externalURL`, an https URL browsers and the CLI reach (the value `ichor init --ui-url` sets) |

Turning users on in the chart also needs a Talos v1.14 contract or later (earlier ones carry one issuer) and an `ichor rollout config` of the control planes. ichor-ui is the issuer; the kube-apiserver reads discovery and keys from the ClusterIP listener on 8444, signed by an Ichor-made CA, so the external URL need not be reachable from the control planes. The issuer document applies without a reboot but restarts the kube-apiserver (15 to 45 s without `/readyz` per node), hence one control plane at a time. Talos does not validate the document, so Ichor does before rendering.

Ichor installs no ingress. ichor-ui is HTTPS only on 8443: an edge re-encrypts to it and verifies the backend against `ichor-ui-ca`, or a plain L4 load balancer passes through the certificate supplied in `ichorUI.tls.secretName`.

A Helm install gets an `IchorInstallation` created once by the controller with `spec.deployment.manager: helm`; Helm then owns upgrades and `ichor upgrade ichor` refuses. The chart's `bundleSecret` value names the secrets-bundle Secrets that the Role `ichor-bundle-secret-role` may get, and nothing else; empty installs no Role.

## Network policy

Cilium is guaranteed, so `ichor-system` gets a `CiliumNetworkPolicy` default deny plus one allow per flow, using Cilium's entities (`kube-apiserver`, `host`, `remote-node`, `world`) for host-network peers.

- They cannot ship in `dist/install.yaml` (Cilium's CRD does not exist yet). `ichor init` applies them after Cilium is Ready, the installer Job re-applies them on every upgrade when the CRD exists, and the chart renders them behind `networkPolicy.cilium.enabled`.
- The webhook and OIDC ingress rules also admit `remote-node`: the apiserver's calls to a pod on another node arrive from the control plane's `cilium_host` address.
- No policy allows `world` egress. What lies outside the cluster (Talos nodes, S3 stores, registries, the OIDC issuer) is allowed by policies the controller writes from the objects and the Deployment that name it, with `toFQDNs` for names.
- The controller's metrics port is open to a namespace only when the operator names one.

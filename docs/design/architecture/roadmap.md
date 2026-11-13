---
sidebar_order: 22
---

# Roadmap

1. CRDs and the kubebuilder project, Helm chart and Kustomize base, deletion
   protection, e2e scaffolding.
2. Typed config (the mirror-type generator first), rendering and apply, drift,
   the cluster lock.
3. Discovery, approval, disk roles, removal, topology. The deletion guard
   phase 1 ships only checks for the `allow-delete` annotation; the quorum and
   last-control-plane rules land here, and an explicit test has to show that
   the annotation alone does **not** let the last control plane go.
4. `init` and `adopt`, Cilium.
5. Read-only UI, approval UI, backup and restore, client certificate renewal.
6. Config editing UI, `TalosOperation`, Talos and Kubernetes upgrades,
   Ichor self-upgrade.
7. Storage integration (longhorn-v1, operation hooks), CA rotation.
8. Override.
9. Monitoring settings, metrics and dashboards, optional components, OIDC and impersonation, Ichor's own users and issuer.
10. UI localisation.
11. Later: more storage presets, IPv6, replica-based
    scaling.

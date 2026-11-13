# Ichor architecture

Status: living document; Ichor is alpha. This document and the pages under it are the
source of truth for the project.

## Summary

Ichor manages Talos Linux clusters through Kubernetes custom resources. A
controller renders machine configuration from typed resources and applies it
to nodes, walks upgrades through the cluster, backs up etcd, and handles the
lifecycle of nodes from maintenance mode to removal. A CLI and a web UI expose
the same operations.

Ichor stops at the Talos layer, with one component it installs itself: Cilium is the only CNI and Ichor's own Helm release. Storage systems,
monitoring and any GitOps tool are the user's business; Ichor only prepares the
Talos side for them and coordinates with them around node operations.


## The parts

Each page below is one section of the design, in reading order.

1. [Motivation](architecture/motivation.md): why Ichor exists, its goals and what it leaves alone.
2. [Overview](architecture/overview.md): the three deliverables and how data flows between them.
3. [Trust](architecture/trust.md): the secrets bundle, who holds which credential, sign-in and roles.
4. [Deployment](architecture/deployment.md): where Ichor runs, how its UI is reached and its network policies.
5. [Principles](architecture/principles.md): the rules the rest follows.
6. [API](architecture/api.md): the custom resources, with
   [Typed configuration](architecture/typed-configuration.md),
   [Patch layers](architecture/patch-layers.md) and
   [Validation](architecture/validation.md).
7. The design, one page per part:
   [Bootstrap](architecture/bootstrap.md),
   [Cilium](architecture/cilium.md),
   [Git, override and recovery](architecture/git-override-and-recovery.md),
   [Nodes](architecture/nodes.md),
   [CNI](architecture/cni.md),
   [Storage](architecture/storage.md),
   [Configuration reconcile](architecture/configuration-reconcile.md),
   [Upgrades](architecture/upgrades.md),
   [Backup, recovery, certificates](architecture/backup-recovery-certificates.md) and
   [Monitoring and registry mirrors](architecture/monitoring-and-registry-mirrors.md).
8. [CLI and UI](architecture/cli-and-ui.md), [Testing](architecture/testing.md),
   [Roadmap](architecture/roadmap.md) and
   [Alternatives considered](architecture/alternatives-considered.md).

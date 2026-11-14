---
sidebar_group: "Purpose"
sidebar_order: 2
---

# Overview

![Architecture of Ichor. The browser, the ichor CLI and kubectl reach the Kubernetes API of the cluster that Ichor manages. Inside that cluster, in the ichor-system namespace, run ichor-ui, the ichor-controller with its admission webhooks, and the Jobs (ichor-installer installs and upgrades Ichor, ichor-cni runs Helm for Cilium); they read and write Ichor's custom resources through the API. The API server holds Ichor's custom resources, Secrets and Leases. Only the controller configures the Talos machines, over the Talos API; a new machine waits in maintenance mode until you approve it, and then becomes a Talos node, a control plane or a worker.](../../images/architecture.light.drawio.svg)

*Ichor runs inside the cluster it manages; ichor-ui reads nodes and never writes to them, and the controller is the only writer.*

Three deliverables come out of one repository:

- `ichor`, the CLI. Bootstraps the first node, adopts existing clusters, and
  offers every operation the UI has. It is also the recovery path that does not
  depend on a running controller.
- `ichor-controller` (plus `ichor-installer`, a Job image). The controller is
  the only component that writes to Talos nodes. Two replicas, leader election.
- `ichor-ui`, a Go backend that embeds the Preact SPA. It reads and writes CRs,
  reads logs and state from the Talos API, and never writes to nodes.

`api/` (CRD types) and `proto/` (Connect definitions) are the shared contracts.
Deliverables don't import each other; shared code lives under `pkg/`. The
controller's entry point stays at kubebuilder's `cmd/main.go`, which the
generated Makefile is tied to; the CLI and the UI backend are
added beside it. The Kustomize base under `config/` is the source of truth for
manifests, and the Helm chart in `dist/chart` is generated from it.

## Data flow

Every write, from the UI, the CLI or a user's own tooling, is a change to a custom
resource. The controller is the only thing that turns those into Talos API
calls. This keeps validation, RBAC and audit identical regardless of how a
change arrived. Reads come from two places: CR status (via the API
server) and raw node state and logs (via the Talos API, read-only).

Long-running work — upgrades, hand-overs, topology changes — keeps its state
machine in CR status, not in memory. A controller restart or a leader change
resumes from where it left off.

---
sidebar_group: "Running and interfaces"
sidebar_order: 20
---

# CLI and UI

The rule is parity. Both write CRs; both share the same Go packages for
rendering, planning (diff, impact, pre-checks, upgrade paths) and
dangerous-operation checks; both use the same Connect API, the CLI falling
back to local computation when it can't reach the backend. The CLI prints for
people by default (tables and aligned blocks) and offers
`-o yaml|json` for scripts, `--follow`/`--watch`, and interactive confirmation
with `--yes`. The exceptions are `init`, `adopt` and full-loss recovery, which
predate the cluster.

The UI is a Vite + Preact + TypeScript SPA built with preactcn, embedded in
the Go backend. The backend talks to the API server, read-only to the Talos
API, and optionally to Prometheus for graphs; the
browser talks only to the backend. It survives drains: stateless, two replicas
where possible, graceful termination that asks clients to reconnect, and
streams that resume from a token (resourceVersion, last log timestamp,
progress sequence). `ichor ui` re-establishes its port-forward to another pod
when the current one goes away.

The UI is built for one cluster: there is no cluster list, and a top
bar shows the cluster's name, its Talos, Kubernetes and Ichor versions and a
health badge. Screens: an Overview, built from stock preactcn components, with
tiles for nodes (and the nodes waiting for approval), the running operation,
backups and versions, a needs-attention list, recent operations, and a
collapsible cluster details section (a config rollout waiting for a reboot shows in the
needs-attention list with its own Apply action), with Add node and cluster
actions (rotating the CAs, shutting down) in the header; a Nodes list and an add-node
(approval) page; a node page with tabs for Summary, Storage & network, Talos,
Kubernetes & etcd, Configuration and Logs; Operations, with a detail page per
operation (per-machine state, pause, resume, and the CA-rotation credentials
hand-over); Backups, with each `TalosEtcdBackup`, its snapshots,
Back up now and restore; Configuration, for editing patches by layer with an
effective-config view annotated by origin and secrets masked, impact preview
before applying, and schema-driven forms; Cilium, with the effective values
read-only, a YAML editor for the user layer, validation and a diff preview, and
an Upgrade card when the embedded chart is newer; Users, only when Ichor's own users are on. A merged multi-node log
viewer is planned.

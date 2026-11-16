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
with `--yes`. The CLI never writes to a Talos node, with three exceptions:
`init` and full-loss recovery (`restore cluster`), which predate the cluster
and so have no controller to act for them, and `reclaim node --talosconfig`,
which acts with the operator's own credential, which Ichor never stores.
`adopt` only reads from nodes.

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
before applying, and a field search (below); Cilium, with the effective values
read-only, a YAML editor for the user layer, validation and a diff preview, and
an Upgrade card when the embedded chart is newer; Users, only when Ichor's own users are on. A merged multi-node log
viewer is planned.

### Searching config fields

A patch is edited by searching for a field, not by choosing a document kind
first. The Configuration page's Search fields button opens a command-palette
dialog over every field of every Talos document kind Ichor accepts. A row shows
the field's path, its kind, its type and Talos's own description. Choosing a row
puts the field in the patch: in the patch's document of that kind, or in a new
one that also shows an input for each field the kind requires. A field is edited
with the control its type calls for: an `Input` for a string or a number, a
`Switch` for a boolean, a `Select` where Talos lists the allowed values, and a
`Textarea` of JSON for an object, an array or a map. A kind's own row adds a
document, so a patch can hold two of one kind. The JSON `Textarea` of the whole
document stays below the chosen fields and edits the same text, and the kind
`Select` still works.

The rows come from an index generated at build time, `ui/src/gen/fieldindex.json`,
which ships as its own chunk of the UI bundle: the browser reads no schema and
ichor-ui's role gains no permission. `make ui-fieldindex` writes it, and the UI
targets of the Makefile run that first; it is not committed. It is built from the
`TalosConfigPatch` CRD schema (paths, types, which fields are required) and from
the Talos machinery documentation tables (each field's description, and the list
of allowed values of a string field), because the CRD schema carries neither
for most fields. The API server stays the only validator: a value that does not
fit is refused when the patch is saved, with the server's own message.
`KubeStaticPodConfig` has one row, its own, and none for the 1,075 fields of the
Kubernetes `Pod` inside it. See ADR 0039's amendment.

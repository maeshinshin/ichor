---
sidebar_group: "Running and interfaces"
sidebar_order: 20
---

# CLI and UI

![The CLI and the UI have the same reach. The browser talks only to the ichor-ui backend, and the CLI talks to the same Connect API or, when it cannot reach it, computes locally with the same shared Go packages. Both write Ichor's custom resources through the Kubernetes API, and the controller is the only writer to Talos nodes. ichor-ui reads nodes read-only; the CLI writes to a node itself only for init, restore cluster and reclaim node with --talosconfig.](../../images/cli-and-ui.light.drawio.svg)

*One rule, parity: both write CRs, share the same packages and the same API; only three CLI commands touch a node.*

## The CLI

- Parity with the UI: both write CRs, share the Go packages for rendering,
  planning (diff, impact, pre-checks, upgrade paths) and dangerous-operation
  checks, and use the same Connect API. The CLI computes locally when it cannot
  reach the backend (`--local`).
- It prints for people by default (tables, aligned blocks), with `-o yaml|json`
  for scripts, `--follow`/`--watch`, and interactive confirmation skipped by
  `--yes`.
- It never writes to a Talos node, except `init` and full-loss recovery
  (`restore cluster`), which predate the cluster and so have no controller, and
  `reclaim node --talosconfig`, which acts with the operator's own credential
  that Ichor never stores. `adopt` only reads from nodes.
- `ichor ui` opens a port-forward to ichor-ui and re-establishes it on another
  pod when the current one goes away.

## The UI

A Vite + Preact + TypeScript SPA built with preactcn, embedded in the Go backend.
The backend talks to the API server and, read-only, to the Talos API; the browser
talks only to the backend. It survives drains: stateless, two replicas where
possible, graceful termination that asks clients to reconnect, and streams that
resume from a token (resourceVersion, last log timestamp, progress sequence).

It is built for one cluster: no cluster list, and a top bar with the cluster's
name, its Talos, Kubernetes and Ichor versions and a health badge.

| Screen | Holds |
|---|---|
| Overview | Stock preactcn tiles: nodes (and those awaiting approval), the running operation, backups and versions; a needs-attention list (a config rollout waiting for a reboot has its own Apply action); recent operations; collapsible cluster details, with the containerd device-ownership switch. Header actions: Add node, rotate the CAs, shut down. |
| Nodes, Add node | The node list and the approval page. |
| Node page | Tabs: Summary, Storage & network, Talos, Kubernetes & etcd, Configuration, Logs. |
| Operations | A detail page per operation: per-machine state, pause, resume, approve (for one that waits for approval), and the CA-rotation credentials hand-over. |
| Backups | Each `TalosEtcdBackup`, its snapshots, Back up now, restore. |
| Configuration | Patches by layer, an effective-config view annotated by origin with secrets masked, an impact preview before applying, and the field search. |
| Cilium | Effective values read-only, a YAML editor for the user layer with validation and a diff preview, and an Upgrade card when the embedded chart is newer. |
| Storage, Settings | `TalosStorage` and the `IchorInstallation` settings. |
| Users | Only when Ichor's own users are on. |

A merged multi-node log viewer is planned.

## Searching config fields

A patch is edited by searching for a field, not by picking a document kind
first. The Configuration page's Search fields button opens a command-palette
dialog over every field of every Talos document kind Ichor accepts; a row shows
the field's path, kind, type and Talos's description. Choosing one puts the field
in the patch's document of that kind, or in a new one that also shows an input
for each field the kind requires. A kind's own row adds a document, so a patch
can hold two of one kind.

| Field type | Control |
|---|---|
| string, number | `Input` |
| boolean | `Switch` |
| Talos lists the allowed values | `Select` |
| object, array, map | `Textarea` of JSON |

The JSON `Textarea` of the whole document stays below the chosen fields and edits
the same text, and the kind `Select` still works.

The rows come from `ui/src/gen/fieldindex.json`, generated at build time and
shipped as its own chunk of the UI bundle, so the browser reads no schema and
ichor-ui's role gains no permission. `make ui-fieldindex` writes it (the UI
targets run it first); it is not committed. It is built from the
`TalosConfigPatch` CRD schema (paths, types, required) and the Talos machinery
documentation tables (descriptions, allowed values), which the CRD schema mostly
lacks. The API server stays the only validator: a value that does not fit is
refused at save with the server's own message. `KubeStaticPodConfig` has one row,
its own, and none for the 1,075 fields of the Kubernetes `Pod` inside it (ADR
0039's amendment).

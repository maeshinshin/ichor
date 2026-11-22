---
sidebar_group: "Running and interfaces"
sidebar_order: 20
---

# CLI and UI

![The CLI and the UI have the same reach. The browser talks only to the ichor-ui backend, and the CLI talks to the same Connect API or, when it cannot reach it, computes locally with the same shared Go packages. Both write Ichor's custom resources through the Kubernetes API, and the controller is the only writer to Talos nodes. ichor-ui reads nodes read-only; the CLI writes to a node itself only for init, restore cluster and reclaim node with --talosconfig.](../../images/cli-and-ui.light.drawio.svg)

*One rule, parity: both write CRs, share the same packages and the same API; only three CLI commands touch a node.*

## The CLI

- Same CRs, Go packages (render, plan, diff, impact, pre-checks, upgrade paths,
  dangerous-operation checks) and Connect API as the UI; it computes locally when
  the backend is unreachable (`--local`).
- Prints for people; `-o yaml|json` for scripts, `--follow`/`--watch`, `--yes`
  skips confirmation.
- Writes to a Talos node only for `init`, `restore cluster` (both predate the
  controller) and `reclaim node --talosconfig` (the operator's own credential,
  never stored). `adopt` only reads.
- `ichor ui` port-forwards to ichor-ui and moves to another pod when its pod goes.

## The UI

A Vite + Preact + TypeScript SPA built with preactcn, embedded in the Go
backend, which talks to the API server and, read-only, the Talos API; the browser
talks only to the backend. Stateless, two replicas where possible, graceful
termination asks clients to reconnect, streams resume from a token
(resourceVersion, last log timestamp, progress sequence). One cluster: no
cluster list; a top bar shows its name, Talos, Kubernetes and Ichor versions and
a health badge.

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

The Configuration page's Search fields button opens a command-palette dialog over
every field of every Talos document kind Ichor accepts (path, kind, type, Talos's
description). Choosing one puts it in the patch's document of that kind, or a new
one that also asks for the kind's required fields. A kind's own row adds a
document, so a patch can hold two of one kind. The whole-document JSON `Textarea`
and the kind `Select` still work.

| Field type | Control |
|---|---|
| string, number | `Input` |
| boolean | `Switch` |
| Talos lists the allowed values | `Select` |
| object, array, map | `Textarea` of JSON |

The rows come from `ui/src/gen/fieldindex.json`, built by `make ui-fieldindex`
(the UI targets run it; not committed) from the `TalosConfigPatch` CRD schema and
the Talos machinery documentation tables, and shipped as its own chunk: the
browser reads no schema and ichor-ui gains no permission. The API server stays
the only validator and refuses a bad value at save. `KubeStaticPodConfig` has two
rows (the collection and its key), none for the 1,000-odd fields of the Kubernetes
`Pod` inside it (ADR 0039).

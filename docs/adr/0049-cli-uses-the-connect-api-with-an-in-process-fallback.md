# 0049. The CLI calls the Connect API through ichor ui's tunnel and falls back to the same handlers in-process

Status: Accepted · Date: 2026-10-26 · Design section: CLI and UI

Decided: `ichor machine|cluster|patch|node|config` call ichor-ui's Connect API over the `ichor ui` port-forward discovery, and when ichor-ui is unreachable run the very same `uiapi.Server` in-process on the user's Kubernetes credentials, so no check is duplicated or skipped

## Context

architecture.md ("CLI and UI", principle 8) says the CLI and UI "use the same
Connect API, and the CLI falls back to local computation when the backend is
unreachable". Until now `internal/cli` never called it, and there was no CLI
for approval, patches, effective config, node detail or logs.

## Decision 1: reach the backend with `ichor ui`'s machinery

`dialBackend` (`internal/cli/api.go`) reuses ADR 0030 verbatim: read the
`ichor-ui-service` Service, `waitForReadyPod`, `portForwardToPod`, on a free
local port, one `GET /` probe, then the generated Connect clients
(`connect.NewXServiceClient`, plain `http.DefaultClient`; unary and server
streaming work over HTTP/1.1). No new connection method. Each command opens
its own short-lived tunnel; only `logs node --follow` reconnects.

## Decision 2: the fallback is the same handler, not a copy

`uiapi.Server` already is the implementation of the Connect service
interfaces, over a `client.Client`; its methods have exactly the generated
clients' signatures. The fallback is `&uiapi.Server{Client: <CLI's kube
client>, ControllerNamespace: <ichor namespace>, Actor: "ichor-cli"}` used as
the client. So `ApproveMachine`'s checks (strong identity, role required, not
already approved), strict field validation on every write, `PreviewImpact`,
and `GetEffectiveConfig`'s secret masking (ADR 0018) run the same code on
both paths. There is nothing to keep in sync and no way to build a path that
skips a check. `uiapi.Server.Actor` (new, default `ichor-ui`) lets the
audit record say which path wrote the CR.

The fallback is chosen once, up front, only when ichor-ui cannot be reached
(no Service, no ready pod, tunnel failure, no HTTP answer), printed to
stderr. An error the backend *returns* (a refused approval, a rejected
patch) is the answer: it is never retried through the other path, and a write
is never re-sent after it may have reached the backend. `--local` forces the
fallback.

| Command | Backend reachable | Backend unreachable |
|---|---|---|
| `get nodes/describe node`, `get clusters/describe cluster`, `get patches/describe patch` | Connect | in-process, reads the CRs |
| `approve node/reject node` | Connect | in-process, same handler, writes the same CR |
| `apply/delete patch` | Connect | in-process, same handler, writes the same CR |
| `preview` | Connect | in-process (`pkg/fieldmeta`, pure) |
| `describe config` | Connect | in-process: renders from the CRs and the bundle with the caller's RBAC, masked by the same code |
| `describe node`, `logs node` | Connect | **no fallback**: error. The read-only Talos identity lives in ichor-ui and the CLI never talks to a node |

`init`, `adopt` and full-loss recovery are unchanged: they exist before the
cluster.

## Decision 3: command shape

architecture.md fixes only `ichor approve node`. The rest follows it:
`ichor get nodes|describe node|approve node|reject node`, `get clusters|describe cluster`,
`get patches|describe patch|apply|delete patch|preview`, `describe node|logs node`,
`describe config`. Output is the wire message as `-o yaml|json`. `approve node`,
`reject node` and `delete patch` confirm unless `--yes`. `apply`
preview first and confirm only when the patch needs a reboot, touches an
install-time-only field or one too new for the cluster's Talos. Patches are
read from a typed `TalosConfigPatch` manifest (`-f`, `-` for stdin) decoded
with unknown fields rejected; `uiapi.PatchToWire` is the one conversion.

## Decision 4: `--follow`

`StreamNodeLogs` already takes and returns an opaque resume token. `logs node
--follow` reconnects (new tunnel) on `CodeUnavailable` or a clean end, passing
the latest token, the Go twin of `ui/src/lib/resumableStream.ts`; the same
caveat about a few repeated or missed lines applies (ADR 0018). The API has
no "snapshot" mode, so without `--follow` the command stops after
`--idle-timeout` (3s) without a new line. `shortcut:` the idle timer
stands in for a `follow` field on `StreamNodeLogsRequest`, which needs a `proto/`
change; add the field when the API next changes.

## Not done

- `--watch` on list/get commands (the API has no watch RPC; polling would be
  the CLI inventing semantics).
- Table output; everything is structured `-o yaml|json`.
- Per-user identity: both paths act as the backend/caller until OIDC and
  impersonation (roadmap phase 9).

## Amendment: Service discovery by label

The CLI finds the ichor-ui Service by `app.kubernetes.io/component=ui` in the Ichor namespace, not by name. Kustomize `namePrefix` makes the init-installed Service `ichor-ichor-ui-service`, the chart names it `ichor-ui-service`, and both now set the label. Names are left as they are because `internal/installer` classifies UI objects by the `ichor-ichor-ui` prefix.

- `apply` always print the impact (reboot, install-time-only, unsupported, or "no reboot needed") on stderr before writing, as the help says; the written object stays alone on stdout.

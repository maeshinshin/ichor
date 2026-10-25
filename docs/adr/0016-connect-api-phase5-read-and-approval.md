# 0016. Connect API: phase 5 read and approval surface

Status: Accepted · Date: 2026-10-25 · Design section: CLI and UI, roadmap item 5

Decided: Split the Connect read/approval API into `ClusterService`/`MachineService`/`NodeService` and fixed their message shapes

## Context

Phase 5 is "read-only UI, approval UI, backup and restore, client
certificate renewal" (roadmap item 5). This work covers only the read and
approval part: cluster and machine lists (including pending approvals),
approve/reject, node detail, and the merged log stream. Backup, restore and
certificate renewal are a separate unit and reserve no methods here, per
"the rule is parity ... both use the same Connect API" (CLI and UI) and the
instruction not to speculate ahead of what a phase-5 screen actually shows.

Three constraints come straight from the design text and had to be load-
bearing in the types, not left to the server implementation to remember:

- "secrets masked" in the effective-config view (CLI and UI) — the browser
  must never receive a secret and then hide it client-side.
- "streams that resume from a token (resourceVersion, last log timestamp,
  progress sequence)" (CLI and UI) — only the log stream exists in this
  phase, so only it needs a resume token here; the parenthetical names three
  different token shapes for three different kinds of stream, and "last log
  timestamp" is the one that applies.
- The browser "talks only to the backend" (CLI and UI); the backend is the
  only thing that talks to the Talos API, and read-only. No message in this
  API may be (or wrap) a raw Talos machinery/COSI type.

Versions consulted: `buf` 1.73.0, `protoc-gen-go` v1.36.12, and
`protoc-gen-connect-go` v1.21.0 — all three already pinned in `flake.nix` and
run through `nix develop`, not installed separately. Docs read: buf's v2
`buf.yaml`/`buf.gen.yaml` configuration reference and connectrpc.com's Go
getting-started guide (both at connectrpc.com/buf.build's current docs, no
specific version selector exposed there). The exact default lint/breaking
category names (`STANDARD`, `FILE`) and the fact that `STANDARD` excludes
`UNARY_RPC` and `COMMENTS` were confirmed against the installed `buf` binary
itself with `buf config init` and `buf config ls-lint-rules`, not taken on
faith from the docs, because the v1/v2 category names differ and only the
installed version's behavior is authoritative.

## Decision

### Service split

Three services, one per screen family, each in its own file under
`proto/ichor/v1alpha1/`, plus a `common.proto` for the handful of shared
types (`ObjectRef`, `Condition`, `AuditRecord`):

- `ClusterService` — `ListClusters`, `GetCluster`. One `Cluster` message
  serves both; the design doesn't describe a heavier cluster-detail view
  than what the list already shows (status, rollout progress, hand-over
  state), so there is no separate summary/detail pair to maintain.
- `MachineService` — `ListMachines` (pending machines included,
  unconditionally — the design shows them in the same list, not behind a
  flag), `GetMachine`, `ApproveMachine`, `RejectMachine`.
- `NodeService` — `GetNode` (hardware, disks, network, resource usage,
  Kubernetes status), `GetEffectiveConfig` (its own method, not a field on
  `GetNode`, because masking and origin annotation are enough shape to carry
  on their own and the node page can fetch it lazily), `StreamNodeLogs`.

`StreamNodeLogs` takes a `repeated ObjectRef machines` and serves both the
node page's single-node tail and the merged multi-node viewer from the same
RPC — the design describes them as the same mechanism at different
cardinality, not two features.

`ApproveMachine` sets exactly the fields `TalosMachineSpec` models today:
`role`, `node_labels`, `hostname`. The design's approval text also mentions
disk roles and a `LinkAliasConfig`-based NIC selection, but neither exists on
the CRD in this checkout (`TalosMachineSpec` has no `disks[]` field yet;
that's still phase-3 controller work in progress elsewhere). Modeling proto
fields for a CRD shape that doesn't exist yet would be exactly the kind of
speculative field the task says not to add. Those fields join
`ApproveMachineRequest` additively — new fields on an existing message are
not a breaking change — in the work unit that adds them to the CRD.

### Generated code location

`gen/go/`, a new top-level directory, sibling to `proto/`. Considered and
rejected: `api/gen/`, `pkg/connectapi/`, `internal/api/` — all three sit
inside directories phase 5's own instructions list as off-limits to this
unit of work (`api/`, `pkg/`, `internal/`) because other agents own them
concurrently. A new top-level directory needs no coordination with anyone
and the import path it produces
(`github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1`) is exactly as usable
from `internal/` or `cmd/` later as any of the alternatives would have been.
It "meshes with the kubebuilder layout" in the sense that matters here: it
doesn't collide with a directory kubebuilder or the Makefile already owns.

`go_package_prefix` is managed centrally in `buf.gen.yaml` rather than a
`go_package` option per `.proto` file, so the Go import path follows the
proto package path (`ichor/v1alpha1` → `gen/go/ichor/v1alpha1`) without
repeating it four times.

### Generated code is committed

The repository already commits `zz_generated.deepcopy.go`
(`api/v1alpha1/zz_generated.deepcopy.go`) rather than generating it on every
build. The same reasoning applies here: `go build ./...` and `go vet ./...`
must work on a plain checkout without requiring `buf` or `nix develop` first,
and later phases that write the server implementation need a stable,
reviewable diff to import against rather than a build-time dependency on the
proto toolchain being present. `make proto-generate` regenerates it in place
when `proto/` changes; CI can diff the regenerated tree against the
committed one to catch drift, the same way `dist/chart` (also committed, and
also regenerated by a Makefile target) is checked.

### Resume token shape

`resume_token` is an opaque `string`, chosen deliberately not to be a
structured message. The design's own wording — "last log timestamp" — says
what it encodes, but nothing requires the client to parse it, and giving it
structure would invite a client to depend on internals that are free to
change (which machine's timestamp wins a tie, whether a sequence number is
appended to break ties within the same timestamp). The server format,
recorded here so an implementation has one shape to target: a per-stream,
per-machine high-water mark, encoded as the last-seen log timestamp plus a
monotonic ordinal to break ties within the same timestamp, joined and
base64-encoded as one token covering every machine in the request. A client
resumes by sending back the latest token it received, verbatim.

### Secrets masking is a server responsibility, encoded in the type

`EffectiveConfigValue.secret` is a `bool` the server sets, and `value` is
documented (in the `.proto` comment, since that's the contract every
implementation reads) as a field the server must populate with a fixed mask
when `secret` is true — never with the real value first. There's no separate
"masked value" field alongside an "actual value" field: a second field would
be exactly the hole the requirement is closing, because nothing stops an
early implementation from filling both and shipping the real one to the
browser by accident. One field, one rule, stated in the message that crosses
the wire.

`ConfigOrigin` carries `layer` (the five patch layers from "Patch layers" —
`system`, `cluster`, `role`, `selector`, `machine`) and `source` (the
specific object, e.g. a `TalosConfigPatch` name, or `"system"`/`"cluster"`
for values Ichor generates rather than a patch), per "effective-config view
... annotated by origin."

### Approve/reject audit shape

`AuditRecord` (`common.proto`) is returned by both `ApproveMachineResponse`
and `RejectMachineResponse`: `actor` (from the caller's authenticated
identity — not a request field, so a client can't forge who performed the
action), `at` (timestamp), `action` (`"approve"`/`"reject"`), `target` (the
`ObjectRef` acted on), and `reason` (free text, used for rejections). That's
who, when, against what, stated once and reused rather than inventing a
separate ad hoc shape per RPC.

### `buf breaking` basis

`breaking.use: [FILE]` in `buf.yaml` — the per-file category, which is what
`buf config init` writes by default and is also the category whose semantics
matches a service per file: a method moving to a different file would be
flagged, which is correct here since each file is a stable service boundary.
`make proto-breaking` compares against `.git#branch=main,subdir=proto`,
gated on `proto/` actually existing on `main` yet (`git cat-file -e
main:proto`) — this PR is the first time `proto/` exists at all, so the bare
command would fail with "had no .proto files" rather than a clean pass; the
gate makes it a no-op until this lands, after which every later PR gets a
real check against the previous accepted state.

### Lint basis

`lint.use: [STANDARD]`, also `buf config init`'s default. Checked explicitly
against the installed `buf` (not assumed): `STANDARD` includes
`ENUM_VALUE_PREFIX`, `ENUM_ZERO_VALUE_SUFFIX` and `PACKAGE_DIRECTORY_MATCH`,
and excludes `COMMENTS` (would require every field/message/enum commented;
this codebase's convention, per the CRD types, comments the ones that need
explaining, not all of them) and `UNARY_RPC` (would forbid the server-
streaming `StreamNodeLogs`, which the design requires).

## Consequences

- `proto/ichor/v1alpha1/{common,cluster,machine,node}.proto` is the contract;
  `gen/go/` is generated and committed; `buf.yaml`/`buf.gen.yaml` sit at the
  repo root since there is exactly one module.
- No server implementation exists yet. `MachineServiceHandler`,
  `ClusterServiceHandler` and `NodeServiceHandler` (the interfaces
  `protoc-gen-connect-go` generated) are what the next work unit implements;
  this unit only makes them exist and compile.
- Backup, restore and certificate renewal land as new files/services later,
  additively, same as the deferred disk-role and NIC-alias fields on
  `ApproveMachineRequest` — nothing here was shaped to anticipate them beyond
  leaving room by not reserving names.
- `make proto-generate`, `make proto-lint` and `make proto-breaking` are new,
  standalone Makefile targets, appended after the existing `helm-*` targets;
  no existing target was changed. `make build test lint` does not depend on
  them and passes unchanged — nothing in `internal/`, `pkg/`, `api/` or
  `cmd/` imports `gen/go/` yet, so the existing targets don't even see this
  change, beyond `go.mod`/`go.sum` picking up `connectrpc.com/connect` and
  `google.golang.org/protobuf` moving from indirect to direct.

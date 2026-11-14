# 0021. UI: read and approval screens on the generated TS client

Status: Accepted, amended twice · Date: 2026-10-25 · Design section: CLI and UI, roadmap item 5

Decided: Built the four read/approval UI screens on a committed, generated TS client, with the approval dialog disclosing identity/hardware/role before use

## Context

ADR 0017 built the UI skeleton (routing, build, preactcn wiring) with one
placeholder page. ADR 0016 defined the Connect API and committed its Go
client; ADR 0018 implemented the server. Nothing yet in `ui/` called it, and
no screen existed. This ADR covers "Read-only UI, approval UI" from roadmap
item 5, scoped to exactly four screens the design names in "CLI and UI":
cluster list and detail (status, rollout progress, hand-over state), the
machine list with pending approvals and the approve/reject actions, and a
node detail page (hardware, disks, network, live resource usage, Kubernetes
state, effective config annotated by origin with secrets masked, logs).
Config editing, the GitOps page, a merged multi-node log viewer and deep
links into other tools are explicitly later phases and out of scope here.

Versions:
- `buf` 1.73.0, `protoc-gen-go` v1.36.12, `protoc-gen-connect-go` v1.21.0 -
  unchanged from ADR 0016, still pinned in `flake.nix`.
- `@bufbuild/protoc-gen-es` and `@bufbuild/protobuf` 2.16.0,
  `@connectrpc/connect` and `@connectrpc/connect-web` 2.2.0, installed into `ui/`'s own `package.json`, not nix (`ui/` already has its
  own npm world per ADR 0017; these packages are tied to `ui/`'s own
  `@bufbuild/protobuf` runtime version, which nixpkgs has no reason to track).
  The `createClient(Service, transport)` and `buf.gen.yaml` shapes below follow
  connectrpc.com's "Getting started" and "Generating code" pages.
- `shadcn` CLI 4.22.1 - same version ADR 0017 already used.

## Decision

### TypeScript client generation, and why it's committed

`buf.gen.yaml` gains one plugin entry:

```yaml
  - local: ./ui/node_modules/.bin/protoc-gen-es
    out: ui/src/gen
    opt:
      - target=ts
      - erasable_syntax=true
```

Connect-ES v2 does not need a second, separate connect-es plugin the way
v1 did: `protoc-gen-es` v2 itself generates both the message types and a
`GenService` const (e.g. `export const ClusterService = ...`) that
`createClient(ClusterService, transport)` consumes directly. Adding
`@connectrpc/protoc-gen-connect-es` (still published, at v1.7.0, for the
older style) would be a second plugin generating code nothing here
calls, so it is not used.

`erasable_syntax=true` is not cosmetic: `ui/tsconfig.app.json` already sets
`erasableSyntaxOnly` (ADR 0017's scaffold), which rejects a real TypeScript
`enum` because it compiles to runtime code a type-stripper can't erase.
Protobuf-ES's default enum output is exactly that real `enum`; without this
option, `tsc -b` fails on every generated enum (`RolloutKind`,
`HandoverPhase`, `MachineRole`, `DiskRole`, `ConfigLayer`) with
`TS1294: This syntax is not allowed when 'erasableSyntaxOnly' is enabled`. With it, enums
generate as `as const` objects instead, and `ui/src/lib/enum.ts`'s
`enumLabel()` does the reverse (value -> name) lookup for display that a
real TS enum would otherwise have given for free.

**The generated output (`ui/src/gen/`) is committed**, for the same reason
ADR 0016 committed `gen/go/`: regenerating it needs `buf` (from
`nix develop`) *and* `ui/node_modules/.bin/protoc-gen-es` (from
`cd ui && npm ci`), and nobody who is only touching a screen - the normal
case from here on - should need either just to open the project. The
counter-pressure ADR 0016 didn't have to weigh is that this generator
writes into `ui/`, which `make build test lint` never touches (ADR 0017);
committing it is still the right call, because the alternative is a `ui/`
checkout that doesn't type-check or build until someone runs `buf generate`
first, silently, with no error pointing at why.

One consequence worth stating plainly: `buf.gen.yaml` is shared by both
plugins, so `make proto-generate` (`buf generate`, unchanged as a target -
only its template grew a new plugin) now fails for anyone who hasn't run
`cd ui && npm ci` first, because the TS plugin's binary won't exist on
disk. This is the same category of pre-requisite `proto-generate` already
had (`nix develop`, for the Go plugins), not a new kind of trap, but it is
a new instance of it for whoever next edits `proto/` without having ever
touched `ui/`. `make ui-proto-generate` (with `ui-install` as a
prerequisite) is the one-command fix; documented here rather than solved
by making `ui/node_modules` a dependency of the unchanged `proto-generate`
target.

`ui/eslint.config.js`'s existing radix-only exemption
(`@typescript-eslint/no-explicit-any` off for `src/lib/radix/**`, ADR 0017)
widens to also cover `src/components/ui/**` (also CLI-generated, never
hand-written) and to turn off `no-empty-object-type`/`no-unused-vars`,
both of which the newly-added `select`/`collection`/`input` shims trip;
`src/gen/**` is added to `globalIgnores` outright, since it's regenerated
output with its own `/* eslint-disable */` header, not code this project's
style rules apply to.

### Screens

Four routes, matching the design's four screens one-for-one:

- `/` - `ClusterList`: table of clusters with Talos/Kubernetes versions,
  control-plane count, rollout progress (kind, phase, nodes done/total) and
  hand-over state per component, each a `Badge`.
- `/clusters/:namespace/:name` - `ClusterDetail`: the same `Cluster`
  message's full detail (conditions, rollout, hand-over), reached from a
  list row; there is no separate nav entry for it, same as there's none for
  an individual PR page.
- `/machines` - `MachineList`: pending and approved machines in two tables
  (machine.proto's own comment: pending machines are listed
  unconditionally, never behind a flag), with the approve/reject dialogs
  below.
- `/nodes/:namespace/:name` - `NodeDetail`: hardware, disks (with the
  WWID/serial/by-id-path/bus-path fallback chain node.proto's own comment
  describes - any of the first three may be empty), network interfaces,
  resource usage, Kubernetes status, effective config, and a live log tail.

`router.tsx` (ADR 0017's ~30-line History-API router) grows exactly two
functions, `matchRoute` (a `:param` matcher - split, compare, no
dependency) and `objectRefPath` (builds a `/base/namespace/name` path from
an `ObjectRef`-shaped value) - still no routing library; a route table with
two dynamic patterns doesn't need one either.

Data fetching is `useEffect` + `useState` per page component, calling the
generated client directly; no data-fetching library was added.
`ui/src/lib/connect.ts` is the only place a `ConnectTransport` is
constructed, `baseUrl: window.location.origin` - same-origin, because
`cmd/ichor-ui` serves both the SPA and the Connect handlers off the same
mux (ADR 0018), and "the browser talks only to the backend" (architecture.md,
"Trust") rules out pointing it anywhere else.

Eight more preactcn components were added via
`npx shadcn@latest add @preactcn/{table,badge,dialog,input,label,select,
alert,textarea}` (joining the three ADR 0017 already installed:
button, card, separator) - the set these four screens actually use for
lists, status chips, the approve/reject dialogs and their form fields. A
`Tabs` component was considered for `NodeDetail`'s sections and dropped:
stacking them as separate `Card`s shows the same data with one fewer
component to install and no interaction logic to get right for a phase
that's read-only everywhere except two buttons.

### Stream resume

`ui/src/lib/resumableStream.ts` exports `streamWithResume`, a generator
generic over one streaming call: it loops, reconnecting with the latest
resume token seen whenever the call throws a `ConnectError` with
`Code.Unavailable` (exactly what ADR 0018's `StreamNodeLogs` returns on a
graceful drain), re-throwing anything else, and returning when the stream
ends cleanly. `NodeDetail`'s log tail is its only caller in this phase - one
`AbortController` per mount, passed as the call's `signal`, torn down on
unmount or on a `namespace`/`name` change.

The resume logic is tested without a server: `ui/src/lib/
resumableStream.test.ts` passes a fake call (a hand-written async
generator/iterable, not a mock library) that yields two lines, throws
`CodeUnavailable`, and on the fake's own second invocation asserts it
received the *second* line's token, not the first's or an empty one - the
exact behavior a backend drain needs. A second test checks that a
non-`Unavailable` error propagates instead of looping forever; a third
that a clean stream end doesn't reconnect. They run with `node --test
"src/**/*.test.ts"` (Node 24's native TypeScript support -
`ui/tsconfig.app.json`'s `erasableSyntaxOnly` already commits this project
to code Node can strip and run directly), so no test framework dependency
is needed. `ui/package.json` has one script, `"test"`; the `Makefile` has
`ui-test` (with `ui-install` as its prerequisite). `ui/tsconfig.app.json`'s
`types` array includes `"node"` (alongside `"vite/client"`) so
`node:test`/`node:assert` type-check too - `@types/node` was already a
dependency, just not wired to the app project.

### The approval screen

Approving is treated as the near-irreversible decision architecture.md's
own "Trust" section implies (the controller starts applying configuration
the moment it returns): the dialog shows identity (UUID, serial, MAC
addresses - with a visible warning when any are missing, re-checking ADR
0011's own rule client-side via `isStrongIdentity()`), the found address
(`Machine.endpoint`), and hardware (fetched via `GetNode`, which ADR 0018
notes works even pre-approval through Talos maintenance mode - if that
fetch fails, the dialog says so and still shows everything else, since a
hardware-detail fetch failing is not a reason to block showing identity).
Role is a required `Select` with no default selection; the submit button is
disabled until a role is chosen and the identity is strong - client-side,
*before* ever calling the server, using the same two conditions
`ApproveMachine` (ADR 0018) checks server-side. If the server still refuses
(a race: two people approving the same machine, or an identity that
weakened between list and dialog), `ui/src/lib/errors.ts`'s `describeError`
turns the `ConnectError`'s code into a short headline
(`CodeFailedPrecondition` -> "Not allowed right now",
`CodeInvalidArgument` -> "Missing or invalid input", etc.) shown above the
server's own message - ADR 0018's refusal messages are already plain
English, so the headline adds the "what kind of problem" context without
replacing or parsing them. This was chosen over hand-writing a distinct
message per one of the four refusal reasons: string-matching the server's
message to tell "already approved" apart from "weak identity" (both
`CodeFailedPrecondition`) would be the client depending on wording the
server is free to change; the client-side pre-check already gives the two
most common cases their precise reason, and the code-level headline covers
whatever reaches the server anyway.

Reject's dialog states, before the button exists to click, what rejecting
does and does not do: it is recorded as an `AuditRecord` only; the
`TalosMachine` is neither deleted nor mutated. In
`machine.proto`'s `Machine` message at this point the only
status field is `approved bool` - there is no separate "rejected" value -
so the dialog says plainly that a rejected machine can reappear in the
pending list indistinguishable from one that was never looked at, if its
own discovery loop runs again. This restates ADR 0018's own "Gap to report"
in a form a person approving or rejecting machines actually reads, rather
than leaving it as something only the ADR says.

## Consequences

- Four screens, reachable from the nav (`Clusters`, `Machines`) or from a
  list row (`ClusterDetail`, `NodeDetail`), all reading through the
  generated TS client; approve/reject both work end to end against the
  generated request/response shapes.
- `make build test lint` needs no UI build (`cmd/ichor-ui` embeds a placeholder
  by default, ADR 0017); `make ui-ci` (ADR 0017's existing target) and
  `make ui-test` cover the UI. `ui/node_modules` and `cmd/ichor-ui/dist` remain
  untracked (`ui/.gitignore`/root `.gitignore`, both from ADR 0017).
- `react` and `preact/compat` must not appear anywhere in
  `ui/package-lock.json`, including via `@floating-ui/dom` and `lucide-preact`,
  which `shadcn add` pulls in on its own for the select/dialog positioning and
  icons the new components need.
- The four screens need nothing beyond the wire types ADR 0016 defined: every
  field the design describes for them (status, rollout, hand-over, identity,
  endpoint, role, hardware, disks, network, resource usage, Kubernetes status,
  effective config with origin and masking, log lines with a resume token)
  already exists, so `proto/` is unchanged.
- No end-to-end run of the UI against a live `cmd/ichor-ui`/`internal/uiapi` is
  part of this decision: the wire format rests on both sides generating from the
  same `.proto` files, and `internal/uiapi`'s specs (ADR 0018) cover the server
  side of every RPC these screens call.
- `ResourceUsage.cpuCoresUsed`'s server-side formula is an assumption about the
  sample window (ADR 0018's own caveat); `NodeDetail` displays whatever it
  receives without re-deriving or second-guessing it.

## Alternatives considered

**A separate `buf.gen.ts.yaml` template, invoked by its own Makefile
target, instead of adding a plugin entry to the existing `buf.gen.yaml`.**
Would have kept `make proto-generate` working without `ui/node_modules`
present. Not taken: one generate command producing both outputs from one source of
truth is simpler to reason about than two templates that could drift out of
sync with each other's plugin options.
The cost (documented above) is paid once per `ui/` checkout, not per
screen.

**`@connectrpc/protoc-gen-connect-es` (the v1-style connect plugin)
alongside `protoc-gen-es`.** Still published (v1.7.0) for projects on the
older code-generation style; unnecessary here since `protoc-gen-es` v2
already generates the `GenService` `createClient` needs.

**A data-fetching library (e.g. a small SWR-style hook) instead of
`useEffect`/`useState` per page.** Four pages, each with at most two
independent fetches and no shared cache requirement across them (a cluster
list and a node detail page never need to agree on the same cached
`Cluster` object), so none is needed, matching ADR 0017's own precedent of
preferring zero dependencies over a reach for one.

**A `Tabs` component for `NodeDetail`.** See "Screens" above - stacked
`Card`s instead.

## Amendment (same date): approval tri-state and service-selectable logs

ADR 0018's amendment changes the two proto messages these screens read:
`Machine.approved` (bool) became `Machine.approval_state`
(`MachineApprovalState`: pending/approved/rejected), and
`StreamNodeLogsRequest` gained `service` (empty selects the kernel log,
otherwise a Talos service name). `ui/src/gen` was regenerated against the
new `proto/`; no `buf.gen.yaml`/plugin change was needed, since both are
ordinary field changes the existing `target=ts` + `erasable_syntax=true`
generation already handles.

`MachineList` now has three buckets - pending, rejected, approved - instead
of two. This closes the exact gap "the approval screen" section above
documents working around: a rejected machine used to have no distinct
state, so this screen's own copy warned that rejecting could look like it
"did nothing" the next time the list loaded. With `approval_state`, a
rejected machine gets its own section and stops reappearing as "pending."
Approve and Reject both stay available on a rejected row - re-approving is
the only "un-reject" this design defines (ADR 0018's amendment: reversible
by design, not a dead end for a misclick), and re-rejecting an
already-rejected machine is allowed server-side and just records the
newest reason. The Reject dialog's copy changed to match: it no longer
claims there's no durable rejected state, since there now is one.

`NodeDetail`'s log tail gained a source `Select` (kernel/kubelet/etcd/
trustd/apid/machined, the names node.proto's own comment lists) above the
log view. Switching it resets the resume token and restarts
`streamWithResume` against the new `service` value - a different log
source is a different stream, not a resumable continuation of the old
one. `"kernel"` is this component's own sentinel for the wire value `""`:
`@preactcn/select`'s own convention reserves an empty string for "nothing
selected", so a legitimate
empty-string wire value needs a non-empty UI-side stand-in, mapped back to
`""` only when building the request.

## Second amendment (same date): the decision record lives on the CR, not only in the RPC response

A rejected machine's reason was visible exactly once, in `RejectMachine`'s
own `AuditRecord` response - gone the next time anyone loaded the list, so
revisiting a rejected machine later showed *that* it was rejected but
never *why*. Closed the way `approval_state` itself was closed (ADR
0018's amendment): on the CR, the single source of truth every reader
(this UI, the CLI, `kubectl get -o yaml`) agrees on, not duplicated per-
caller state that only the original caller ever saw.

`TalosMachineSpec.ApprovalDecision` (`api/v1alpha1/talosmachine_types.go`,
a new `*TalosMachineApprovalDecision{Actor, Reason, At}`) sits beside
`ApprovalState`, not inside it: the struct asserts no approved/rejected
state of its own, so `ApprovalState` stays the only field a reader needs
to check for that. `ApproveMachine` and `RejectMachine`
(`internal/uiapi/machine_service.go`) both set it now; `IsApproved()`'s
own behavior is untouched. `actor` stays the fixed `"ichor-ui"` string
(ADR 0018) - added now, not deferred, so no decision recorded from here on
is ever missing an actor once a real per-person one exists to fill it.

**Symmetric reason, by design.** `ApproveMachineRequest` gained an
optional `reason` field alongside `RejectMachineRequest`'s existing one,
additive on the wire (`buf breaking` reports nothing). Rejecting almost
always benefits from a stated reason; approving usually doesn't - the
dialog already discloses identity, hardware and role before the button is
even clickable, so the "why" is usually self-evident. But
`AuditRecord.reason` was never documented as reject-only ("mainly used
for rejections," ADR 0016 - not exclusive to them), and giving approve a
reason field costs one optional string, not a parallel code path.
Asymmetry would have meant writing down a reason why only one half of a
symmetric action could explain itself, which this had no good answer for.

`MachineList`'s Rejected section shows `Reason` and `When` per row now,
read straight from `Machine.approval_decision`. The approve dialog gained
its own optional, non-blocking `Reason` textarea for the same symmetry.

`IsApproved()`'s meaning does not change, and `internal/controller` is
untouched.

Amendment 2026-11-05 (ADR 0130): the "symmetric reason" is gone. `ApproveMachineRequest.reason`, `RejectMachineRequest.reason` and `MachineApprovalDecision.reason` are deprecated and unused, `TalosMachine.spec.approvalDecision.reason` is kept in the schema but no longer written or shown, the Approve and Reject dialogs have no reason box, and the Rejected list has no Reason column. `ichor approve node|reject` lost `--reason`. The decision still records the actor and the time.

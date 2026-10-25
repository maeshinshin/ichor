# 0018. UI backend: the read and approval server implementation

Status: Accepted · Date: 2026-10-25 · Design section: CLI and UI, roadmap item 5

## Context

ADR 0016 made `ClusterService`, `MachineService` and `NodeService` exist as
compiling Go interfaces (`ichorv1alpha1connect.*Handler`) and committed the
generated code under `gen/go/`. Nothing implemented them. This unit is that
implementation, registered on `cmd/ichor-ui`: nine RPCs across the three
services, the Talos-reading half of `NodeService`, secret masking for
`GetEffectiveConfig`, a resumable log stream, graceful termination, and a
UI-backend-only RBAC `ClusterRole` that stays out of the controller's
`manager-role`.

Versions consulted: `connectrpc.com/connect` v1.21.0 and
`github.com/siderolabs/talos/pkg/machinery` v1.14.3, both already pinned in
`go.mod`. Everything below was checked against the vendored module source
in the Go module cache (`go.yaml.in/yaml/v4`'s actual decode behavior,
machinery's `config.Provider.RedactSecrets`, the COSI resource shapes under
`pkg/machinery/resources/{hardware,block,network,perf}`) rather than taken
on faith from documentation; no WebFetch against talos.dev was needed since
the module source answered every question this unit ran into. Context7 and
Serena were unavailable for this session, as noted by the task; `rg` and
direct reads of the module cache stood in for both.

## Decision

### One `Server` type, three handler interfaces

`internal/uiapi.Server` (`server.go`) embeds `client.Client` and implements
`ClusterServiceHandler`, `MachineServiceHandler` and `NodeServiceHandler` on
the same type. All three read the same two CRDs (`TalosCluster`,
`TalosMachine`) through the same client; splitting them into three Go types
would only have produced three copies of the same `Get`/`List` plumbing.
`cmd/ichor-ui/main.go` constructs one `Server` and registers all three
generated handlers on one `http.ServeMux`.

The Kubernetes client is a plain, uncached `client.New(cfg, ...)`, not a
`ctrl.Manager`'s cached/informer-backed one: this backend is a stateless,
request-driven reader, not a reconciler, so there is no watch loop to
justify an informer cache, and avoiding one keeps the RBAC `ClusterRole`
honest about what it actually needs (`get`/`list`, never `watch`).

### Approval's four refusals (`machine_service.go`)

`ApproveMachine` refuses, in order, before it writes anything:

1. **Already approved** -- `CodeFailedPrecondition`, no mutation at all.
   Re-approval does not try to detect "did anything actually change"; it
   refuses outright, so "approving an already-approved machine causes no
   reconfiguration" holds by construction rather than by a diff that could
   itself be wrong.
2. **Weak identity** -- `strongIdentity` (`identity.go`) re-applies ADR
   0011's rule (UUID, serial and at least one permanent MAC all present) to
   the `TalosMachine`'s already-recorded `spec.identity`. This is a
   deliberate second check, not reuse of `internal/controller`'s: that
   version is unexported and keyed on its own `HardwareIdentity`, not the
   CRD type, and discovery refusing to auto-register a weak identity does
   not guarantee every `TalosMachine` this server ever sees went through
   discovery (a future registration path, or a hand-written fixture, might
   not have).
3. **`MACHINE_ROLE_UNSPECIFIED`** -- `CodeInvalidArgument`. The wire enum's
   zero value is refused rather than stored as an empty
   `TalosMachineSpec.spec.role`: `pkg/render.BuildConfig`'s call to
   machinery's `machine.ParseType` does not itself reject an empty role (an
   empty role resolves to a worker silently, machinery's own choice, not
   this package's to rely on changing), so the check is explicit here and
   again in `GetEffectiveConfig` rather than assumed downstream.

`RejectMachine` refuses to touch an already-approved machine
(`CodeFailedPrecondition` -- "reject" has no meaning post-approval; removal
is a different, later operation) and, for a pending machine, deliberately
mutates nothing and deletes nothing. Deleting it was the real risk design
wanted guarded against: ADR 0011's "same machine returning" branch only
short-circuits for an object that still exists under its own name, so
deleting a rejected machine would let the next discovery pass recreate it
as fresh and pending, making the rejection meaningless the moment discovery
runs again. Leaving the object alone, with its `Approved: false` untouched,
is the whole fix.

**Gap to report**: the proto has no "rejected" state distinct from
"pending." `RejectMachineResponse` carries only an `AuditRecord`; the
`Machine` message's `approved bool` cannot represent "explicitly turned
away" versus "never looked at," so a rejected machine looks identical to a
freshly discovered one in `ListMachines`/`GetMachine`. Recording that
durably (an annotation, or a proto field) is a contract decision, left to
the design per the task's instruction not to add fields speculatively.

### Audit (`AuditRecord`)

`actor` is the fixed string `"ichor-ui"` (`machine_service.go`'s `actor`
constant), not a per-caller identity: there is no authentication yet
(OIDC/impersonation is roadmap phase 9), so anything else would be
fabricated. `AuditRecord` is returned to the caller and nothing more --
not also written as a Kubernetes `Event` or any other durable log. Adding
an `EventRecorder` would mean wiring a `client-go` broadcaster this backend
has no other use for, purely to duplicate what the response already states;
skipped under ponytail's "does this need to exist."

### Secrets masking is machinery's job, not a path denylist

`effectiveconfig.go` does not classify any field path as secret by hand.
`pkg/render.BuildConfig` (new, exported from the previous `Render`'s body;
see below) returns the merged `config.Provider` before encoding, and
`effectiveConfigValues` renders it twice -- once plain, once through
`Provider.RedactSecrets(secretMask)` -- then flattens both to
path-\>JSON-value maps (`leafPaths`/`walkLeaves`) and calls any leaf where
the two differ `secret: true`, taking the **redacted** copy's value for the
wire. A leaf is never read from the plain tree once it is known to differ.

This was the deliberate choice over a `pkg/fieldmeta`-style prefix table:
machinery's own `config/types/v1alpha1/v1alpha1_redact_test.go` shows
`RedactSecrets` already covers tokens, CA keys (never certs -- a cert is
public by definition and stays unredacted), the cluster bootstrap/discovery
secret, aescbc/secretbox encryption secrets, registry auth (username stays,
password/token/client-key don't), wireguard private keys, VIP cloud API
tokens and disk-encryption passphrases. Re-deriving that list by hand would
duplicate machinery's own authority on its config shape and silently fall
behind every time machinery adds a secret-bearing field; diffing against
machinery's own redaction cannot fall behind it.

Two things found empirically while building the leak test, both now
load-bearing comments in the code:

- `Provider.RedactSecrets` does not always honor the replacement string it
  is given: the cluster bootstrap token field is hardcoded to `"***"`
  regardless of the caller's argument (confirmed by reading
  `v1alpha1_redact_test.go`, which asserts `"***"` for that one field while
  every other redacted field uses the test's own `"**.***"`). `secretMask`
  is therefore fixed at the literal `"***"` in this package -- changing it
  would make that one field's mask silently diverge from every other
  masked field's.
- A `[]byte`-typed field (a CA key) decodes back from YAML as a plain Go
  `string`, and the *value itself* is already base64 text in the YAML
  machinery wrote, not the raw PEM text -- this is machinery's own
  rendering choice for `[]byte` fields, observed rather than documented.
  `GetEffectiveConfig`'s wire value for such a field is therefore base64,
  which is still safe (nothing of the real key survives a correct
  redaction) but not literally the string `"***"`; the test
  (`effectiveconfig_test.go`) checks both the raw and base64-encoded forms
  of the two CA keys for exactly this reason.

### Origin, without a second renderer

A leaf's `ConfigOrigin` is the last (bottom-up, so highest-priority)
applicable `TalosConfigPatch` whose **own** documents -- rendered in
isolation via `render.DocumentFromPatch` (newly exported for this reason)
plus `container.New(doc).EncodeBytes(...)`, never diffed against the merged
tree -- set that exact leaf path. A leaf no patch's own document sets is
bucketed as `{layer: CONFIG_LAYER_SYSTEM, source: "system"}`. This needed
no second render pass over the whole config (the earlier draft of this
design called `render.Render` once per patch-prefix and diffed consecutive
outputs; dropped once `DocumentFromPatch` made rendering each patch's own,
isolated contribution directly possible) -- `patchOrigins` runs once over
the already-known list of applicable patches.

Simplification recorded here rather than tested further: everything not
claimed by a patch is bucketed as `CONFIG_LAYER_SYSTEM`/`"system"`, with no
split between "Ichor's own system-layer generation" (the control-plane
taint) and "whatever machinery's `generate.NewInput().Config()` fills by
default" (cluster name, default install image, the discovery identity
document). The design's own `ConfigOrigin.source` comment allows both
`"system"` and `"cluster"` as non-patch sources without defining the line
between them; splitting it further wasn't something this unit had a
concrete screen requirement to resolve against.

### `pkg/render` changes: `BuildConfig` and `DocumentFromPatch` exported

`pkg/render/render.go` gained two exports, both pure refactors of code that
already existed, not new rendering logic:

- `BuildConfig` is `Render`'s existing body, stopping before
  `EncodeBytes`/hashing and returning the `coreconfig.Provider` itself.
  `Render` now calls `BuildConfig` then encodes -- identical behavior,
  confirmed by `pkg/render`'s existing test suite passing unchanged.
- `DocumentFromPatch` (was `documentFromPatch`) is the same per-document
  kind switch, now exported so `patchOrigins` can convert one patch's
  documents without reimplementing the kind dispatch.

This package is one `pkg/render` other agents are also working in: the
task flagged that a fix rejecting an empty `TalosMachineSpec.spec.role`
(machinery's `machine.ParseType("")` otherwise resolves to a worker
silently) might already be landing elsewhere. It is not present in this
worktree's `pkg/render/render.go` as of this change, and this unit's own
refactor does not touch `machine.ParseType` or add any such check inside
`pkg/render` itself -- `ApproveMachine` and `GetEffectiveConfig` each guard
against an empty/unspecified role on their own side instead (see above and
`node_service.go`), independent of whatever lands in `pkg/render` later, so
neither relies on a check that might not be there yet.

### Resume tokens (`resumetoken.go`)

A per-machine `{time, ordinal}` checkpoint, JSON-then-base64-encoded,
exactly matching ADR 0016's "last-seen log timestamp plus a monotonic
ordinal to break ties within the same timestamp, joined and base64-encoded
as one token covering every machine in the request." Opaque to the client,
as specified -- nothing but this package ever decodes it.

`StreamNodeLogs` streams Talos's `Dmesg` API (`talosclient_machinery.go`):
the request carries `repeated ObjectRef machines` and a resume token, no
service/container selector, so a whole-machine kernel log is the only log
source the proto's shape can mean (a per-container log, which would need an
`id`, is a gap to report if the UI ever needs it). `LogLine.Time` is when
this backend *observed* the line, not a timestamp parsed out of it: Dmesg's
lines carry a boot-relative monotonic offset, not wall-clock time, in the
vendored module's resource shape (no controller source ships with
`pkg/machinery` to check the exact format against), so the backend's own
clock is the only stable ordering key available without guessing at an
unverified text format.

**Known limit, recorded rather than hidden**: resuming after a backend
drain re-opens a fresh `Dmesg` stream on the new pod; Talos's own `tail`
flag replays some recent backlog, but each replayed line gets a *new*
`LogLine.Time` from the new pod's clock, not the original observation time.
A resumed stream therefore dedupes on "have I sent a line with this
observed time or later" within one backend process, not truly exactly
across a drain -- a small window of possible gap or re-delivery right at
the reconnect point is accepted rather than solved, since Dmesg offers
nothing stable to key an exact resume on. This is explicitly why logs don't
get the same "exact resumable" treatment architecture.md's "streams that
resume from a token" reserves for a `resourceVersion`-keyed watch.

### `NodeTalosClient`: the read-only Talos surface (`talosclient.go`, `talosclient_machinery.go`)

A four-method interface (`Hardware`, `Disks`, `NetworkInterfaces`,
`ResourceUsage`, plus `StreamLogs`) returning the wire types
(`gen/go/ichor/v1alpha1.Hardware` etc.) directly -- no parallel domain
struct, since nothing else in this package needs a Talos-shaped value that
isn't about to be put straight on a response. `NewMachineryNodeTalosClient`
dials maintenance mode (insecure, unauthenticated) when the machine isn't
approved yet -- no CA trust exists for it -- or mints a `role.Reader`
client certificate from the bundle otherwise ("readers who can access
read-only APIs that do not expose secrets," `pkg/machinery/role`'s own doc
comment), matching "Trust": the UI backend never holds an admin identity.

Per field, mapped from COSI resources confirmed in the vendored module
(`hardware.SystemInformation/Processor/MemoryModule`, `block.Disk`,
`network.LinkStatus/AddressStatus`, `perf.CPU/Memory`):

- `Disk.by_id_path` comes from scanning `block.Disk.Symlinks` for a
  `/by-id/` path, since the resource carries no dedicated field for it.
- `Disk.role` and `NetworkInterface.alias` are always
  `DISK_ROLE_UNSPECIFIED`/empty: disk-role assignment and
  `LinkAliasConfig` don't exist on the CRD yet (the same gap ADR 0016
  already noted for `ApproveMachineRequest`), so there is no source of
  truth to fill them from. Both will populate automatically once that CRD
  work lands, with no change needed here.
- **Unverified shortcut**: `ResourceUsage.cpu_cores_used` is derived from
  `perf.CPU.CPUTotal.Idle` as `(100-idle)/100 * cores`, assuming
  `CPUTotal`'s fields are already a percentage for the latest sample
  window. The vendored module ships only the resource's shape, not the
  controller that populates it, so this was never checked against a real
  Talos node's live values. Upgrade by sampling `perf.CPU` twice and
  computing a rate once this is checked against a live cluster.

This file (like `internal/controller`'s own `*_machinery.go` files) has no
direct unit test: it is a thin adapter over a real gRPC client, and no
Talos call may reach a real node from a test. `NodeTalosClient`'s consuming
logic in `node_service.go` is tested through a hand-written fake instead.

### RBAC: kept out of `manager-role` with a build tag, not a path change

`internal/uiapi/rbac_markers.go` carries every `+kubebuilder:rbac` marker
for this package's own `ClusterRole`, behind `//go:build ichorui_rbac` --
a tag `go build`/`go test`/`go vet` never set, and the file has nothing but
a package clause and comments, so excluding it changes nothing about what
the package does or how it's tested. `make manifests` (`paths="./..."`, no
special tags) therefore never discovers these markers and never merges them
into the controller's `manager-role`. A new, purely additive Makefile
target, `manifests-ichor-ui`, passes `--load-build-tags=ichorui_rbac` and
scopes `paths="./internal/uiapi/..."`, writing `config/rbac/ichor-ui/role.yaml`
under its own `roleName=ichor-ui-role`.

This was chosen over narrowing `manifests`'s own `paths="./..."` argument
(which would also have worked, since `./cmd/ichor-ui` and `./cmd` can be
told apart by whether the glob is recursive) because other agents are
concurrently editing this Makefile and were told not to expect its existing
targets rewritten; the build-tag approach needed zero changes to any
existing line, confirmed by running `make manifests` before and after and
diffing `config/`: empty.

The resulting `ClusterRole` (`get`/`list` on `talosclusters` and
`talosconfigpatches`, `get`/`list`/`update` -- never `create`, `patch` or
`delete` -- on `talosmachines`, `get` on `secrets`) is deliberately narrower
than the controller's: no `watch` (no cache/informer, see above), no
`events`, no `finalizers`, no `status` subresource (approval writes
`spec` through the main resource endpoint via `client.Update`, not
`Patch`, so `update` is the only verb ever exercised). `secrets: get`
mirrors a tradeoff ADR 0011 already accepted for the controller for the
same structural reason: `TalosCluster.spec.secretsRef` can name a Secret in
any namespace, so nothing narrower than cluster scope can express "the
Secret this particular `TalosCluster` names," short of restricting
`TalosCluster` to one namespace -- a design decision neither ADR made
unilaterally.

`config/rbac/ichor-ui/` (`service_account.yaml`, `role.yaml` (generated),
`role_binding.yaml`, `kustomization.yaml`) is a standalone kustomize base,
deliberately **not** added as a resource of `config/rbac/kustomization.yaml`
or `config/default/kustomization.yaml`: `ichor-ui` has no Deployment or
Service manifest yet anywhere in `config/` (that is a later work unit, per
ADR 0017's own scope boundary), so wiring RBAC into the controller's install
path would deploy permissions for a workload that doesn't exist there yet.

### `cmd/ichor-ui/main.go`: wiring and graceful termination

`main.go` gained a Kubernetes client (`ctrl.GetConfigOrDie()` +
`client.New`, the scheme built the same way `cmd/main.go` builds its own:
`clientgoscheme.AddToScheme` + `ichorv1alpha1.AddToScheme`), a `draining`
channel, a `uiapi.Server`, and the three generated handlers registered on
the existing mux alongside `spaHandler`. Nothing in `webfs_dev.go`,
`webfs_embed.go` or the SPA-fallback logic changed.

Graceful termination: `draining` is closed exactly once, at the same point
shutdown already began (`<-ctx.Done()`), **before** `srv.Shutdown` is
called. `StreamNodeLogs`'s own `select` (`done` vs. `s.Draining`) notices on
its next scheduling and returns `CodeUnavailable` with a message asking the
client to reconnect with its latest resume token -- "graceful termination
that asks clients to reconnect" (architecture.md). Any already-finished
unary call is unaffected; `srv.Shutdown`'s own timeout is what the draining
signal exists to make unnecessary for an in-flight stream specifically,
rather than making that stream wait out the whole shutdown window.

## Consequences

- Nine RPCs across three services are implemented and tested against
  envtest (`internal/uiapi/*_test.go`, 18 Ginkgo specs plus plain-Go tests
  for `strongIdentity` and the resume token). `make build test lint` passes
  unchanged for the rest of the repository.
- The masking guarantee rests on `config.Provider.RedactSecrets`, not on
  this package's own judgment of which fields are secret. If machinery ever
  stops redacting a field it used to, this package's output changes with
  it automatically (for better, if machinery widens its own redaction, or
  worse, if it narrows it) -- a dependency worth re-confirming on any
  `pkg/machinery` version bump, the same way a version-contract bump
  already gets scrutiny per principle 4.
- `pkg/render/render.go` now exports `BuildConfig` and `DocumentFromPatch`;
  both are refactors of existing code, confirmed behavior-identical by the
  package's own pre-existing test suite passing unchanged.
- `config/rbac/ichor-ui/` exists but is not deployed by any kustomization
  yet; wiring it in is the job of whichever work unit adds `ichor-ui`'s
  Deployment/Service manifests.
- Gaps reported rather than silently worked around: no proto-level
  "rejected" state for a `TalosMachine` (noted above); `StreamNodeLogs` has
  no way to select a specific service/container log, only the whole-machine
  kernel log; `AuditRecord.actor` is a fixed placeholder pending OIDC
  (roadmap phase 9); `ResourceUsage.cpu_cores_used`'s formula is unverified
  against a live node.

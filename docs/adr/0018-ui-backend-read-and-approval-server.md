# 0018. UI backend: the read and approval server implementation

Status: Accepted, amended five times · Date: 2026-10-25 · Design section: CLI and UI, roadmap item 5

Decided: One `Server` type implements all three handler interfaces over a single uncached client; settled the four approval refusals; fixed `loadBundle` resolving the TalosCluster's own namespace instead of the backend's, the same bug ADR 0011 had already fixed on the controller side; granted the `nodes` get/list `findKubernetesNode` had always read without it; added a chart-vs-`config/` RBAC drift test after the chart's own copy of this `ClusterRole` was found silently narrower than `config/rbac/ichor-ui/role.yaml`; extended that drift test to the controller's `manager-role` and `manager-secrets-role`, which found and fixed missing `talosoperations`, Argo CD `applications` and metrics-cert Secret grants

## Context

ADR 0016 made `ClusterService`, `MachineService` and `NodeService` exist as
compiling Go interfaces (`ichorv1alpha1connect.*Handler`) and committed the
generated code under `gen/go/`. Nothing implemented them. This ADR is that
implementation, registered on `cmd/ichor-ui`: nine RPCs across the three
services, the Talos-reading half of `NodeService`, secret masking for
`GetEffectiveConfig`, a resumable log stream, graceful termination, and a
UI-backend-only RBAC `ClusterRole` that stays out of the controller's
`manager-role`.

Versions: `connectrpc.com/connect` v1.21.0 and
`github.com/siderolabs/talos/pkg/machinery` v1.14.3, both pinned in `go.mod`.
The vendored module source in the Go module cache is the authority for
`go.yaml.in/yaml/v4`'s decode behavior, machinery's
`config.Provider.RedactSecrets`, and the COSI resource shapes under
`pkg/machinery/resources/{hardware,block,network,perf}`.

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
2. **Weak identity** -- re-applies ADR 0011's rule (UUID, serial and at
   least one permanent MAC all present) to the `TalosMachine`'s
   already-recorded `spec.identity`, via
   `ichorv1alpha1.TalosMachineIdentity.IsStrongIdentity` (`identity.go`
   itself was later deleted once the rule moved there; see ADR 0033).
   This is a deliberate second *check*, not a second *rule*:
   `internal/controller`'s own call site converts its own
   `HardwareIdentity` to the CRD type and calls the same method, and
   discovery refusing to auto-register a weak identity does not
   guarantee every `TalosMachine` this server ever sees went through
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
the design rather than added speculatively (the tri-state amendment below
closes it).

### Audit (`AuditRecord`)

`actor` is the fixed string `"ichor-ui"` (`machine_service.go`'s `actor`
constant), not a per-caller identity: there is no authentication yet
(OIDC/impersonation is roadmap phase 9), so anything else would be
fabricated. `AuditRecord` is returned to the caller and nothing more --
not also written as a Kubernetes `Event` or any other durable log. Adding
an `EventRecorder` would mean wiring a `client-go` broadcaster this backend
has no other use for, purely to duplicate what the response already states;
skipped because it does not need to exist.

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

Two properties of machinery's redaction, both load-bearing comments in the
code:

- `Provider.RedactSecrets` does not always honor the replacement string it
  is given: the cluster bootstrap token field is hardcoded to `"***"`
  regardless of the caller's argument (machinery's
  `v1alpha1_redact_test.go` asserts `"***"` for that one field while every
  other redacted field uses the test's own `"**.***"`). `secretMask`
  is therefore fixed at the literal `"***"` in this package -- changing it
  would make that one field's mask silently diverge from every other
  masked field's.
- A `[]byte`-typed field (a CA key) decodes back from YAML as a plain Go
  `string`, and the *value itself* is already base64 text in the YAML
  machinery wrote, not the raw PEM text -- this is machinery's own
  rendering choice for `[]byte` fields, observed rather than documented.
  `GetEffectiveConfig`'s wire value for such a field is therefore base64,
  which is still safe (nothing of the real key survives a correct
  redaction) but not literally the string `"***"`, so the leak check
  (`effectiveconfig_test.go`) covers both the raw and base64-encoded forms of
  the two CA keys.

### Origin, without a second renderer

A leaf's `ConfigOrigin` is the last (bottom-up, so highest-priority)
applicable `TalosConfigPatch` whose **own** documents -- rendered in
isolation via `render.DocumentFromPatch` (newly exported for this reason)
plus `container.New(doc).EncodeBytes(...)`, never diffed against the merged
tree -- set that exact leaf path. A leaf no patch's own document sets is
bucketed as `{layer: CONFIG_LAYER_SYSTEM, source: "system"}`. This needs
no second render pass over the whole config: rendering once per patch-prefix
and diffing consecutive outputs was rejected because `DocumentFromPatch` makes
each patch's own, isolated contribution directly renderable --
`patchOrigins` runs once over the already-known list of applicable patches.

Simplification: everything not
claimed by a patch is bucketed as `CONFIG_LAYER_SYSTEM`/`"system"`, with no
split between "Ichor's own system-layer generation" (the control-plane
taint) and "whatever machinery's `generate.NewInput().Config()` fills by
default" (cluster name, default install image, the discovery identity
document). The design's own `ConfigOrigin.source` comment allows both
`"system"` and `"cluster"` as non-patch sources without defining the line
between them; no screen requires splitting it further.

### `pkg/render` changes: `BuildConfig` and `DocumentFromPatch` exported

`pkg/render/render.go` gained two exports, both pure refactors of code that
already existed, not new rendering logic:

- `BuildConfig` is `Render`'s existing body, stopping before
  `EncodeBytes`/hashing and returning the `coreconfig.Provider` itself.
  `Render` calls `BuildConfig` then encodes -- identical behavior.
- `DocumentFromPatch` (was `documentFromPatch`) is the same per-document
  kind switch, now exported so `patchOrigins` can convert one patch's
  documents without reimplementing the kind dispatch.

`ApproveMachine` and `GetEffectiveConfig` each guard against an
empty/unspecified role on their own side (machinery's `machine.ParseType("")`
otherwise resolves to a worker silently; see above and `node_service.go`),
independent of `pkg/render`'s own explicit empty-role check (ADR 0011), so
neither relies on a check downstream.

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

Per field, mapped from COSI resources in the vendored module
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
- **Shortcut**: `ResourceUsage.cpu_cores_used` is derived from
  `perf.CPU.CPUTotal.Idle` as `(100-idle)/100 * cores`, assuming
  `CPUTotal`'s fields are already a percentage for the latest sample
  window. The vendored module ships only the resource's shape, not the
  controller that populates it, so the formula is an assumption about the
  sample window. Upgrade by sampling `perf.CPU` twice and computing a rate.

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
told apart by whether the glob is recursive) because the build-tag approach
needs no change to any existing Makefile target, and `make manifests` output
for everything else is unchanged.

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
Service manifest yet anywhere in `config/` (shipping it is a separate, later
decision; see ADR 0017), so wiring RBAC into the controller's install
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

- Nine RPCs across three services are implemented over a single uncached
  client.
- The masking guarantee rests on `config.Provider.RedactSecrets`, not on
  this package's own judgment of which fields are secret. If machinery ever
  stops redacting a field it used to, this package's output changes with
  it automatically (for better, if machinery widens its own redaction, or
  worse, if it narrows it) -- a dependency worth re-confirming on any
  `pkg/machinery` version bump, the same way a version-contract bump
  already gets scrutiny per principle 4.
- `pkg/render/render.go` now exports `BuildConfig` and `DocumentFromPatch`;
  both are refactors of existing code with identical behavior.
- `config/rbac/ichor-ui/` exists but is not deployed by any kustomization
  yet; wiring it in belongs with the change that adds `ichor-ui`'s
  Deployment/Service manifests.
- Gaps reported rather than silently worked around, two of which the
  amendment below closes: no proto-level "rejected" state for a
  `TalosMachine` (closed), and `StreamNodeLogs` having no way to select a
  specific service/container log (closed). Still open: `AuditRecord.actor`
  is a fixed placeholder pending OIDC (roadmap phase 9);
  `ResourceUsage.cpu_cores_used`'s formula is an assumption about the sample
  window.

## A build-tag file is not type-checked by default, and two gaps that let it drift silently

Putting `rbac_markers.go`'s content behind `//go:build ichorui_rbac` correctly keeps
its markers out of `manager-role` (the stated goal), but it has a side
effect nobody had named yet -- `go build ./...`, `go test ./...`, `go vet
./...` and golangci-lint's default run **never compile that file at all**,
since none of them ever sets that tag.

A syntax or type error in that file would pass every check in the repo
silently. `rbac_markers.go` is comment-only today so this never bit, but the
next file anyone puts behind a build tag for the same "keep it out of a
default scan" reason will hit this the first time it has an actual mistake
in it. Fixed in `make vet`, the one place now: a second `go vet -tags
ichorui_rbac ./internal/uiapi/...` pass after the untagged one, so the file
is compiled on every `make test`/CI run without the tag ever reaching
`manifests`, `build`, or anything that produces a binary. One place, not
two: golangci-lint's own build-tag config was considered and rejected so
that only `make vet`'s recipe needs to know this tag exists.

Two related gaps closed in the same pass, both CI-level rather than
`internal/uiapi`-level:

- **`manifests-ichor-ui` was defined but never called from anywhere** --
  not `manifests`, not CI. `config/rbac/ichor-ui/role.yaml` could silently
  fall behind `rbac_markers.go`'s actual markers indefinitely; nothing
  would ever regenerate it except a developer remembering the target's
  name. `manifests` now depends on `manifests-ichor-ui`, so one `make
  manifests` run (or the CI job below) regenerates both `ClusterRole`s
  together.
- **No CI step compared generated output against what's committed, for
  anything** -- `config/crd/bases`, `config/rbac/*` (including
  `ichor-ui/role.yaml` once wired in above), and `pkg/typedconfig`'s
  `zz_generated.*.go` could all drift from their source silently; only
  `dist/chart/chart_test.go`'s chart-vs-`config/` check existed, one layer
  short of source-vs-`config/`. Added as `manifests-drift`, a job in
  `.github/workflows/test.yml` run in parallel with `test`: `make manifests
  generate fmt` (`fmt`, not just `generate` -- see its own comment: raw
  `controller-gen`/mirrorgen output isn't always import-sorted the way
  `go fmt` leaves it, and skipping it produces false positives against
  already-correct, already-committed files), then `git diff --exit-code`,
  failing with a message naming the fix (`make manifests generate fmt`, then
  commit).

## Amendment (same date): approval tri-state and service-selectable logs

Two reported gaps are closed: `RejectMachine`'s own design (never deleting the machine) left no way to
record *that* it had been rejected, and `StreamNodeLogs` could only ever
mean the kernel log. Both proto messages change; both changes are additive
or a pre-release breaking change the design explicitly authorized (v1alpha1,
not yet shipped to any real client).

### `TalosMachineSpec.Approved` (bool) becomes `ApprovalState` (tri-state)

A bool cannot represent pending/approved/rejected without a second field
claiming the same truth, which is exactly the anti-pattern to avoid: two
fields that can both answer "is this approved," with nothing stopping them
from disagreeing. `api/v1alpha1/talosmachine_types.go` removes `Approved
bool` and adds `ApprovalState string` (`pending`/`approved`/`rejected`,
enum-validated, defaulting to `pending`), plus an `IsApproved()` method so
every caller asks the same one question the same one way rather than
comparing the string literal directly. `machine.proto`'s `Machine.approved`
(field 8) is replaced the same way: `reserved 8; reserved "approved";` plus
a new `MachineApprovalState approval_state = 11` enum field, mirroring the
CRD's three states exactly.

This is a real breaking change to both the CRD and the wire type, pre-
authorized for exactly this reason (pre-release, nothing depends on the old
shape yet). `make proto-breaking` reports exactly one breaking finding, the intended one
(`Previously present field "8" with name "approved" on message "Machine" was
deleted`); the new `approval_state` field and `StreamNodeLogsRequest.service`
(below) are pure additions.

Every caller of the old `Spec.Approved`/`Machine.approved` moves to the new
shape with the same mechanical swap (`m.Spec.Approved` ->
`m.Spec.IsApproved()`, or setting `ApprovalState` instead of `Approved`);
none changes behavior.

### Reversibility: rejecting is not permanent

`RejectMachine` sets `ApprovalState` to `rejected` (never deletes the
object, for the same reason as before -- deleting it would let discovery
recreate it as fresh and pending, making the rejection meaningless the
moment discovery runs again; `internal/uiapi/discovery_integration_test.go`
drives the real, exported `controller.DiscoveryReconciler.Discover` against
a rejected fixture to pin this). `ApproveMachine`'s only refusal is an *already-approved*
machine; a *rejected* one is not approved, so approving it succeeds and
moves it straight to `approved` -- this is the entire mechanism by which a
rejection is undone, and it needed no new RPC or field.

Decision: **reversible**. The alternative -- a rejection that can never be
undone -- turns a misclick into a permanent dead end for that piece of
hardware, with no UI gesture this phase defines for "undo." Ichor's own
design leans the other way throughout (drift policy, lock re-acquisition,
`forceRemove`, reuse after reset): operator mistakes get a way back, not a
one-way door. The cost is that "rejected" means "not currently wanted,"
not "permanently barred" -- acceptable, since nothing in the design asks
for the stronger guarantee, and a stronger one (a reject that really can
never be undone) is easy to add later as an annotation or a second state if
a real incident ever calls for it.

`RejectMachine` refuses to touch an already-*approved* machine (removal is
the correct operation there, a different and later one); rejecting an
already-*rejected* machine is allowed and simply records the newest reason.

### `StreamNodeLogsRequest.service`: resolving architecture.md's open question

Added as an optional `string service = 3`: empty means Talos's kernel log
(`Dmesg`, the only source this RPC could reach before), any other value is
a Talos service name (`kubelet`, `etcd`, `trustd`, `apid`, `machined`, ...),
read through Talos's `Logs` API. `NodeTalosClient.StreamLogs` gained the
same parameter; `talosclient_machinery.go` branches on it, using
`constants.SystemContainerdNamespace` with `common.ContainerDriver_CONTAINERD`
for the non-empty case: that is exactly the namespace/driver
`talosctl logs <service>` resolves to with no flags given
(`cmd/talosctl/cmd/talos/{logs,root,image}.go` in the vendored
`github.com/siderolabs/talos` source), and the RPC's `id` parameter is the
service name verbatim.

**Architecture.md's open question -- "Can `os:reader` read logs, or does
the UI backend need more?" -- is answered: yes, for both RPCs this
backend uses.** `internal/app/machined/pkg/system/services/machined.go` in the same
vendored source carries the authoritative per-method role table (not
`pkg/machinery`, which ships no server-side policy at all), and both
entries read:

```go
"/machine.MachineService/Dmesg": role.MakeSet(role.Admin, role.Operator, role.Reader),
"/machine.MachineService/Logs":  role.MakeSet(role.Admin, role.Operator, role.Reader),
```

`role.Reader` is in both sets. The UI backend's read-only identity
(`role.MakeSet(role.Reader)`, `talosclient_machinery.go`) needs nothing
more for either RPC. This is recorded here, not left as a runtime surprise:
if a future Talos version narrows either table, `StreamLogs`'s error
wrapping states plainly that it was using "this backend's read-only
(role.Reader) Talos identity" when the call failed, so a `PermissionDenied`
reads as a Talos policy change, not a mystery.

## Amendment (2026-10-26): `loadBundle` was resolving the wrong namespace

`internal/uiapi/bundle.go`'s `loadBundle` read
`cluster.Namespace` to find the secrets bundle Secret, instead of this
backend's own namespace. ADR 0011 settled this exact question for
`internal/controller`'s own `loadBundle` -- a `TalosCluster` may live in
any namespace the reader watches, but Kubernetes RBAC cannot express "the
Secret this `TalosCluster` names, wherever it happens to live" at
anything narrower than cluster-wide `Secret` read, so the Secret is
resolved in the reader's own fixed namespace instead, and
`internal/uiapi/rbac_markers.go`'s own marker already only grants
`get`/`update`/`patch` on `ichor-webhook-server-cert`/
`ichor-metrics-server-cert` plus namespace-scoped `list`/`watch`/`create`
in `ichor-system` -- never a cluster-wide `Secret` read. `internal/
uiapi`'s copy of `loadBundle` never got the matching fix when ADR 0011
landed on the controller side: it read `cluster.Namespace` directly,
which is wrong for the identical reason and was invisible in every
existing test because every fixture happened to put the `TalosCluster`
and the Secret in the same namespace (`testNamespace`, "default").

A `TalosCluster` outside the backend's own namespace would have made
`GetEffectiveConfig` fail with `Forbidden` (RBAC permits reads only in
`ichor-system`); one living inside it only worked by coincidence, not
because the code resolved the right namespace on purpose.

Fixed to match `internal/controller`'s own shape exactly:
`Server.ControllerNamespace` (set from `POD_NAMESPACE` in `cmd/ichor-ui/
main.go`, the same env var `cmd/main.go` already reads for the
controller) is what `loadBundle` resolves against now, never `cluster.
Namespace`; an empty `ControllerNamespace` fails loudly
("controller namespace is empty (is POD_NAMESPACE set?)") rather than
risking a broader read than intended, the same discipline `internal/
controller/bundle.go` already has. The tests mirror
`internal/controller/bundle_test.go`: the Secret resolves in the given
namespace even when the `TalosCluster`'s differs, an empty namespace fails
explicitly, and a missing Secret names the namespace it looked in; a
`GetEffectiveConfig` test creates the `TalosCluster` and the Secret in two
different namespaces end to end.

## Amendment (2026-10-26): the `ClusterRole` had no `nodes` rule, so every `GetNode` was `Forbidden`

`node_service.go`'s
`findKubernetesNode` -- `GetNode`'s lookup of the `corev1.Node` behind a
`TalosMachine`, by `Get` on `Spec.Hostname` or, failing that, a `List` over
every `Node` matched against the Talos endpoint address -- was there from
this ADR's first version, but `rbac_markers.go` never carried a matching
`+kubebuilder:rbac` marker for `nodes`. Nothing caught it: envtest does not
enforce RBAC, so every existing test against a fake client passed
regardless of what the generated `ClusterRole` actually said; only a real
deployment's `Get`/`List` would have returned `Forbidden`, surfaced to the
UI as `CodeInternal` (`GetNode`'s own error wrapping). Fixed by adding

```go
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list
```

to `rbac_markers.go` and regenerating (`make manifests`, which calls
`manifests-ichor-ui`): `config/rbac/ichor-ui/role.yaml` and
`dist/chart/templates/rbac/ichor-ui-role.yaml` both gained the rule, in the
same `ClusterRole` as `talosclusters`/`talosconfigpatches`/`talosmachines`
-- never `namespace=`, since a `Node` is cluster-scoped in the Kubernetes
API itself and has no namespace a `Role` could qualify into. This is the
one marker in the file where that is not a choice: every other ClusterRole
rule here could, in principle, have been narrowed to a Role if some other
design decision had fixed its resource to one namespace (`secrets`'s own
`namespace=ichor-system` is exactly that, chosen once `loadBundle` stopped
reading `TalosCluster.spec.secretsRef`'s namespace -- see ADR 0011, which
also records this same shape regressing into a cluster-wide grant twice
before). `nodes` has no such escape; `get;list` only, matching exactly the
two calls `findKubernetesNode` makes and nothing wider (no `watch`, since
this package keeps no informer cache, consistent with the rest of this
ADR's "RBAC: kept out of `manager-role`" section above).

`dist/chart/chart_test.go`'s `checkNoClusterWideSecretsRead` guards only
rules naming `secrets`; it does not, and should not, fire on this `nodes`
rule.

**No mechanism catches a missing marker itself at this point.** envtest's own
non-enforcement of RBAC is a real gap -- a marker can be deleted or never
written, and every test in this package still passes -- but closing it
would mean either standing up a real API server with RBAC turned on (a
different, heavier test setup change than this fix, and one that would
affect every other `internal/uiapi` test, not just this file) or hand-
writing a second list of "every client-go type this package touches" to
diff against `rbac_markers.go`'s markers, which is exactly the kind of
parallel bookkeeping that drifts the same way the markers themselves just
did. That is not addressed in this amendment; see "Amendment: an enforcing RBAC test"
below.

## Amendment (2026-10-26): the chart's `ClusterRole` had silently fallen behind `config/`, and nothing would have caught it a third time

`dist/chart`'s templates and `config/`'s manifests have no automatic drift
check tying them together, and a hand-edit of either alone goes unnoticed.
`dist/chart/templates/rbac/ichor-ui-role.yaml` had `talosclusters` and
`talosconfigpatches` sharing one `apiGroups` entry with only `get`/`list`, while
`config/rbac/ichor-ui/role.yaml` (regenerated from `rbac_markers.go` when
`PatchService`, ADR 0035, added `create`/`update`/`delete` on
`talosconfigpatches`) had moved on. A Helm install granted none of the three:
every `TalosConfigPatch` create, update or delete `PatchService` makes came
back `Forbidden`, surfaced to the UI as `CodeInternal`.

Fixed in the chart (`dist/chart/templates/rbac/ichor-ui-role.yaml`): split
`talosconfigpatches` back into its own `apiGroups` entry carrying
`create`/`delete`/`get`/`list`/`update`, matching `config/rbac/ichor-ui/
role.yaml` exactly. The comment explaining why the `secrets` rule lives in
its own file (`ichor-ui-secrets-role.yaml`) instead was left untouched --
that split is deliberate packaging, not drift, and stays.

**The fix is a test, not a sync.** `config/rbac/ichor-ui/role.yaml` is a
generated manifest; `dist/chart/templates/rbac/ichor-ui-role.yaml` is a Helm
template carrying `{{ .Release }}` substitutions and its own grouping of
`apiGroups` entries -- there is no byte-identical form to propagate the way
`checkCRDsMatchConfig` already propagates `config/crd/bases` into the
chart's CRD templates. Hand-mirroring was the original design (this ADR's
"RBAC" section above) and it had now drifted twice (the `nodes` rule in the
previous amendment, `talosconfigpatches`'s verbs here) with no CI signal
either time. The decision is not to give up on keeping the chart in sync by
hand -- a generator can't produce Helm template syntax from a plain
manifest -- but to make a drift between them fail loudly instead of
shipping silently. `dist/chart/chart_test.go` gained
`checkUIBackendRBACMatchesConfig`: it loads `config/rbac/ichor-ui/role.yaml`
from disk, renders the chart the same way every other check in that file
already does, and compares both the `ClusterRole`'s and the namespaced
`Role`'s rules as a set of (apiGroup, resource, verb) triples -- not YAML
text, so grouping resources or verbs into one `apiGroups` entry versus
several never causes a false failure. The namespaced `Role` is matched by
rule content, not by name: `config/`'s copy names it `ichor-ui-role` (same
name as the `ClusterRole`, disambiguated only by kind), while the chart
gives it its own fixed name, `ichor-ui-secrets-role`, for the packaging
reason that file's own comment already explains; nothing about that name
difference is itself drift, so the test does not fail on it.

When the chart drifts, the check's failure message names the exact missing
grant and which file to fix, for example:

```text
chart_test.go:119: helm template ichor .: ClusterRole ichor-ui-role is
missing ichor.maesh.dev/talosconfigpatches:update (present in
../../config/rbac/ichor-ui/role.yaml); add it to
dist/chart/templates/rbac/ichor-ui-role.yaml
```

**The same check, extended to the controller's `manager-role`.** The drift
check had only looked at `ichor-ui-role`, so the same drift in the
controller's own RBAC went unseen: `config/rbac/role.yaml` (controller-gen
output) carried `talosoperations`, `talosoperations/status` and the Argo CD
`applications` rules, and the chart's `manager-role.yaml` had none of them. A Helm install would be
Forbidden on `TalosOperation` (no upgrade could run) and unable to read
`Application` state (the CNI handover could never finish).

`checkControllerRBACMatchesConfig` reuses `checkRBACRulesMatch` unchanged and
compares `config/rbac/role.yaml`'s `ClusterRole manager-role` with the
chart's `manager-role`, and its namespaced `Role manager-role` with the
chart's `manager-secrets-role` (the chart splits secrets out; chart names are
release-templated, so they are matched by suffix). Not compared, on purpose:
`leader-election-role`, `metrics-auth-role` and `metrics-reader` (static
kubebuilder scaffolds, not generated from markers) and the per-CRD
admin/editor/viewer roles (end-user roles, not the controller's identity).

The shared triple also carries `resourceNames`; without it a name-scoped
grant for one Secret hid a missing one for another (the chart's
`manager-secrets-role` lacked `get/patch/update` on `ichor-metrics-server-cert`,
which `cmd/main.go`'s cert rotator needs).

## Amendment: an enforcing RBAC test

`GetEffectiveConfig` needs `list` on `talosstorages`: `render.ResolveSystemInputs`
lists `TalosStorage` as the ServiceAccount (ADR 0050), so `rbac_markers.go`
grants `talosstorages` `list` (no `get`, no `watch`: one non-cached List per
call). Every `Get`/`List` in `internal/uiapi` and the render inputs it calls
(`ResolveSystemInputs`, `ResolveRegistryAuth`, `secretsbundle.Load`) needs a
grant: Secrets are `get` in the controller namespace, `argocd-cm` and
Applications are granted.
`internal/uiapi/rbac_enforced_test.go` loads `config/rbac/ichor-ui/role.yaml`
into envtest (whose apiserver enforces RBAC) and runs `GetEffectiveConfig` as a
client impersonating `system:serviceaccount:ichor-system:ichor-ui`, so a read
the generated role does not grant fails the suite with Forbidden. A new render
input needs a marker, and a handler added to that spec is covered.

Amendment 2026-11-05 (ADR 0130): no reason is asked for on approve or reject. `reason` on `RejectMachineRequest`, `ApproveMachineRequest` and `AuditRecord`'s typed use is deprecated and never read; a rejection records the actor and the time only, and a rejected machine that is rejected again simply records the newest decision.

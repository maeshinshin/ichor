# 0011. Discovery does not choose its own candidates, and never auto-registers a weak identity

Status: Accepted · Date: 2026-10-25 · Design section: "Nodes" (discovery and approval)

Decided: Made `TalosMachineSpec.Role` optional; discovery never picks a role or auto-registers a weak identity

## Context

Phase 3 asked for the half of "Nodes" that runs before approval: read a
maintenance-mode node's hardware identity over the insecure connection
maintenance mode offers, and turn a strong identity into a pending
`TalosMachine` that approval later acts on. Two things had to be settled
before any of that could be written.

**Where does a candidate endpoint come from?** "Nodes" lists two options --
"registered by IP or found by scanning a subnet" -- but nothing in the API
types or the controller package picks one, and scanning a subnet is
explicitly out of this phase's scope (it is its own piece of design: which
interfaces, which CIDRs, how a result is deduplicated against a machine
that already has a `TalosMachine`). Deciding it here would be choosing a
design the task handing this phase down had already flagged as undecided.
`DiscoveryReconciler.Discover` therefore takes the endpoint as a plain
`string` argument. Whatever supplies it -- an operator registering one by
IP, a future subnet scanner, a CLI command -- calls `Discover` the same way;
nothing about this phase's shape commits to one of them. **This is handed
back to the design, not decided here.**

(Resolved below, in "Discovery probes only declared ranges": the answer is
neither free-form registration nor a scanner, but a declared range on
`TalosCluster.spec.discovery` that `EnumerateCandidates` expands and
`DiscoverAll` drives through `Discover` unchanged.)

**Is `TalosMachineIdentity` sufficient?** Yes, as given: `uuid` (required),
`serial` and `macAddresses` (both optional) already hold exactly the tuple
"Nodes" defines, and optional fields are the right shape for a key that can
be weak. `Approved` on `TalosMachineSpec` already carries the approval gate
`TalosMachineReconciler.Reconcile` already enforces (its first line: an
unapproved machine is left untouched, no status write, nothing). Nothing
needed adding to either.

**`role`, on the other hand, did not fit.** It was `+required` with an enum
of `controlplane`/`worker`, but discovery creates the `TalosMachine` *before*
approval assigns a role ("Nodes": approval "sets the role, labels, disk
roles and basic networking"). A required field with no value yet is a type
that cannot represent the state this phase needs to create. `role` is now
`+optional`; nothing else on the identity or approval types changed.

## Decision

1. `TalosMachineSpec.Role` becomes optional. A discovered, unapproved
   machine has no role until approval sets one; `pkg/render` already fails
   closed on an empty role (via `machine.ParseType`), so an operator who
   approves without setting a role gets a clear `RenderFailed` condition
   rather than a silently wrong config.
2. `DiscoveryReconciler.Discover(ctx, cluster, endpoint)` reads identity over
   a `MaintenanceClient` (a new, three-line interface mirroring
   `TalosClient`: one method, `Identity`, dialed through
   `client.WithMaintenanceMode` -- the same insecure, unauthenticated
   connection "Nodes" specifies) and classifies what it finds:
   - **Weak** (`strongIdentity` is false: UUID, serial or every permanent
     MAC missing) -- never auto-registered. `Discover` records a
     `WeakIdentity` Warning Event on the `TalosCluster` (address, whatever
     of UUID/serial/MACs/manufacturer were read) and creates nothing.
   - **Strong, UUID unseen** -- creates a new `TalosMachine` named after the
     UUID, `Approved: false`, `Role` unset.
   - **Strong, UUID already named by an existing `TalosMachine` with the
     *same* serial and MAC set** -- the same machine returning. `Discover`
     returns the existing object; nothing is created or modified.
   - **Strong, UUID already named by an existing `TalosMachine` with a
     *different* serial or MAC set** -- a collision "Nodes" says Ichor
     cannot resolve by itself (the UUID "may repeat"). `Discover` records an
     `IdentityCollision` Warning Event and leaves the existing machine
     untouched; nothing new is created.
3. The object's name is the UUID itself. SMBIOS UUIDs are already
   lowercase, hyphenated hex -- a valid Kubernetes name -- and using it
   turns "does a `TalosMachine` already exist for this identity" into a
   single `Get`, which is also what makes the collision case detectable at
   all: a `List`-and-filter approach would have no single place to apply
   "same name, different hardware."
4. Neither outcome ever sets `Approved`. The configuration reconciler's
   existing first line -- untouched by this change -- is therefore the only
   place that decides whether a machine gets configuration, and it already
   refuses anything unapproved.

## Consequences

- No new CRD, no new abstraction beyond the two small interface types
  (`MaintenanceClient`, `HardwareIdentity`) `talosclient.go` already has a
  direct precedent for.
- A weak or colliding identity leaves no `TalosMachine` behind at all. The
  only record is the Event on the `TalosCluster`. An operator resolving it
  has to go look at cluster Events, not a dedicated list -- an acceptable
  gap for this phase, since the approval UI that would normally surface this
  is explicitly later work ("Nodes", "Roadmap" phase 5).
- Because discovery never sets `Approved` and never loads the secrets
  bundle, it cannot be the path through which an unintended node gets
  configuration: the only writer that applies anything is
  `TalosMachineReconciler`, gated on `Approved`, which this phase does not
  touch.
- Discovery's candidate-selection question stays open. Whoever answers it
  next (registration CLI, subnet scanner, or both) implements it as a
  caller of `Discover`, not a change to `Discover` itself.

## A second finding in the same file: the bundle Secret's namespace

While working in `internal/controller/talosmachine_controller.go`, a
cluster-wide `ClusterRole` rule granting unrestricted `get;list;watch` on
every `Secret` in every namespace was found reintroduced by commit
`645f5be` ("feat(controller): reconcile TalosMachine configuration"),
which added `loadBundle`'s read of `TalosCluster.spec.secretsRef`. Commit
`823b04f` had already removed an identical rule once, for a different
Secret (`ichor-webhook-server-cert`), by moving it into a namespace-scoped
`Role`. `645f5be`'s marker --

```go
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
```

-- regenerates the same cluster-wide rule, confirmed by inspecting
`config/rbac/role.yaml` after `make manifests`.

### Why this one could not be narrowed the same way

`823b04f`'s fix works because `ichor-webhook-server-cert` always lives in
the controller's own namespace. The bundle Secret did not: `loadBundle`
resolved it in `cluster.Namespace` -- *whichever namespace that particular
`TalosCluster` happens to live in* -- and `test/e2e/e2e_test.go` creates its
`TalosCluster` in `ichor-e2e`, not the controller's namespace, proving this
is not hypothetical. A `Role` scoped to one fixed namespace can never grant
access to a Secret whose namespace varies per `TalosCluster`; Kubernetes RBAC
has no way to express "the Secret this field names, wherever its owner
lives" below cluster scope.

### Decision: resolve the bundle Secret in the controller's own namespace, not the `TalosCluster`'s

`### Deployment` already describes the default placement:

> The default is self-managed: Ichor runs inside the cluster it manages.
> ... A hub mode, where Ichor manages clusters from outside, is left open
> by the API but not built first.

One controller, one namespace, by default. Resolving `secretsRef` against
*that* namespace rather than the `TalosCluster`'s turns the open question
above into a non-issue: the Secret's location is now fixed and known at
manifest-generation time, so the RBAC marker can carry
`namespace=ichor-system` (the same literal the webhook certificate markers
already use) and `make manifests` emits a namespace-scoped `Role`, not a
`ClusterRole`.

This is a change in meaning for a public field, not just an
implementation detail: `TalosClusterSpec.SecretsRef` used to mean "the
Secret in this TalosCluster's namespace" and now means "the Secret in the
controller's own namespace." The `TalosCluster` itself is unaffected -- it
may still live in any namespace the controller watches; only the bundle
Secret's location changed. The field's doc comment in
`api/v1alpha1/taloscluster_types.go` says so.

`loadBundle` takes the controller's namespace as an explicit parameter
(`TalosMachineReconciler.ControllerNamespace`, set from `POD_NAMESPACE` in
`cmd/main.go`, the same downward-API variable the webhook certificate
rotator already uses) and fails outright if it is empty, rather than
letting an empty namespace resolve against every namespace by accident.

`dist/chart/chart_test.go` gained a standing guard,
`checkNoClusterWideSecretsRead`, asserting that no rendered `ClusterRole`
grants `get`, `list` or `watch` on `secrets` without `resourceNames`. Run
against the regression this ADR describes (the marker without
`namespace=`), it fails with:

```
ClusterRole "ichor-manager-role" grants [get list watch] on secrets across every namespace with no
resourceNames; the bundle Secret is resolved in the controller's own namespace (ADR 0011), so this
permission must live in a namespace-scoped Role instead
```

`rbac.namespaced`'s default is unchanged (`false`): that toggle scopes
*every* rule in `manager-role`, not only the secrets one, to the release
namespace, and nothing about this decision needs the rest of the
controller's RBAC narrowed.

### Consequence for hub mode

This decision is deliberately tied to the self-managed default. If hub
mode (`TalosCluster` objects managed from outside the clusters they
describe, "Deployment") is ever built, "the controller's own namespace"
stops being a safe place to assume every managed cluster's bundle lives --
a hub managing clusters across teams or tenants is exactly the case a
single shared namespace of Secrets was meant to avoid. Hub mode's design
has to revisit this RBAC shape explicitly: a namespace-scoped `Role` per
managed namespace (one per tenant), or an explicit opt-in `ClusterRole`
for operators who accept the wider blast radius -- not a silent
cluster-wide grant by default. This is called out here so that whoever
designs hub mode does not have to rediscover it.

## A third finding: `role` must not be silently defaulted

`TalosMachineSpec.Role` becoming optional (this ADR's main decision) raised
the question of what happens when an *approved* machine still has no role
-- discovery never sets one, and approval is not built yet. The assumption
when `Role` was loosened was that `pkg/render`'s existing
`machine.ParseType` call would fail closed on an empty string.

It does not. Talos machinery's `machine.ParseType("")` returns
`TypeWorker, nil` -- not an error -- because it exists to parse config
documents, where an absent `machineType` field has always defaulted to
worker. Used on `TalosMachineSpec.Role` directly, that default is wrong: an
approved machine with no role would have silently rendered (and applied) a
worker configuration instead of failing visibly.

`pkg/render.Render` now checks `m.Spec.Role == ""` explicitly, ahead of
`ParseType`, and fails with `"render: machine role is not set"`. The
reconciler's existing `RenderFailed` condition path (unchanged) is what
surfaces this to an operator, the same way any other render failure does.
`pkg/render/render_test.go`'s `TestRenderBundleLeak` gained an `"empty
role"` case alongside its existing `"unparseable role"` one, confirming the
explicit failure and that no bundle material leaks into its message.

Enforcing this earlier -- at admission, when a machine is approved without
a role -- was considered and not done: `TalosMachineCustomValidator` is
registered for `verbs=delete` only (ADR 0006, guarded by
`chart_test.go`'s `checkWebhookInvariants`), and widening it to
create/update is a bigger, deliberate scope change that ADR made on
purpose. The render-time check is sufficient for this phase's guarantee --
nothing ever applies a worker configuration to a machine that was not
explicitly given that role -- without reopening ADR 0006.

## Discovery probes only declared ranges

This ADR's opening section left one question unanswered on purpose: where
a candidate endpoint for `Discover` comes from. It is answered now.

### Decision: `TalosCluster.spec.discovery` declares addresses and CIDRs; empty means off

`spec.discovery` (`TalosClusterDiscovery`) holds two optional lists,
`addresses` and `cidrs`. Both empty -- the default, on every `TalosCluster`
that doesn't set the field at all -- means discovery is disabled: nothing
in `EnumerateCandidates` or `DiscoverAll` ever opens a connection. **Ichor
never discovers a range by itself.** No subnet scan, no listening for a
beacon, no default range: every candidate traces back to something an
operator wrote into the CR. `EnumerateCandidates` pairs each declared
address with Talos's fixed `apid` port (`constants.ApidPort`, 50000) --
the only port this ever dials; `DiscoverAll` then calls the existing
`Discover` once per candidate, unchanged from this ADR's first section.

Three reasons, the third the deciding one:

1. **A CR per machine would make creating the CR the approval.** "Nodes"
   describes a pending-approval list a human acts on; a machine has to be
   able to appear on it without a human operation. A declared range (one
   CR, written once, well before any specific machine exists) keeps
   discovery and approval two separate steps; a CR-per-candidate scheme
   collapses them into one.
2. **An unbounded scan opens connections on networks nobody told the
   tooling about**, and is unreliable besides (ARP timeouts, firewalls,
   mixed-MTU segments all behave differently across a scan Ichor didn't
   choose the shape of).
3. **`### Trust` already assumes a separate provisioning network exists**:
   "A separate provisioning network is assumed; the docs say so." A
   declared CIDR or address list *is* that network, named explicitly. This
   decision adds no new assumption to the design -- it gives the API a
   place to write down an assumption the design already made.

### Validation lives in the CRD, not the controller

Both fields are CEL-validated (`k8s.io/apiserver`'s IP/CIDR CEL library,
available to CRD `x-kubernetes-validations` by default -- confirmed against
envtest's Kubernetes 1.36): `addresses` must each satisfy `isIP(a) &&
ip(a).family() == 4`; `cidrs` must each satisfy `isCIDR(c) &&
cidr(c).ip().family() == 4 && cidr(c).prefixLength() >= 24`. IPv6 is
rejected on both -- consistent with the design's own non-goals ("IPv6 ...
Later, maybe") and avoiding a /24-sized numeric threshold that would mean
something absurd for IPv6 (a /24 of IPv6 is still 2^104 addresses).
`family()` and `prefixLength()` return CEL `int`, not string, which is not
obvious from the function names and cost an iteration to find (envtest
rejected `== '4'` with a type error before accepting `== 4`).

A naive `self.all(...)` rule on an unbounded list is itself rejected by the
API server's own CEL cost estimator ("estimated rule cost exceeds budget"),
because the estimator assumes worst-case string length when none is
declared. `+kubebuilder:validation:items:MaxLength` (not plain `MaxLength`,
which controller-gen refuses on a slice field: "must apply maxlength to a
textual value, found type array") on each list brings the estimate back
under budget.

### Why a /24 cap on `cidrs`, and why a cap at all

A cap exists. `cidrs` accepts at most four entries (`MaxItems=4`), each
`/24` or smaller (`prefixLength() >= 24`, so at most 256 addresses);
`addresses` accepts at most 64 entries directly. A `/16` is 65536
addresses and a `/8` is 16777216 -- both large enough to turn one
`TalosCluster` edit into a connection-attempt budget no fixed concurrency
and timeout combination keeps politely bounded, and both far bigger than
any single provisioning network a rack or two of hardware actually needs.
Capped and rejected at admission (not accepted and left to the controller
to cope with) because the CRD is the layer that can refuse before anything
runs, the same reasoning `MaxItems` already uses elsewhere in this schema.

Worst case with the caps: 64 + 4×256 = 1088 candidates.

### Rate limiting: fixed values, not settings

`discoveryConcurrency = 32` and `discoveryDialTimeout = 2 * time.Second`,
both in `internal/controller/discovery.go`, neither exposed as a field
anywhere -- nobody asked for that knob, and a fixed worst case is the
property that matters, not tunability. The per-candidate deadline covers
the *whole* of one `Discover` call: the insecure maintenance-mode dial and
identity read, *and* the Kubernetes API `Get`/`Create` that follow for a
strong identity. That is deliberate, not an oversight: an earlier version
bounded only the dial, at 750ms, and `go test`'s own parallel package run
(every package's envtest instance competing for the same CPU) occasionally
made the Kubernetes API calls alone take longer than that, which
`DiscoverAll` then silently treated as "unreachable" -- turning ordinary
API latency into a lost discovery. 2s absorbs that without meaningfully
changing the worst case: 1088 candidates / 32 workers × 2s ≈ 68 seconds,
still well short of "hangs."

### Silence is the point, not a gap

`DiscoverAll` does not log or event per unreachable candidate, and neither
does `Discover`'s own connect/read error paths (checked again here: they
return a plain `error`, nothing is logged on that branch). A declared
provisioning network answers on a handful of addresses and says nothing on
the rest, by construction -- that is what "separate provisioning network"
means in practice, not every address having a machine behind it. Reporting
every miss would make the one candidate worth an operator's attention
indistinguishable from a thousand that were never going to answer.

### Tests never reach a real socket

Every test in `internal/controller/discovery_test.go` supplies its own
`MaintenanceClient` factory; none ever constructs
`NewMachineryMaintenanceClient`, the only thing in this codebase that
calls `client.WithMaintenanceMode` and actually dials. Addresses used in
tests are RFC 5737 TEST-NET-1 (`192.0.2.0/24`) and TEST-NET-2
(`198.51.100.0/24`) -- documentation-only ranges that are never real
networks -- on top of the factory-level guarantee, not instead of it. The
concurrency-bound and timeout tests simulate latency with `time.Sleep` and
`<-ctx.Done()`, never a dial.

### What is still open

`DiscoverAll` is not wired into `cmd/main.go` or any `ctrl.Manager` trigger.
*Which* candidates to probe is now decided; *when* `DiscoverAll` runs for a
given `TalosCluster` -- on a timer, on a watch event, from a CLI command --
is a separate question this phase still does not answer, for the same
reason the original endpoint question was deferred: it is its own design
decision, not implied by anything settled here.

- Addendum (ADR 0086): the same identity answering in maintenance mode at a new address is recorded in `status.observedEndpoint`, never in `spec.endpoint`.
- Addendum (ADR 0108): a `TalosMachine`'s `spec.endpoint` must be an address of `spec.discovery` (or the user needs `update` on `talosmachines/endpoint`), and approving needs `approve` on `talosmachines/approval`. Discovery-created machines are unaffected.

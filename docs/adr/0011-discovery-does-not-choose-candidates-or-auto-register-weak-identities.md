# 0011. Discovery does not choose its own candidates, and never auto-registers a weak identity

Status: Accepted · Date: 2026-10-25 · Design section: "Nodes" (discovery and approval)

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

## A second, unrelated finding in the same file

While working in `internal/controller/talosmachine_controller.go`, a
regression was found in its RBAC markers, not introduced by this phase but
sitting in the same file this phase was already editing.

### Context

Commit `823b04f` ("fix: narrow controller RBAC off cluster-wide secrets
access") removed a `ClusterRole` rule granting unrestricted
`get;list;watch` on every `Secret` in every namespace, moving the one Secret
the controller actually needs (`ichor-webhook-server-cert`, which lives in
the controller's own namespace) into a namespaced `Role`. Commit `645f5be`
("feat(controller): reconcile TalosMachine configuration"), which added
`loadBundle`'s read of `TalosCluster.spec.secretsRef`, reintroduced the
identical marker:

```go
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
```

`make manifests` turns this into the same cluster-wide `ClusterRole` rule
823b04f removed, confirmed by inspecting the regenerated
`config/rbac/role.yaml`.

### Why the obvious fix (`namespace=ichor-system`) does not work here

Adding `namespace=ichor-system` to the marker -- the fix that worked for the
webhook certificate Secret -- was proposed and checked against this
codebase before applying it, because the two Secrets are not alike:
`ichor-webhook-server-cert` always lives in the controller's own namespace
(`POD_NAMESPACE`), but the Secret `loadBundle` reads is named by
`TalosCluster.spec.secretsRef` and looked up in `cluster.Namespace` --
*whatever namespace that particular `TalosCluster` lives in*, which is not
fixed to the controller's install namespace.

This is not hypothetical: `test/e2e/e2e_test.go` creates its `TalosCluster`
in the `ichor-e2e` namespace (`const namespace = "ichor-e2e"`), with
`SecretsRef: corev1.LocalObjectReference{Name: "ichor-e2e-secrets"}` --
a `LocalObjectReference`, resolved in `ichor-e2e`, not `ichor-system`. A
`Role` scoped to `namespace=ichor-system` can never grant access to that
Secret; applying the proposed fix as given would have made `loadBundle`
return `SecretsBundleUnavailable` for every `TalosCluster` not created in
the controller's own namespace, the e2e suite's own cluster included.

The companion suggestion -- flip the Helm chart's `rbac.namespaced` default
to `true` -- was checked the same way and has the same problem at a larger
scale: that toggle (a stock kubebuilder/Helm-plugin setting, not specific to
secrets) turns *every* rule in `manager-role`, not only the secrets one,
into a `Role` scoped to the release namespace. Defaulting it to `true` would
silently stop the controller from managing any `TalosCluster` or
`TalosMachine` outside its own install namespace by default -- a much larger
behavioural change than narrowing one Secret rule, and one this phase's
author is not positioned to decide unilaterally.

### Decision

Not resolved here. Kubernetes RBAC cannot express "the Secret named by
`cluster.Spec.SecretsRef` in `cluster.Namespace`, for any `TalosCluster` the
controller is watching" at anything narrower than cluster scope, *given the
current design*, where a `TalosCluster` (and so its bundle Secret) may live
in any namespace. Closing this gap for real needs one prior decision this
ADR defers rather than makes: either (a) restrict `TalosCluster` to the
controller's own namespace -- which breaks the multi-namespace pattern the
e2e suite already exercises and needs its own sign-off, webhook
enforcement, doc and sample updates -- or (b) accept cluster-wide
`get;list;watch` on Secrets as consistent with "Trust"'s own statement that
the controller already holds an `os:admin`-equivalent Talos identity and is
the most privileged component in the system, and look for mitigation
elsewhere (NetworkPolicy, a narrower ServiceAccount boundary, audit alerts
on unexpected Secret reads) instead of RBAC.

No code, chart or test changes were made for this half of the finding. The
marker in `talosmachine_controller.go` is unchanged from `645f5be`; dist/chart
was touched only for the `role` field sync this ADR's other half needed,
not for RBAC. This finding and the evidence above are handed back to the
design for the real decision.

# 0009. Reboot necessity is predicted, because Talos will not answer it

Status: Accepted · Date: 2026-10-25 · Design section: 5.6 (configuration reconcile)

Decided: Keep predicting reboot necessity from `pkg/fieldmeta`'s field-path table; Talos itself won't answer the question

## Context

`pkg/fieldmeta` predicts, from a small table keyed by field path, whether a
configuration change needs a node reboot. It defaults to reboot-required and
carries eight hand-written classifications. Its own author flagged the obvious
weakness: those eight are Talos domain knowledge, so a machinery bump cannot
tell us when Talos quietly changes the behaviour underneath them.

Reading machinery v1.14.3's API suggested the prediction might be unnecessary.
`ApplyConfigurationRequest.Mode` has `AUTO`, with `REBOOT` deprecated in favour
of it; `DryRun` is a bool on the same request; and the response carries `Mode`
("States which mode was actually chosen"), `ModeDetails` and `Warnings`. That
reads exactly like Talos offering to answer the question itself, per node and
per version, which would be strictly better than predicting it.

It was verified before anything was deleted, and the answer is no.

**`AUTO` is a hardcoded alias for `NO_REBOOT`.** From Talos v1.14.3,
`internal/app/machined/internal/server/v1alpha1/v1alpha1_server.go`:

```go
case machine.ApplyConfigurationRequest_AUTO:
    in.Mode = machine.ApplyConfigurationRequest_NO_REBOOT
    fallthrough
```

Nothing in that RPC ever sets `Mode = REBOOT`, and the switch never looks at
the diff. `ModeDetails` is a static string. Confirmed empirically against a
real node: three dry runs — one changing only a DNS nameserver, one changing
only the installer image tag, one applying a whole worker configuration —
returned byte-identical `Mode` and `ModeDetails`.

The real decision lives elsewhere. Each subsystem's own controller reacts to
the already-written configuration asynchronously and asks for a reboot through
a `RebootRequest` resource, which a separate controller consumes. There is no
point in the API where Talos summarises that into "this change needs a
reboot".

## Decision

Keep `pkg/fieldmeta`, and keep predicting. Its table is the only mechanism that
can answer the question where Ichor needs it answered.

Two places need it, and neither can dry-run:

- **A validating webhook on `TalosConfigPatch`, at admission time.** There may
  be no node to ask: the cluster may not be bootstrapped, the target machine
  may not exist, and admission has to answer synchronously anyway.
- **The reconciler, before it applies.** Design 5.6 step 4 requires taking the
  cluster lock, running the node-operation hook and draining *before* a
  reboot-causing apply. Learning the answer from Talos after applying is too
  late by construction.

So the design's wording — the mode is derived from the diff — stands, and now
has evidence behind it rather than being the only idea anyone had.

Two findings from the same investigation are worth acting on separately:

- **`Warnings` on the apply response is genuinely useful**, just not for this.
  It comes from `ValidateAtRuntime` and surfaces deprecated-field migration
  hints, missing cross-document configuration (etcd encryption on a control
  plane, a cluster ID or secret missing when discovery is on) and
  `KubeNodeConfig` label mismatches. The reconciler should surface it in status
  rather than dropping it.
- **Dry run is genuinely side-effect free.** Verified: a node's
  `MachineStatus` version and timestamp were unchanged after a batch of dry
  runs, not even rewritten with the same value. So it is safe to use for the
  impact preview's *validation* half, even though it cannot answer the reboot
  half.

## Consequences

- The eight hand-written entries stay, with the risk their author named: a
  Talos behaviour change will not announce itself. The mitigation is the
  default — anything not in the table is reboot-required — so the failure mode
  of a stale table is an unnecessary reboot, not a node left running a
  configuration it does not have.
- Whenever the pinned Talos version moves, those eight are worth re-reading
  against the release notes. That is a cheap, bounded review, and better than
  the alternative of trusting an API that does not answer.
- The reconciler should pass `DryRun` first for validation and warnings, then
  apply for real — two calls, with the mode decided by the table in between.

## A note for the e2e harness, found along the way

A real apply of a full worker configuration to a node in maintenance mode
starts Talos's `UnattendedInstallController`, which installs to disk and then
asks for a reboot through `RebootRequest`. On the KubeVirt harness both test
VMs went unreachable at that point — TCP resets rather than timeouts, so the
kernel was up with nothing listening — and did not return within the test
window. Cluster pods were confirmed to have egress to `factory.talos.dev`, so
it is not obviously a network block, and it was not root-caused.

**Resolved.** It was not KubeVirt and not the install: Talos's certificate
controller blocks apid on a reachable control-plane endpoint for *worker*
nodes only, so an isolated worker never starts apid at all. A control-plane
config comes up normally. The details, and the four other harness
requirements the same investigation turned up, are in ADR 0007.

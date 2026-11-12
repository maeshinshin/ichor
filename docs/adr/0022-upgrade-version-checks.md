# 0022. pkg/upgrade: version checks machinery already answers, and the ones it doesn't

Status: Accepted · Date: 2026-10-25 · Design section: "Upgrades"

Decided: Delegate the Talos/Kubernetes compatibility matrix to `pkg/machinery/compatibility` instead of reimplementing it

## Context

Design section "Upgrades" lists, among the per-node pre-checks for a Talos upgrade, "the
supported upgrade path and the Kubernetes pairing," and separately requires Kubernetes upgrades
to move "one minor at a time within the skew policy." It also names the first thing to evaluate
before writing any of this: "Reusing talosctl's `upgrade-k8s` logic is the first thing to
evaluate."

This ADR covers only the calculable half of that: given version strings, is a transition
supported. It does not cover the state machine, `TalosOperation`, or anything that drives an
actual upgrade against a running cluster — that is separate work.
Two pre-checks genuinely need a running cluster (etcd health, node readiness); this package
neither implements nor declares them, on purpose (see Decision).

## Evaluating `upgrade-k8s` reuse

`talosctl upgrade-k8s`'s logic lives in `github.com/siderolabs/talos/pkg/cluster/kubernetes`
(`upgrade.go`), which in turn is built on `github.com/siderolabs/go-kubernetes/kubernetes/upgrade`
— a separate, small, public Go module (not `internal/`), at `v0.2.41` as pinned by Talos v1.14.3's
own `go.mod`. It is genuinely reusable code, not internal-only, so the question is whether to
depend on it, and the answer is no, for two independent reasons:

- **Wrong shape.** `upgrade.Checks` (`checks.go`) takes a COSI `state.State`, a `*rest.Config` for
  the Kubernetes API, and node lists, and talks to both directly (`k8s.io/client-go/dynamic`,
  `k8s.io/apimachinery`). `pkg/upgrade` is specified to be a pure package — version strings in,
  an error out — so importing this would import a stateful, cluster-connected tool into a package
  whose whole point is not being one.
- **Wrong granularity, for the one piece that would otherwise fit.** `upgrade.Path.IsSupported()`
  (`path.go`) is pure (just a `switch` on `"major.minor->major.minor"` strings) and would have
  been the reusable part. But it hardcodes an explicit, flat list of every allowed pair from
  `1.19->1.19` through `1.37->1.37`, one entry per Kubernetes minor released since go-kubernetes
  started tracking this. Every entry is the identical "same minor, or exactly one minor forward"
  rule — there is no exception anywhere in the list — so depending on it would mean taking on a
  module that needs bumping just to recognize a Kubernetes release it doesn't enumerate yet,
  for a rule with no actual per-version content to reuse.

`KubernetesSkew` (`kubernetes_skew.go`) reimplements the identical rule generically instead,
using `golang.org/x/mod/semver` (already an indirect dependency, now direct) rather than
`github.com/blang/semver/v4` (what `go-kubernetes` and `pkg/machinery/compatibility` both use) —
no reason to prefer one here, `x/mod/semver`'s string-in/string-out API needed less wrapping for
this one comparison.

## Decision

**The Talos upgrade path and the Kubernetes/Talos pairing are not reimplemented at all.**
`pkg/machinery/compatibility` (already a dependency, ADR 0003) carries both, per Talos minor
release, maintained by Talos itself:

- `TalosVersion.UpgradeableFrom(host *TalosVersion) error` — the actual rule, which is a minimum
  host version, a maximum host version for downgrades, and an explicit deny-list, **not** a
  generic "one minor at a time" rule. Talos 1.14's own compatibility
  data (`compatibility/talos114/talos114.go`) sets `MinimumHostUpgradeVersion = 1.12.0`, meaning
  `1.12 -> 1.14` is a supported upgrade that skips `1.13` entirely. `pkg/upgrade.TalosPath` is a
  thin wrapper: parse both versions with `compatibility.ParseTalosVersion`, call
  `to.UpgradeableFrom(from)`.
- `KubernetesVersion.SupportedWith(target *TalosVersion) error` — the Kubernetes/Talos pairing,
  same per-minor data (`talos114.MinimumKubernetesVersion` = `1.32.0`,
  `MaximumKubernetesVersion` = `1.37.99` for Talos 1.14). `pkg/upgrade.KubernetesPairing` wraps it
  the same way.

**The Kubernetes skew policy is reimplemented, generically, in `pkg/upgrade.KubernetesSkew`**:
same major version, minor moves by at most one step, never backwards. Sourced from
[kubernetes.io's version skew policy](https://kubernetes.io/releases/version-skew-policy/)
("the relative version skew between control plane components ... must differ by no more than one
minor version") and consistent with `go-kubernetes`'s own hardcoded table (every pair in it is exactly
this rule, as established above).

**The two cluster-dependent pre-checks (etcd health, node readiness) are not declared here at
all — not as an implementation, and not as an interface either.** Declaring
`EtcdHealthChecker`/`NodeReadinessChecker` interface types with no implementation and no caller,
just to name the shape, was rejected. Go's own
convention is that interfaces are declared by the consumer, at the point of consumption ("accept
interfaces, return structs") — the consumer here is the upgrade state machine, which does not
exist yet. Declaring the interface anyway means guessing both its method set and where it
belongs before anything calls it: whoever writes that state machine will declare exactly the
methods they call, in their own package, and there is no reason to expect `EtcdHealthy(ctx)
error` matches what they actually need (per node or cluster-wide? a bool, an error, or a
structured result? synchronous or progress-reporting?). Deleting it costs nothing the design
still needs: "pre-checks cover etcd health ... and node readiness" stays exactly as said in
design section "Upgrades", unchanged by this ADR either way — what the paragraph above describes
is a requirement on the eventual state machine, not a Go declaration this package owed it.
`Impact`-style result aggregation (the shape `pkg/fieldmeta.Assess` uses) was also considered and
not added, for an unrelated reason: the three pure checks (`TalosPath`, `KubernetesPairing`,
`KubernetesSkew`) answer three genuinely different questions with different inputs (Talos-to-
Talos, Kubernetes-to-Talos, Kubernetes-to-Kubernetes), so there is no single changed-paths-style
list to aggregate over the way fieldmeta's field paths are; combining them into one call would
just be a tuple of three independent answers wearing a struct.

## Consequences

- `pkg/upgrade` has no version-compatibility data of its own to maintain. A Talos release that
  changes a supported path, a Kubernetes pairing, or (per Kubernetes' own policy, essentially
  never) the skew rule shows up the moment machinery is bumped, for the first two, with zero
  code change here.
- `golang.org/x/mod` is a direct dependency; no new module is added.
- Callers (the upgrade state machine) get three independent pure functions, and decide for
  themselves how to sequence and aggregate them.

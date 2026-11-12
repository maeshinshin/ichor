# 0033. `strongIdentity` and `loadBundle`: two call sites, one rule each

Status: Accepted · Date: 2026-10-26 · Design section: Nodes, Trust

Decided: moved the identity-tuple rule to `TalosMachineIdentity.IsStrongIdentity` in `api/v1alpha1`, and the secrets-bundle Secret read/decode to a new `internal/secretsbundle` package, so `internal/controller` and `internal/uiapi` each still call it from their own, separately-justified call site, but neither keeps its own copy of the rule.

## Context

Both `internal/controller` and `internal/uiapi` independently implemented
two of the same checks:

- `strongIdentity`: ADR 0011's identity-tuple rule (SMBIOS UUID, serial,
  at least one permanent MAC address), checked once by discovery
  (`internal/controller/discovery.go`, against a freshly discovered
  `HardwareIdentity`) and once by approval
  (`internal/uiapi/identity.go`, against a `TalosMachine`'s already-recorded
  `spec.identity`). **Having two call sites is correct and deliberate**:
  approval must not depend on discovery having already enforced the rule --
  a `TalosMachine` created any other way (a future registration path, a
  hand-written fixture, a test) is not guaranteed to have gone through
  discovery, so approval is the backstop that has to re-check. The problem
  was that each call site carried its own copy of the three-line rule
  itself. A rule that guards an approval gate drifting between two copies
  is not a cosmetic problem: if one copy gains a condition the other
  doesn't, the UI can approve a machine the controller would have refused
  as weak, and nothing detects it -- there is no test that compares the
  two rules against each other, only tests that each rule matches its own
  copy's intent.

- `loadBundle`: reads the Secret `TalosCluster.spec.secretsRef` names and
  decodes it as a `secrets.Bundle`, once in `internal/controller/bundle.go`
  (to apply configuration) and once in `internal/uiapi/bundle.go` (to
  render an effective-config preview and dial a read-only Talos client).
  `internal/uiapi`'s own doc comment argued the duplication was safe
  because a drift's symptom would be "loud" -- a decode failure. That
  argument covers a *decoding* drift, but not a *leak-safety* drift:
  `internal/controller/bundle.go`'s own doc comment states the actual
  invariant that matters -- "every error path below is structural ... and
  never includes the Secret's data in its message" -- and a copy that
  quietly stopped upholding that would fail *silently*: the Secret's own
  key material would start appearing in an error message, logged or shown
  somewhere, with no test anywhere positioned to catch it, because
  `internal/uiapi` had no leak test of its own for this function at all.

## Decision

### `IsStrongIdentity`, on the CRD type

```go
func (m TalosMachineIdentity) IsStrongIdentity() bool {
	return m.UUID != "" && m.Serial != "" && len(m.MACAddresses) > 0
}
```

`api/v1alpha1` is the natural home: `TalosMachineIdentity` already lives
there, and both existing call sites work with that type one way or
another.

- `internal/uiapi/machine_service.go`'s call site already has a
  `TalosMachineIdentity` in hand (`m.Spec.Identity`), so it calls the
  method directly: `m.Spec.Identity.IsStrongIdentity()`.
  `internal/uiapi/identity.go` is deleted outright -- there is nothing left
  for that file to do -- and the rule's truth table is covered in
  `api/v1alpha1/talosmachine_identity_test.go`, its one home.
- `internal/controller/discovery.go`'s call site has a `HardwareIdentity`
  (its own, pre-CRD representation of what `Discover` read off the wire),
  not yet a `TalosMachineIdentity` -- that conversion only happens a few
  lines later, once this very check has passed. `strongIdentity(hw
  HardwareIdentity) bool` stays as a thin wrapper that builds the
  three-field struct and calls the method, so the call site
  in `discovery.go` is unchanged.

Two call sites, as before; one rule, where there were two.

### `internal/secretsbundle`, a new package under `internal/`

```go
package secretsbundle

const SecretKey = "secrets.yaml"

func Load(ctx context.Context, c client.Client, namespace, name string) (*secrets.Bundle, error)
```

Both callers are already under `internal/`, so this costs nothing in
public API surface -- it is exactly as internal as the two copies it
replaces. `Load` takes the resolved `namespace` and `name` directly rather
than a `*TalosCluster`, because the two callers resolve the namespace
differently and that choice is each caller's own to keep:

- `internal/controller/bundle.go`'s `loadBundle` always reads in the
  controller's own fixed namespace (`POD_NAMESPACE`), regardless of which
  namespace the `TalosCluster` itself lives in -- the RBAC tradeoff ADR
  0011 already made (Kubernetes RBAC cannot express "the Secret this
  `TalosCluster` names, wherever it lives" any narrower than a
  cluster-wide Secret read, so the controller fixes its own namespace
  instead and keeps the RBAC marker scoped to it).
- `internal/uiapi/bundle.go`'s `loadBundle` reads in the `TalosCluster`'s
  own namespace.

This asymmetry is deliberate and unchanged: this ADR centralizes the read-and-decode-without-leaking logic,
not the namespace-resolution policy, which is a separate question neither
caller's own code or tests raised as broken.

`SecretKey` is the one literal value now. `internal/controller` and
`internal/uiapi` each keep a package-local `bundleSecretKey = secretsbundle.SecretKey`
alias, so their test fixtures can keep keying a `corev1.Secret` on the
package-level name -- an alias cannot drift from the constant it points at,
so this is not a second definition.

### `Load` never puts the Secret's contents in an error

`Load`'s errors are structural: Secret not found, key missing, malformed
YAML. None includes the bundle's secret material (bootstrap token, trustd
token, cluster ID/secret, any cert or key) in its message. Both callers
inherit that one property from the shared decoder, rather than each
upholding it in its own copy. `internal/secretsbundle` carries the direct
tests for it, including a malformed-YAML case that checks no secret
material appears in the error.

## Consequences

- `internal/uiapi/identity.go` and its test are gone; the rule's test lives
  in `api/v1alpha1`.
- `internal/controller/bundle.go` and `internal/uiapi/bundle.go` both
  shrink to a namespace/name resolution plus a call into
  `internal/secretsbundle.Load`; their own `shortcut:` comments
  justifying the duplication are removed, since the duplication they
  excused no longer exists.
- `internal/cli/bundlefile.go` keeps its own, third `bundleSecretKey`
  constant (`= "secrets.yaml"`): it reads and writes the local
  `~/.ichor/clusters/<name>/secrets.yaml` backup file, not a Kubernetes
  Secret, so it does not use `internal/secretsbundle`.
- The frontend's own `isStrongIdentity()` (`docs/adr/0021`,
  TypeScript, client-side display only -- a warning badge on the
  approval dialog, not an authorization decision) is a third
  reimplementation of the same rule, in a different language and runtime
  that cannot share code with the Go rule.

# 0041. Four fixes from the phase 3-6 security review

Status: Accepted · Date: 2026-10-26 · Design section: Configuration reconcile, Trust, Bootstrap, GitOps and Argo CD

Decided: redact bundle material from dry-run warnings unconditionally rather than relying on Talos v1.14.3's current (verified) behavior of never echoing one; renamed a registry-auth leak test to match what it actually exercises and documented its two genuinely unreachable paths; validate `ichor init --cluster` as a Kubernetes object name before any file write; added CEL format checks to `TalosClusterRegistry.host`/`.endpoints` since machinery validates neither.

## Context

A read-only security review of everything added since an earlier phase-1-2 review (`internal/controller`, `internal/uiapi`, `internal/cli`, `pkg/`, `api/v1alpha1`, RBAC manifests) turned up four findings worth fixing. This ADR covers the four; a fifth (a missing `nodes` RBAC grant on the UI backend) went to a different agent owning `internal/uiapi`.

## Decision: dry-run warnings are redacted unconditionally, not because Talos echoes a value today

`TalosMachineReconciler.setWarnings` (`internal/controller/talosmachine_controller.go`) joins Talos's own dry-run warning strings (`machined`'s `ApplyConfiguration` RPC, `DryRun: true`) into a `TalosMachine.status` Condition -- a field readable far more broadly than Secret RBAC allows. The review flagged this as untested against real bundle material.

**What was actually read, not assumed** (Talos v1.14.3, both modules in `$(go env GOMODCACHE)/github.com/siderolabs/talos@v1.14.3` and `.../talos/pkg/machinery@v1.14.3`):

- `internal/app/machined/internal/server/v1alpha1/v1alpha1_server.go`'s `ApplyConfiguration`: for `in.DryRun == true`, the response is built with only `Mode`/`ModeDetails` (a `configdiff.DiffConfigs` summary) -- `Messages[i].Warnings` is never set on that branch at all. It *is* set, from `ValidateAtRuntime`'s own return value, on the non-dry-run branch (`Warnings: warnings`, line 316 in that file). Ichor's own `DryRunApply` (`internal/controller/talosclient_machinery.go`) always sends `DryRun: true`, so `msg.GetWarnings()` is, for this exact Talos version, always empty -- the warnings feature this code reads from has never had anything to read, through the real RPC, in production.
- `pkg/machinery/config/container/validate.go`'s `validate`/`runtimeValidate`/`validateContainer`, and every warning-string call site in `pkg/machinery/config/types/v1alpha1/v1alpha1_validation.go` (grepped for every `warnings = append` in that file, read each call site's surrounding code): every warning is either a static deprecation notice, a field *name*, or a non-secret value (machine type, network interface name, CNI name). None echoes a submitted secret-shaped value. No validator exists at all for `RegistryMirrorConfig`/`RegistryConfig`/`RegistryAuthConfig` (grepped, zero matches) -- relevant to the fourth decision below too.

So: today, for Talos v1.14.3, this is not a live leak, through either of the two facts above (the RPC drops warnings for dry-run; and even if it didn't, nothing generates a value-echoing one). Fixed anyway, because both facts are about Talos's current version, not about this code, and `setWarnings` itself had zero redaction logic -- a future Talos release changing either fact would reopen the gap with no corresponding change needed on Ichor's side to trigger it.

**Fix**: `redactBundleMaterial` (`internal/controller/bundle.go`) replaces any of the bundle's own secret-shaped scalars found verbatim in the joined warning text with `"[redacted]"`, called unconditionally from `setWarnings` regardless of whether today's warnings could ever actually contain one. `bundleSecretMaterial` (the field list) moved from `leak_test.go` into production code in the same file, so the list the test checks against and the list the redaction uses are the same list, not two that could drift. `TestTalosMachineBundleLeak` (`leak_test.go`) gained an eighth route, `"dry run warnings echo bundle material"`, feeding `fakeTalosClient.warnings` the bundle's own secret material directly -- standing in for whatever a future Talos version might start echoing, rather than waiting for one to actually ship before this is tested at all.

## Decision: name the registry-auth leak test for what it tests, document what it can't

`pkg/render/registry_test.go`'s `TestRegistryAuthLeak` had a subtest named "render fails downstream after resolving a real credential." It forces failure via `VersionContract = "not-a-version"`, which fails at `coreconfig.ParseContractFromVersion` (`render.go`, in `buildConfig`, before `injectRegistries` is ever called) -- not downstream of credential resolution at all. The credential in scope never gets near a failing code path.

Checked whether `injectRegistries`'s own two error returns (`registry.go`: `container.New(doc)`, `configpatcher.Apply`) could be forced instead, reading both functions: `container.New` is called with exactly one already-typed, in-process document, so its only failure modes (duplicate/conflicting document) cannot trigger against a single document; `configpatcher.Apply` merges that document onto a `cfg` `baseConfig` just generated fresh via machinery's own generator, so there is no reachable way through this package's public API to make either call fail. Both are exactly as unreachable, and for the same shape of reason, as the two paths `TestRenderBundleLeak` already documents it could not force.

**Fix**: renamed the subtest to `"render fails before registries are ever touched (invalid versionContract)"`, and added a comment naming both unreachable paths and why, in the same style `TestRenderBundleLeak`'s own doc comment already uses for its two.

## Decision: validate `--cluster` before any file write, not after the API server would anyway

`ichor init`'s Step 1 (`internal/cli/init_run.go`) writes the secrets bundle to `~/.ichor/clusters/<cluster>/secrets.yaml` (`internal/cli/bundlefile.go`'s `filepath.Join(home, ".ichor", "clusters", cluster)`) before the `TalosCluster` object -- whose `metadata.name` is the same `--cluster` value -- ever reaches the API server, which is the only thing that would otherwise reject a malformed value. `--cluster` becomes that object name regardless, so checking the same constraint earlier is not a new restriction, only an earlier application of one that already existed.

**Fix**: `buildInitPlan` (`internal/cli/init_plan.go`) now rejects `--cluster` with `k8s.io/apimachinery/pkg/util/validation.IsDNS1123Subdomain` before any other check, including the ones already there -- `buildInitPlan` runs entirely before Step 1's file write, so this is enough to close the gap without touching `init_run.go`'s own ordering. Covered by a new case in `TestBuildInitPlan_Errors`'s existing table (`init_plan_test.go`).

## Decision: `TalosClusterRegistry.host`/`.endpoints` get the same CEL rigor `spec.discovery` already has, confirmed necessary first

Checked first, as the review asked, whether machinery's own `ValidateAsClient`/`ValidateAtRuntime` catches a malformed `host` or `endpoints` value: grepped `pkg/machinery/config/types/v1alpha1/v1alpha1_validation.go` for `RegistryMirror`/`RegistriesConfig`/`MachineRegistries` -- zero matches. Machinery has no validator at all for this part of the config; a malformed value reaches a node's machine config completely unexamined.

**Fix**: added CEL to both fields -- `host` must be `"*"` or match `^[a-zA-Z0-9.-]+(:[0-9]{1,5})?$`; every `endpoints` entry must satisfy the CEL extension library's `isURL()`. The first attempt (a stricter, RFC-shaped hostname regex, and `isURL` with no length bound) was rejected by the API server's CEL cost estimator (`estimated rule cost exceeds budget by factor of 25.2x` for the regex, `16.8x` for the unbounded `isURL` loop) -- fixed by adding `MaxLength=255` on `host` and `MaxItems=8`/`items:MaxLength=2048` on `endpoints` (the estimator's own suggested fix) and simplifying the regex to drop nested optional groups, the same style `spec.discovery.addresses`/`.cidrs` already uses for the same reason. Verified against envtest (not just `go build`, which cannot catch a CEL compile-time rejection): `make manifests generate` followed by `go test ./api/v1alpha1/...` with `KUBEBUILDER_ASSETS` set, which installs the real CRD into a real (test) API server and would fail exactly the way the first attempt did if the budget were still exceeded. Three new specs in `validation_test.go` cover accepting `"*"` and a `host:port`, and rejecting a non-matching host and a non-URL endpoint.

## Consequences

- `internal/controller/bundle.go` gained `bundleSecretMaterial` and `redactBundleMaterial`; `setWarnings` takes `*secrets.Bundle` as a new parameter.
- `pkg/render/registry_test.go`: one subtest renamed, no behavior change.
- `internal/cli/init_plan.go`: one new, early validation; no change to any already-valid `--cluster` value's behavior.
- `api/v1alpha1/taloscluster_types.go`: two new CEL rules, both additive (narrower schema, not wider) -- any already-valid `TalosClusterRegistry` continues to validate, confirmed by the existing fixtures in `pkg/render` (which never go through API-server admission, so are unaffected either way) and `api/v1alpha1/validation_test.go` (which does, and still passes).
- The RBAC finding from the same review (`internal/uiapi`'s `GetNode` missing a `nodes` grant) is out of scope here -- handled by the agent owning `internal/uiapi`.
- Follow-up: `redactBundleMaterial` was removed; `setWarnings` and the etcd-backup controller (ADR 0047) now share one `scrubber` in `internal/controller/bundle.go`, which replaces longer values first so a secret that contains another is not left half-visible.

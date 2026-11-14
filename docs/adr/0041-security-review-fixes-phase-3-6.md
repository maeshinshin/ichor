# 0041. Four fixes from the phase 3-6 security review

Status: Accepted · Date: 2026-10-26 · Design section: Configuration reconcile, Trust, Bootstrap, GitOps and Argo CD

Decided: redact bundle material from dry-run warnings unconditionally rather than relying on Talos v1.14.3's current behavior of never echoing one; renamed a registry-auth leak test to match what it actually exercises and documented its two genuinely unreachable paths; validate `ichor init --cluster` as a Kubernetes object name before any file write; added CEL format checks to `TalosClusterRegistry.host`/`.endpoints` since machinery validates neither.

## Context

A read-only security review of everything added since an earlier phase-1-2 review (`internal/controller`, `internal/uiapi`, `internal/cli`, `pkg/`, `api/v1alpha1`, RBAC manifests) turned up four findings worth fixing. This ADR covers four of them.

## Decision: dry-run warnings are redacted unconditionally, not because Talos echoes a value today

`TalosMachineReconciler.setWarnings` (`internal/controller/talosmachine_controller.go`) joins Talos's own dry-run warning strings (`machined`'s `ApplyConfiguration` RPC, `DryRun: true`) into a `TalosMachine.status` Condition -- a field readable far more broadly than Secret RBAC allows.

**What Talos v1.14.3 does** (the `siderolabs/talos` and `pkg/machinery` modules at that version):

- `internal/app/machined/internal/server/v1alpha1/v1alpha1_server.go`'s `ApplyConfiguration`: for `in.DryRun == true`, the response is built with only `Mode`/`ModeDetails` (a `configdiff.DiffConfigs` summary) -- `Messages[i].Warnings` is never set on that branch at all. It *is* set, from `ValidateAtRuntime`'s own return value, on the non-dry-run branch (`Warnings: warnings`, line 316 in that file). Ichor's own `DryRunApply` (`internal/controller/talosclient_machinery.go`) always sends `DryRun: true`, so `msg.GetWarnings()` is, for this exact Talos version, always empty -- the warnings feature this code reads from has never had anything to read, through the real RPC, in production.
- `pkg/machinery/config/container/validate.go`'s `validate`/`runtimeValidate`/`validateContainer`, and every warning-string call site in `pkg/machinery/config/types/v1alpha1/v1alpha1_validation.go`: every warning is either a static deprecation notice, a field *name*, or a non-secret value (machine type, network interface name, CNI name). None echoes a submitted secret-shaped value. No validator exists at all for `RegistryMirrorConfig`/`RegistryConfig`/`RegistryAuthConfig` -- relevant to the fourth decision below too.

So: today, for Talos v1.14.3, this is not a live leak, through either of the two facts above (the RPC drops warnings for dry-run; and even if it didn't, nothing generates a value-echoing one). Fixed anyway, because both facts are about Talos's current version, not about this code, and `setWarnings` itself had zero redaction logic -- a future Talos release changing either fact would reopen the gap with no corresponding change needed on Ichor's side to trigger it.

**Fix**: `redactBundleMaterial` (`internal/controller/bundle.go`) replaces any of the bundle's own secret-shaped scalars found verbatim in the joined warning text with `"[redacted]"`, called unconditionally from `setWarnings` regardless of whether today's warnings could ever actually contain one. `bundleSecretMaterial` (the field list) lives in production code in the same file, so the list the leak test checks against and the list the redaction uses are the same list, not two that could drift.

## Decision: name the registry-auth leak test for what it tests, document what it can't

`pkg/render/registry_test.go`'s `TestRegistryAuthLeak` had a subtest named "render fails downstream after resolving a real credential." It forces failure via `VersionContract = "not-a-version"`, which fails at `coreconfig.ParseContractFromVersion` (`render.go`, in `buildConfig`, before `injectRegistries` is ever called) -- not downstream of credential resolution at all. The credential in scope never gets near a failing code path.

`injectRegistries`'s own two error returns (`registry.go`: `container.New(doc)`, `configpatcher.Apply`) cannot be forced through this package's public API: `container.New` is called with exactly one already-typed, in-process document, so its only failure modes (duplicate/conflicting document) cannot trigger against a single document; `configpatcher.Apply` merges that document onto a `baseConfig` just generated fresh via machinery's own generator. Both are exactly as unreachable, and for the same shape of reason, as the two paths `TestRenderBundleLeak` already documents it cannot force.

**Fix**: the subtest is named `"render fails before registries are ever touched (invalid versionContract)"`, with a comment naming both unreachable paths and why, in the same style `TestRenderBundleLeak`'s own doc comment uses for its two.

## Decision: validate `--cluster` before any file write, not after the API server would anyway

`ichor init`'s Step 1 (`internal/cli/init_run.go`) writes the secrets bundle to `~/.ichor/clusters/<cluster>/secrets.yaml` (`internal/cli/bundlefile.go`'s `filepath.Join(home, ".ichor", "clusters", cluster)`) before the `TalosCluster` object -- whose `metadata.name` is the same `--cluster` value -- ever reaches the API server, which is the only thing that would otherwise reject a malformed value. `--cluster` becomes that object name regardless, so checking the same constraint earlier is not a new restriction, only an earlier application of one that already existed.

**Fix**: `buildInitPlan` (`internal/cli/init_plan.go`) now rejects `--cluster` with `k8s.io/apimachinery/pkg/util/validation.IsDNS1123Subdomain` before any other check, including the ones already there -- `buildInitPlan` runs entirely before Step 1's file write, so this is enough to close the gap without touching `init_run.go`'s own ordering.

## Decision: `TalosClusterRegistry.host`/`.endpoints` get the same CEL rigor `spec.discovery` already has

Machinery's own `ValidateAsClient`/`ValidateAtRuntime` (`pkg/machinery/config/types/v1alpha1/v1alpha1_validation.go`) has no validator at all for `RegistryMirror`/`RegistriesConfig`/`MachineRegistries`; a malformed `host` or `endpoints` value reaches a node's machine config completely unexamined.

**Fix**: added CEL to both fields -- `host` must be `"*"` or match `^[a-zA-Z0-9.-]+(:[0-9]{1,5})?$`; every `endpoints` entry must satisfy the CEL extension library's `isURL()`. The API server's CEL cost estimator rejects a stricter, RFC-shaped hostname regex and an unbounded `isURL` loop, so the fields are bounded: `MaxLength=255` on `host`, `MaxItems=8` and `items:MaxLength=2048` on `endpoints`, and the regex has no nested optional groups, the same style `spec.discovery.addresses`/`.cidrs` already uses for the same reason.

## Consequences

- `internal/controller/bundle.go` gained `bundleSecretMaterial` and `redactBundleMaterial`; `setWarnings` takes `*secrets.Bundle` as a new parameter.
- `internal/cli/init_plan.go`: one new, early validation; no change to any already-valid `--cluster` value's behavior.
- `api/v1alpha1/taloscluster_types.go`: two new CEL rules, both additive (narrower schema, not wider) -- any already-valid `TalosClusterRegistry` continues to validate.
- Follow-up: `redactBundleMaterial` was removed; `setWarnings` and the etcd-backup controller (ADR 0047) now share one `scrubber` in `internal/controller/bundle.go`, which replaces longer values first so a secret that contains another is not left half-visible.

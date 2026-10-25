# 0014. CLI skeleton under cmd/ichor, and how `ichor adopt` guarantees zero drift

Status: Accepted · Date: 2026-10-25 · Design section: CLI and UI; Bootstrap (`ichor adopt`)

## Context

Phase 4 is the CLI. `docs/design/architecture.md`'s "CLI and UI" section sets
the rule — parity: the CLI and the UI share the same Go packages for
rendering, diffing and planning, and both write CRs rather than mutating a
Talos node directly. It names three exceptions that predate the cluster:
`init`, `adopt` and full-loss recovery. This unit scaffolds `cmd/ichor` with
cobra and implements the first of those exceptions, `ichor adopt`:

> `ichor adopt` brings an existing cluster under management by diffing each
> node's current config against a fresh render and storing the difference as
> initial patches, so ownership transfers with zero drift.

Two problems surfaced while building this that are worth recording so the
next person touching the CLI doesn't rediscover them the hard way.

### cobra-cli's default layout collides with kubebuilder's

`cobra-cli init cmd/ichor` writes `cmd/ichor/main.go` importing
`<module>/cmd` — its built-in assumption that command definitions live in a
`cmd` package one level up from `main.go`, resolved against the module root
rather than against the path given to `init`. In this repo that `cmd`
package is already taken: `cmd/main.go` is the controller manager's own
`package main`, from `kubebuilder init`. Importing a `main` package is a
compile error ("is a program, not an importable package"), and it broke
`make manifests` repo-wide the moment it landed, because `controller-gen`'s
`paths="./..."` walks into `cmd/ichor` too.

cobra-cli isn't wrong to assume this; kubebuilder's and cobra-cli's
conventions simply both want to own a top-level `cmd` package, and only one
can. The fix is to let cobra-cli scaffold normally and then move its output:
the command tree lives in `internal/cli` (package `cli`), not
`cmd/ichor/cmd`, and `cmd/ichor/main.go` is reduced to:

```go
package main

import "github.com/maeshinshin/ichor/internal/cli"

func main() { cli.Execute() }
```

`cobra-cli add <name>` has the same issue in reverse: given no way to target
a subdirectory (`--package` was removed from the CLI), it always writes
`cmd/<name>.go` at the module root. Every new subcommand has to be generated
there and moved into `internal/cli` by hand, same as `adopt.go` was.

### goreleaser's default `dist/` collides with the committed chart

`dist/chart` is a committed Helm chart (ADR tooling elsewhere generates it
from `dist/install.yaml`); goreleaser also defaults to writing its own build
output to `dist/`. `goreleaser build --clean` deletes whatever `dist:` points
at before writing to it — discovered by running it and watching `git status`
show the entire chart staged for deletion. `.goreleaser.yaml` now sets
`dist: .goreleaser-dist` (ignored in `.gitignore`) so a release build can
never touch the chart.

## Decision

### Package layout

```
cmd/ichor/main.go       — package main; nothing but Execute()
internal/cli/root.go    — rootCmd, persistent flags (-o/--output, --yes,
                           --kubeconfig, --talosconfig), Execute()
internal/cli/adopt.go   — the `adopt` cobra.Command: flags, RunE wiring
internal/cli/adopt_plan.go — buildAdoptPlan: the diff/verify/capture logic,
                           no I/O, fully unit-testable
internal/cli/adopt_run.go  — runAdopt: orchestration (fetch, confirm,
                           create, report) against adoptDeps, an interface
                           bundle a test can fake entirely
internal/cli/talosdial.go  — talosConfigReader + dialTalos: the one real
                           Talos API call adopt makes (CurrentConfig)
internal/cli/kube.go       — kubeconfig loading, the controller-runtime
                           client, scoped to Ichor's own CRDs only
internal/cli/bundlefile.go — reads a local secrets bundle file
internal/cli/output.go     — -o yaml|json encoding
```

Scaffolding commands run (from the repo root, inside `nix develop`):

```
cobra-cli init cmd/ichor
cobra-cli add adopt
```

then moving the four files `cobra-cli` wrote (`cmd/ichor/cmd/{root,adopt}.go`
from `init`, `cmd/adopt.go` from `add`) into `internal/cli`, renaming their
`package cmd` to `package cli`, and rewriting `cmd/ichor/main.go`'s import as
above. `flake.nix` already carried `cobra-cli`; nothing to add there.

### kubeconfig and talosconfig

Both follow the tool whose convention they borrow, rather than inventing a
third:

- `--kubeconfig` (persistent, root-level): client-go's own
  `clientcmd.NewDefaultClientConfigLoadingRules()` resolves it — an explicit
  flag value wins, otherwise `$KUBECONFIG`, otherwise `~/.kube/config` — the
  same resolution kubectl itself uses. This is adopt's only path to the
  Kubernetes API: the TalosCluster/TalosMachine objects it reads, and the
  TalosConfigPatch objects it creates, all live behind it.
- `--talosconfig` (persistent, root-level): passed through to machinery's
  `client.WithConfigFromFile`, whose empty-string case already resolves
  `$TALOSCONFIG`, then `~/.talos/config`, then the in-cluster service account
  mount — exactly talosctl's own resolution
  (`pkg/machinery/client/config/path.go`). adopt dials each TalosMachine's
  Talos API through this, using whatever admin identity the operator's
  talosconfig already carries for the cluster being adopted.

adopt deliberately does *not* reuse `internal/controller.NewMachineryTalosClient`
(the controller's Talos API adapter): that constructor derives its client
certificate from a secrets bundle Ichor already manages. adopt predates that
management — there may be no Ichor-issued credential yet — so it dials the
same way talosctl would, through the operator's own talosconfig. The
secrets bundle still has to come from somewhere, since rendering a baseline
needs one: `--secrets-bundle` takes a local file in the same format
`talosctl gen secrets` writes and `internal/controller/bundle.go`'s Secret
convention already documents (`secrets.yaml`), since adopt runs before
Ichor has a Secret of its own to read it from.

### How `ichor adopt` guarantees zero drift

For each `TalosMachine` already recorded for `--cluster`:

1. Read the node's live configuration over the Talos API
   (`talosConfigReader.CurrentConfig`).
2. Render a baseline from the `TalosCluster`/`TalosMachine`/existing
   `TalosConfigPatch` objects already in the Kubernetes API
   (`pkg/render.Render` — not re-implemented).
3. Diff live vs. baseline with `internal/controller.ChangedPaths` (exported
   from this unit's previously-unexported `changedPaths`, logic unchanged) —
   not re-implemented either.
4. If nothing changed, there is nothing to adopt for this machine.
5. Otherwise, build a candidate `TalosConfigPatch` and **verify it before
   ever proposing it**: render baseline+candidate and require the result to
   be byte-identical to the node's live configuration. Only a passing
   candidate is ever returned from `buildAdoptPlan`; only a `buildAdoptPlan`
   result with a patch is ever shown to the operator or created.

Step 5's self-check, not the diff in step 3, is what the "zero drift"
acceptance test actually exercises: it is exactly the comparison the test
performs independently (render the created patch again, compare to the
current-config fixture).

Step 5 also has to not leak secrets, which drove the scope decision in
`adopt_plan.go`: **only `SysctlConfig` and `KernelModuleConfig` documents are
ever captured automatically.** The legacy v1alpha1 `Config` document is
deliberately never captured.

**Update, same day:** the reason this ADR originally gave no longer holds,
and is recorded here rather than silently deleted so the history is
honest. The original reason was schema-specific: `TalosConfigPatchDocument`'s
`config.machine` object required `type`, `token` and `certSANs` on *any*
Config document, sparse or not, and `token` mirrored the secrets bundle —
there was no way to patch e.g. `cluster.network.cni.name` without also
restating it. A same-day mirrorgen change (see ADR 0003's update) dropped
`machine.token`, `machine.ca`, `cluster.ca` and six other secret-bearing
fields from the mirror type and the CRD schema entirely, closing a real
hole (`configpatcher.Apply`'s strategic merge meant a `Config` document
could already overwrite a cluster's CA through a `TalosConfigPatch`). The
two fields `config.machine` still requires — `type` and `certSANs` — are
not secrets: `type` is the role already on `TalosMachine.Spec.Role`,
`certSANs` the machine's own address. A sparse Config document is now
representable without touching anything sensitive.

**This command still does not capture it, but the reason is different
now: nobody has built the sparse-reconstruction path, not that it is
unsafe.** Turning a legacy-document diff into a sparse patch means walking
`ChangedPaths`' dot-separated output back into a partial
`typedconfig.Config` value with every untouched field left at its Go zero
value (so `configpatcher`'s merge — "skip a zero value on the right" —
leaves the base's value alone), which is a different, fiddlier kind of code
than the whole-document capture `SysctlConfig`/`KernelModuleConfig` already
get; building it well enough to trust needs its own pass, not a few lines
bolted onto this one while finishing `ichor init`. `UnrepresentablePaths`
still exists and is still correct — it just no longer means "this is
secret," only "adopt doesn't build this kind of patch yet."

The consequence is unchanged in practice today, only in reason: a
difference confined to the legacy Config document still comes back from
`buildAdoptPlan` as `UnrepresentablePaths` — field *names* only, safe to
print — with no patch. `runAdopt` reports these and leaves that machine's
drift for the operator to patch by hand. This is covered by
`TestBuildAdoptPlan_LegacyDiffIsNeverAutoPatched` and is the one
`TestAdoptNeverLeaksSecrets` subtest ("legacy diff reported, not patched")
that proves the refusal itself never leaks either — that test is still
meaningful: whatever the future sparse-capture path ends up setting still
has to prove it never reaches outside what actually changed.

### Confirmation and output

`--yes` skips the interactive y/N prompt before any `TalosConfigPatch` is
created, matching the design document's "interactive confirmation with
`--yes`" for every other dangerous CLI operation. `-o yaml|json` prints the
patches adopt is about to create (or just created); both encoders only ever
see `ichorv1alpha1.TalosConfigPatch` values, never the secrets bundle or a
full rendered machine configuration.

## Consequences

- `cmd/ichor/main.go` stays a two-line shim forever; all real CLI code lives
  under `internal/cli`, which also means future subcommands (`init`, `ui`,
  `restore --recover-cluster`) land there too, not under `cmd/ichor/cmd`.
- `cobra-cli add <name>` will keep writing to the wrong place
  (`cmd/<name>.go` at the module root) until/unless a future cobra-cli
  version restores a way to target a subdirectory; every new subcommand
  needs the same by-hand move this ADR describes for `adopt.go`.
- adopt's zero-drift guarantee has a known gap: differences confined to the
  legacy Config document are reported, not patched. This no longer needs a
  CRD change to close (the schema now allows a sparse Config document); it
  needs a sparse-reconstruction function nobody has written yet. Until then,
  `ichor adopt` is complete for sysctl/kernel-module drift and honest about
  everything else.
- `.goreleaser.yaml`'s `dist:` override means any future tooling that reads
  goreleaser's own output has to know to look in `.goreleaser-dist`, not
  `dist/`.

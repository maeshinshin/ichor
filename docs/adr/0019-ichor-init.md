# 0019. `ichor init`: scope, the namespace decision, and the gaps it leaves open

Status: Accepted; superseded in part by ADR 0099 · Date: 2026-10-25 · Design section: Bootstrap (`ichor init`)

Decided: `ichor init`: VIP recommended not required, Ichor applied through the API server not inlined, topology presets carry count/schedulable

## Context

`docs/design/architecture.md`'s "Bootstrap" section gives `ichor init` six
steps and one example invocation:

```sh
ichor init --preset standard --cni cilium [--argocd --repo URL --path clusters/NAME] [--gitops-dir DIR]
```

This phase builds steps 1-5. Step 6 (hand-over) is excluded on purpose: it
is a continuous reconciliation loop against Argo CD's own status (watch,
poll, retry), not a one-shot action a CLI invocation can perform and then
exit. It belongs to the controller, as a later unit's work.

## Decision: VIP recommended, insecure apply's assumption stated

**VIP is recommended, not required.** `buildInitPlan` accepts any of the
CRD's four endpoint types (`vip`, `loadBalancer`, `dns`, `node`) and only
prints a warning -- never an error -- when the type isn't `vip`, carrying
the design document's own reason in the warning text (changing the
endpoint later means re-issuing every certificate in the cluster).

**Insecure apply assumes a separate provisioning network, and says so.**
`ichor init --help` and the interactive confirmation prompt both state
this explicitly, because `maintenanceApplier.ApplyInsecure` genuinely
cannot authenticate the node it's talking to -- that is what "insecure"
means here, per architecture.md's "Trust" section.

## Decision: Ichor itself is applied through the API server, not inlined

The design's original step 3 said `cluster.inlineManifests` carries "Ichor
itself, the CNI (unless flannel), and, with `--argocd`, Argo CD plus a
root Application." Building it showed that claim does not hold once you
ask *why* each one needs to be there:

- The CNI genuinely has to be inlined: a node is not Ready and nothing can
  be scheduled before a CNI exists, and nothing else can put one there
  before the API server is even up.
- Ichor does not have that constraint. Design step 4 already gets a
  working kubeconfig before step 5 creates any CR; by that point the API
  server is reachable, and applying Ichor's own install manifest there
  works exactly like any other `kubectl apply`. It is also more
  debuggable: a failure applying through the API server comes back as an
  ordinary error, where a failure inside `cluster.inlineManifests` is
  buried in Talos's own boot log.

So `cluster.inlineManifests` now carries **only the CNI's bootstrap
manifest** (`cniBoot.InlineManifest`, nil for flannel). Ichor's own
install manifest is applied separately, after step 4, by
`applyIchorManifest`, straight through the API server:

- The manifest is `make build-installer`'s `dist/install.yaml`, embedded
  in the `ichor` binary at build time (`internal/installmanifest`),
  mirroring `cmd/ichor-ui`'s `uiembed`/committed-placeholder pattern
  exactly (ADR 0017): `make build-ichor-embed` copies `dist/install.yaml`
  into `internal/installmanifest/install.yaml` (gitignored) and builds
  with `-tags installembed`; a plain `go build ./...`, with no prior
  `make` step, embeds the committed `placeholder.yaml` instead, so a
  fresh checkout keeps compiling. `applyIchorManifest` refuses outright
  if the embedded bytes are still the placeholder (checked by a sentinel
  string, not by trying to apply it and letting that fail some other
  way).
- **The embedded controller version can never drift from the CLI's own
  version**, because both come out of the same `go build` invocation --
  there is no separate fetch step, and no version flag for an operator to
  get wrong. The property this gives phase 10 (air-gap): the binary
  already carries everything it installs, nothing to mirror separately
  for this one component.
- CRDs need a moment to become Established before resources that depend
  on them can be created (kustomize's own ordering already puts the
  Namespace and CRDs first in `dist/install.yaml`, but "created" and
  "Established" are not the same moment). `createWithRetry` bounds this
  at 10 attempts, 500ms apart.
  (`shortcut:` a fixed budget, not a watch on the CRD's `Established`
  condition; upgrade if a slower cluster ever needs longer than 5s here.)

**Argo CD itself is never installed by `init`, at all**, with or without
`--argocd`. Fetching and applying a third-party install manifest during
bootstrap means deciding which version, signed by whom, from where --
that is not this command's call to make, and architecture.md's "Ichor
doesn't install or operate CNI, storage or monitoring; it bootstraps CNI
and Argo CD once and hands them over" does not mean "fetches it from the
internet during a privileged bootstrap." `--argocd` prepares exactly two
things that are ready for use once Argo CD exists some other way: the
repository credentials Secret, and (with `--gitops-dir`) the root
Application. `initArgoCD` prints this plainly before doing either --
naming the Argo CD version range (v3.5.x+) this command's
Application/sync-option shapes were verified against -- specifically so
an operator who ran `--argocd` does not assume it means Argo CD is
running.

`docs/design/architecture.md`'s step 3 and its "Hand-over" paragraph were
rewritten to match: the CNI is now the only component that follows the
inject-through-`inlineManifests`-then-remove path; Ichor and Argo CD
never did.

## Decision: topology presets carry count and schedulable; a single `--namespace`

**Four presets, not a single hardcoded one.** `presetTopology` maps each
of `single` (1+0, schedulable), `compact-ha` (3+0, schedulable),
`standard` (3+n, tainted, recommended) and `large` (5+n, tainted) to the
control-plane count and schedulable value architecture.md's "Nodes"
section pairs it with. `init` derives both from `--preset` rather than
taking them as independent flags -- which is what makes "control planes
unschedulable with zero workers" (the combination the design explicitly
rejects) unreachable by construction: every `schedulable=false` row in
the table is one whose preset is sized for separate worker nodes added
later. `TestPresetTopology_NeverRejectsSchedulableWithNoWorkers` checks
the table itself stays that way; `buildInitPlan`'s own rejection
(`TestBuildInitPlan_RejectsUnschedulableWithNoWorkers`, which inserts a
deliberately-wrong fifth entry for the duration of the test) is kept as
the real enforcement point for whatever a future preset might get wrong,
even though today's four rows never reach it.

**One `--namespace`, not two.** `TalosClusterSpec.SecretsRef`'s doc
comment (ADR 0011) says the bundle Secret lives in the controller's own
namespace, which need not be the `TalosCluster`'s -- RBAC cannot express
"the Secret this `TalosCluster` names, wherever it lives" below cluster
scope. Since `applyIchorManifest` now actually deploys the controller,
and `config/manager`'s kustomize base hardcodes its namespace to
`ichor-system` (matching the RBAC markers' own literal), `--namespace`
defaults to `ichor-system` and `buildInitPlan` warns -- not rejects -- if
it is overridden, since a Secret or CR created elsewhere would be
invisible to the controller this same run just deployed. This remains a
simplification specific to the self-managed default (one controller, one
namespace, created by this same command); hub mode would need the
controller's namespace and the `TalosCluster`'s as two separate flags.

## Decision: disk roles, including the three that are now representable

Architecture.md's "Nodes" section names five roles: `system` (the install
target), `ephemeral`, `storage`, `userVolume` and `unused`. At the time
this phase started, only `system` had anywhere to go -- `machine.install`
-- so that is what shipped first: a required `--install-disk` flag,
folded into the same `Config`-kind patch as the inline manifests
(`buildInlineManifestsPatch`), with real values for
`machine.type`/`machine.certSANs` (the node's own role and address), not
placeholders, since this document is authored directly rather than
reconstructed from a diff.

The other three roles became representable once another unit extended
the mirror allow-list with `VolumeConfig`, `UserVolumeConfig` and
`RawVolumeConfig` (and `pkg/render.DocumentFromPatch` was extended here,
mechanically, to actually convert the three new `TalosConfigPatchDocument`
kinds -- the allow-list alone did not make them renderable). `init` now
exposes one optional CEL-selector flag per role
(`--ephemeral-disk-selector`, `--storage-disk-selector`,
`--user-volume-disk-selector`, each paired with an optional `*-volume-name`
for the latter two), building a `VolumeConfig`/`RawVolumeConfig`/
`UserVolumeConfig` document per selector given, appended to the same
bootstrap patch:

- **Selectors, not device paths.** Architecture.md is explicit that disks
  are identified by WWID, serial or by-id path, not `/dev/sdX`, so these
  flags take the same CEL expression `DiskSelector.Match` itself takes
  (e.g. `disk.wwid == "..."`). `init` does not translate a device path
  into a selector on the operator's behalf -- it has no way to query the
  node's disk inventory yet, so it would be guessing at field names
  instead of using what the operator already knows about their own
  hardware.
- **`ephemeral`'s name is fixed** (`constants.EphemeralPartitionLabel`,
  `"EPHEMERAL"`): Talos validates a system `VolumeConfig`'s name against
  its own known set, confirmed by reading `volume_config.go`'s
  `validateVolumeConstraints`, not assumed.
- **`storage`/`userVolume` need a size hint.** Talos's own
  `ProvisioningSpec.Validate` rejects a `RawVolumeConfig`/`UserVolumeConfig`
  with neither `minSize` nor `maxSize` set ("min size or max size is
  required") -- confirmed by running it and reading the error, not by
  memory. Each gets `minSize: "0"` (a real, non-empty size, not an unset
  field -- `ByteSize.IsZero()` checks for a nil/absent value, and `"0"`
  parses to a present zero) and no `maxSize`, so it grows to fill its own
  disk.
- **No sharing, so no negative `maxSize`.** Architecture.md's note that a
  *negative* `maxSize` means "space to leave free" (`available - value`),
  not "space to consume," only matters once two roles share one disk
  (EPHEMERAL reserving space with a negative `maxSize`, a user volume
  taking what's left). Every role flag here implies its own dedicated
  disk -- the architecture doc's own "recommended layout," not the
  discouraged shared one -- so there is nothing here that needs a
  negative value, and the shared-disk case is not built.
- **No disk encryption flag.** `EncryptionKey.KeyStatic` was dropped from
  the mirror type as secret material (same day, by the unit extending the
  allow-list); the remaining key kinds (KMS, TPM, node ID) are each their
  own integration, out of scope here.

## Decision: a known, pre-existing test collision in a file this unit does not own

Verifying this work (`make build test lint`) surfaced one `internal/controller`
test failure: `discovery_test.go`'s `uuidRejected` and
`taloscluster_controller_test.go`'s `uuidClusterReconcile` are both the
literal `"70707070-7070-7070-7070-707070707070"`, written independently by
different units sharing the same envtest API server within one Ginkgo
run -- whichever spec runs first creates a `TalosMachine` with that name,
and the other's own `Create` then fails with `AlreadyExists`. Confirmed
reproducible (same failing spec across two runs with different random
seeds, ruling out an ordering-dependent flake) and confirmed unrelated to
this unit's own diff (neither file is touched by any commit in this ADR's
history). `internal/controller` is out of this unit's scope to edit, so
this is recorded here rather than fixed: `make test` is green for every
package this ADR's work touches (`internal/cli`, `pkg/cni`, `pkg/gitops`,
`pkg/render`, `internal/installmanifest`), and fails only on this one,
pre-existing, out-of-scope collision.

## Consequences

- `ichor init --argocd --gitops-dir DIR` without Argo CD already installed
  on the target cluster produces a correct, ready-to-use GitOps skeleton
  and (if `--repo-username`/`--repo-password` were given) a repository
  credentials Secret, but does not itself bring Argo CD up -- and says so.
  An operator installs Argo CD by some other means before its root
  Application can do anything.
- The CNI's own Argo CD Application lands in the GitOps skeleton's
  `apps/cni/application.yaml` because `pkg/cni.Preset.ArgoCDApplication`
  returns `pkg/gitops`'s own typed `*gitops.Application` (ADR 0013's and
  ADR 0015's updates) -- no conversion needed between the two packages.
- Hub mode, if built later, will need `ichor init` (or whatever supersedes
  it) to take the controller's namespace and the `TalosCluster`'s as two
  separate flags; today's single `--namespace` is correct only because
  the self-managed default makes them the same namespace.
- Per-disk role selection today is one CEL selector per role, each
  implying a dedicated disk. The shared-disk layout
  (`systemDiskLayout: shared`, EPHEMERAL reserving negative `maxSize` so a
  user volume gets what's left) is not built; architecture.md calls it
  "discouraged" for exactly the I/O-contention reason that makes it a
  reasonable thing to leave for whoever actually needs it.
- `two UUID literals colliding across internal/controller/discovery_test.go
  and taloscluster_controller_test.go` is a known, reported, unfixed bug
  outside this unit's scope; whoever next touches either file should pick
  a different literal for one of them.

- `--discover-address` and `--discover-cidr` (repeatable) fill `spec.discovery` with the same IPv4 and /24-or-smaller limits the CRD enforces, so nodes booted into maintenance mode after init are found without a hand patch (ADR 0011); unset leaves discovery off.

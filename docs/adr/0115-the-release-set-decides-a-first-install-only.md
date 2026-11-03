# 0115. The release set decides a first install only; each component upgrades on its own

Status: Accepted · Date: 2026-11-03 · Design section: Upgrades · Touches: 0043, 0057, 0059, 0098, 0099, 0103, 0110, 0113, 0114

Decided: the release set in `pkg/release` (`Pinned`) decides the versions of a first install (`ichor init`, the CLI defaults, the UI's proposed values); after that Talos, Kubernetes and Cilium upgrade on their own, to any version the compatibility rules accept, and the set does not limit them. Each upgrade target must be a final semantic version newer than what runs, on a path the component's rule allows, and compatible with the other components in one table (`pkg/release/compat.go`) whose refusal only the override verb (ADR 0110) passes, recorded in the cluster's Events.

## Context

ADR 0113 made the release set a constraint: every upgrade moved only to the pinned version, the webhook refused any other version, adopt refused a cluster newer than the set, and `ichor upgrade cluster` ran the components in one sequence. The user reviewed it on 2026-11-03 and decided that only the first install's version comes from the Ichor build: after the install each component must be upgradable on its own, to a version other than the set's. ADR 0113 went too far in binding later versions.

The compatibility question did not go away. Nothing said which Talos, Kubernetes and Cilium combinations Ichor can operate, so an upgrade could pair a Kubernetes that the running Talos does not support, or a Kubernetes that the installed Cilium does not run. This ADR keeps the 0113 set as the first-install default, drops the constraint on later versions, and puts the compatibility question into one typed table that every upgrade path reads.

## Decision

### What 0113 keeps

- `pkg/release.Pinned` is the one set in Go. Init, the CLI defaults and the UI's proposed values read it. `ichor version` shows it as "versions a new install gets".
- Cilium upgrades go to the chart embedded in the running Ichor (ADR 0098). The Versions card offers the upgrade when the cluster runs an older Cilium.
- The Ichor row, self-upgrade (ADR 0059) and the Ichor release notice (ADR 0114) are unchanged.
- `ichor init` keeps the set. `--talos-version`, `--kubernetes-version` and `--version-contract` default to the set, and any other value is refused (`checkPinnedFlags`). The reason is that a first install is the build's tested matrix; a deliberate choice of another version is an upgrade, which can be done after the install. A sensible choice is therefore not blocked by the install path, but the first install is still the set.

### What 0113 loses (superseded)

- **0113 item 3 (adopt refuses newer, reports behind).** Superseded. Adopt takes the cluster as it runs, older or newer than the set. It refuses a cluster it cannot operate and warns about an incompatible one; see "adopt" below. There is no "behind" state. The version contract is probed from the running Talos, as it was before 0113.
- **0113 item 4 (the webhook refuses versions other than the set).** Superseded. See "the webhook" below.
- **0113 item 5 (no override for the set).** Superseded: the override verb now passes the compatibility refusal (ADR 0110), not the set.
- **0113 item 6 (`ichor upgrade cluster`, the rollout steps talos, config, kubernetes, cilium, and the Versions card's one button).** Removed. Each component is upgraded with its own operation.
- **0113 item 8 (the Versions card lost its per-row upgrade buttons, and showed Behind or Up to date against the set).** Superseded. The Talos and Kubernetes rows have an Upgrade button (StartOperationDialog) that offers the target as a combobox of the candidates (see "the version picker"). The Behind and Ahead badges against the set are removed; Talos, the contract and Kubernetes rows show the running version. The Cilium row keeps its state: it is offered when the embedded chart is newer.

### The admission rule (webhook)

The TalosCluster webhook checks the versions a write sets:

- Each version must be a semantic version: `talosVersion` and `kubernetesVersion` pass `version.CheckSemver` (all three numbers), and `versionContract` (`vX.Y`) passes `semver.IsValid`. Any version is accepted by form; the set is not a bound.
- The Talos and Kubernetes pair is checked with `release.CheckTalosStep` when either side changes, and with `release.CheckKubernetesStep` (which also takes the Cilium the status shows) when Kubernetes changes. An incompatible pair is refused unless the caller holds the override verb (ADR 0110), through the same RightCheck the last-worker guard uses. A version that neither machinery nor the table knows is refused with no override.
- An update that changes `spec.talosVersion`, `spec.kubernetesVersion` or `spec.versionContract` is refused when the new value is an older version (`release.CheckUpdate`, no downgrade). An unchanged value is always allowed.
- The webhook does not check the upgrade path or the Kubernetes skew. Those are the plan's rule and the executors' (see "the upgrade target rule").

### The upgrade target rule

A target for a Talos or Kubernetes upgrade is valid when it is a final `vX.Y.Z` (no pre-release, no build metadata, not the `vX.Y` shorthand), newer than the running version, and:

- **Talos**: `upgrade.TalosPath(running, target)` accepts it. This defers to `pkg/machinery/compatibility`'s `TalosVersion.UpgradeableFrom`, the rule machinery encodes: the target's minor release sets a minimum running version (Talos 1.14: `MinimumHostUpgradeVersion` 1.12.0), a maximum (exclusive) version for a downgrade (1.16.0), and a list of denied versions. Within those bounds Talos may skip minor releases. A target minor that machinery does not know is refused ("upgrades to version ... are not supported").
- **Kubernetes**: `upgrade.KubernetesSkew(running, target)` accepts it (at most one minor release up, within the same major, never backwards), and `upgrade.KubernetesPairing(target, spec.talosVersion)` accepts it (the cluster's Talos supports the target, machinery's `KubernetesVersion.SupportedWith`).

Sources for the rules:

- Talos, "Upgrading Talos Linux", Supported upgrade paths (docs.siderolabs.com/talos/v1.14/configure-your-talos-cluster/lifecycle-management/upgrading-talos): "this migration is only tested between adjacent minor releases. Thus the recommended upgrade path is to always upgrade to the latest patch release of all intermediate minor releases." The machinery bounds above are the code that enforces what Talos allows; the docs recommend adjacent minors.
- Kubernetes, the version skew policy (kubernetes.io/releases/version-skew-policy): an upgrade moves at most one minor version at a time; `pkg/upgrade/kubernetes_skew.go` cites it.

Both rules are applied by the server's plan (`uiapi` planOperation, for a newer target only; a target that is the same or older keeps the pre-0113 no-op and downgrade rules) and by `ichor upgrade`. The executors (ADR 0043, 0057) check them again and check the Kubernetes pairing of the Talos target, as before. The candidate list is exactly these rules applied to the candidates (see "the version picker"), so a target the list offers passes them.

The Talos 1.14 documentation also says that from Talos 1.14 the `ghcr.io/siderolabs/installer` image is no longer published, and installers are served by the Image Factory as `factory.talos.dev/metal-installer/<schematic-id>:<version>`. The candidate source for Talos targets must therefore not assume that the tags of `ghcr.io/siderolabs/installer` list the Talos releases of 1.14 and later.

### The compatibility table

Compatibility is enforced by one typed table: `pkg/release/compat.go`, maintained with each Ichor release, with the source URL of each entry in a comment. It holds the Kubernetes range each Talos release runs and each Cilium chart runs. Two functions read it:

- `release.TalosKubernetesError(talos, kubernetes)`, nil when the pair is compatible; the error names the rule.
- `release.CiliumKubernetesError(cilium, kubernetes)`, nil when the Cilium runs the Kubernetes.

Where machinery's compatibility package knows the Talos release, the answer comes from machinery (the pairing rule above); the table covers what machinery does not. `release.ErrUnknownVersion` means neither machinery nor the table knows the version. The wording of each refusal names a step the user can take (`pkg/release/steps.go`):

- A Kubernetes step whose installed Cilium cannot run the target: "upgrade Cilium to <embedded> first, the chart this Ichor embeds, which runs Kubernetes <k8s>", when the embedded Cilium runs it. Cilium moves only to the chart embedded in the running Ichor (ADR 0098), so when that chart does not run the target either, the refusal says "this Ichor ships Cilium <embedded>, which does not run Kubernetes <k8s>: a newer Ichor release is needed".
- A Talos or Kubernetes pair the table marks incompatible: the error from the table, which names the supported range.

Compatibility is enforced, and it is not a note. The override verb (ADR 0110) is the only way past a compatibility refusal:

- The operation path (`PlanOperation`, `CreateOperation`) takes the override request field (`Override`, as node actions and etcd restores do). Without it the refusal stands. With it, the caller must hold the override verb on the cluster (`authorizeOverride`), and the override is recorded: the plan carries a warning and `CreateOperation` writes a Warning Event with the reason ("Override") on the TalosCluster.
- The webhook path passes a refused pair when the caller holds the override verb (the RightCheck), and logs the override.
- A version that neither machinery nor the table knows is refused on every path, and no override passes it.

The UI shows an incompatible candidate only behind an override checkbox, and disables it with the reason otherwise.

### The version picker

The Talos and Kubernetes upgrade dialog offers the target as a searchable list of candidates. The controller builds the list, not the browser:

- Candidates are the tags of `ghcr.io/siderolabs/installer` for Talos and of `ghcr.io/siderolabs/kubelet` for Kubernetes, read through the ADR 0114 egress policy and registry client. The Talos source is subject to the registry note above. The list is cached in `IchorInstallation.status.upgradeTargets`.
- The UI receives the full filtered list once and filters it in the frontend (a stock preactcn combobox). There is no server-side search.
- Offered: final releases newer than what runs and valid under the upgrade target rule. Talos: the patches of the current minor and the next minor (no skipping, though machinery would allow it; the picker offers only the adjacent step, and a typed version still passes the rule). Kubernetes: supported by the cluster's Talos (pairing) and at most one minor up (skew).
- Groups, newest first: "Recommended" (the release set's version, only when newer than the running version), "Patch" (newer patches of the current minor), "Next minor".
- The default is Recommended if present, else the newest patch of the current minor.
- A compatibility refusal shows as described above, with the override checkbox.
- A typed version not in the list can be chosen as "Use vX.Y.Z" (private mirrors, offline). The server plan still validates it (newer, path, pairing, compatibility).
- If the list cannot be fetched (the check is off, the list is missing, or the fetch fails), the combobox still works with the typed entry and shows the fetch error as a muted note.
- CLI: `ichor upgrade talos|kubernetes --list` prints the same groups. Without VERSION it uses the same default.

### Adopt

Adopt judges the versions it takes over (`checkAdoptCompat`, after the cluster is loaded):

- Refused, before anything is written, when a version the cluster runs is unknown to both machinery and the table (`ErrUnknownVersion`): Ichor could not operate the cluster. The Talos floor v1.12 (`adoptMinTalos`, ADR 0103) is still refused by its own gate.
- Warned, and adopted as it runs, when the pair is incompatible (Talos with Kubernetes, or the Cilium that adopt installs, the embedded chart, with Kubernetes). The warning says that an upgrade that would make the pair compatible is refused until it is.
- Older or newer than the set is not a reason to refuse or warn.

`--version-contract` is accepted for any contract the Talos supports, as before 0113. The contract is probed from the running Talos.

### What is tested

The table is data, and the rules are code. The tests run the rules, not the table alone:

- Talos paths, through machinery's compatibility package (`pkg/release` tests): the running versions tested are v1.11.5 (refused, below the 1.12.0 minimum of Talos 1.14), v1.13.2 to v1.14.3 (adjacent minor, accepted), v1.12.4 to v1.14.3 (skips 1.13, accepted within the bounds), and v1.14.3 to v1.14.4 (patch, accepted). The target v1.15.0 is refused as a minor machinery does not know. Talos 1.12 and 1.13 as targets are not tested: their rules are machinery's, and no Ichor build has offered them as targets yet.
- Kubernetes skew and pairing: v1.36.2 to v1.37.1 on Talos 1.14.3 (accepted), v1.35.0 to v1.37.1 (two minors, refused), v1.37.1 to v1.38.0 (not supported by Talos 1.14.3, refused).
- The refusals name the step (`TestCheckKubernetesStepNamesTheOrder`), and the webhook and the operation service refuse an incompatible pair and accept it under the override verb.
- Adopt warns for a known incompatible pair and refuses an unknown version (`TestCheckAdoptCompatWarnsOrRefuses`).
- Init refuses an incompatible set (`TestCheckInitCompatRefusesAnIncompatibleSet`); the pinned set is checked to be compatible (`TestPinnedIsConsistentWithMachinery`).

## Why this shape

- The first install is the one place the build's tested matrix is the right answer, so the set stays there. Later versions are the user's choice, so the set does not bind them.
- Compatibility is a table in Go, not a per-cluster setting: the webhook, the plan, the CLI, adopt and the picker read one table, so they cannot disagree.
- Refusals are overridable only by the override verb, as every other safety guard is (ADR 0110), so an admin can act in an incident without a new mechanism, and the Event keeps the record.
- Upgrades are per component, as ADR 0043 and ADR 0057 intended. A rollout that moves three components in sequence is a choice the user can make by running three operations, not a built-in step.

## Consequences

- `pkg/release` keeps `Pinned`, `Compare`, `CheckUpdate`, `CheckTalosTarget`, `CheckKubernetesTarget`, `CheckTalosStep`, `CheckKubernetesStep`, and the compatibility functions of `compat.go`. `CheckCreate`, `CheckTarget`, `Adoptable` and the rollout `Steps` are removed.
- `ichor upgrade cluster` and `PlanClusterUpgrade`/`StartClusterUpgrade` are removed. The generated API that still declares them is removed with them (proto and gen, owned by the listing change).
- The CRDs are unchanged (ADR 0026 kinds; append-only fields). The override request field for plan and create is added to the operation protos; the Versions card's proto state field is documented as no longer carrying the set comparison.
- The Versions card and its states change accordingly; see the UI change of the same release.
- A cluster that runs an incompatible pair is reported as incompatible (the upgrade path names it) and is not blocked from other edits.

## Open points

- **The embedded set.** The table must hold the pinned set together: `Pinned` (Talos, Kubernetes and the embedded Cilium) is checked by `TestPinnedIsConsistentWithMachinery`. When it is not, the first install is refused by `checkInitCompat`, so the set and the chart move together in one change.
- **The Talos candidate source** (see the registry note above). The picker follows the Talos docs' installer location for 1.14 and later.
- **Talos 1.15 and later** are not in machinery's table of this build, so they are refused as unknown until a build that knows them.

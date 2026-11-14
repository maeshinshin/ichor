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
- `ichor init` keeps the set. `--talos-version`, `--kubernetes-version` and `--version-contract` default to the set, and any other value is refused (`checkPinnedFlags`). A first install is the build's tested matrix; a different version is an upgrade, run after the install. Init also runs `checkInitCompat` on the set and refuses it when the compatibility table does not hold the Talos, Kubernetes and Cilium of the set together.

### What 0113 loses (superseded)

- **0113 item 3 (adopt refuses newer, reports behind).** Superseded. Adopt takes the cluster as it runs, older or newer than the set. It refuses a cluster it cannot operate and warns about an incompatible one; see "adopt" below. There is no "behind" state. The version contract is probed from the running Talos, as it was before 0113.
- **0113 item 4 (the webhook refuses versions other than the set).** Superseded. See "the admission rule" below.
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

- **Talos**: `upgrade.TalosPath(running, target)` accepts it. It defers to `pkg/machinery/compatibility`'s `TalosVersion.UpgradeableFrom`, the rule machinery encodes per target minor. For the target's minor machinery sets a minimum host version (Talos 1.14: 1.12.0), a maximum host version for a downgrade (exclusive: 1.16.0), and a list of denied host versions (empty for 1.14). Within those bounds Talos may skip minor releases. A target minor that machinery's switch does not list is refused ("upgrades to version ... are not supported"); this build's switch covers Talos 1.2 to 1.14, so 1.15 is refused.
- **Kubernetes**: `upgrade.KubernetesSkew(running, target)` accepts it (at most one minor release up, within the same major, never backwards), and `upgrade.KubernetesPairing(target, spec.talosVersion)` accepts it (machinery's `KubernetesVersion.SupportedWith` for the cluster's Talos).

Sources for the rules:

- Talos, "Upgrading Talos Linux", Supported upgrade paths (docs.siderolabs.com/talos/v1.14/configure-your-talos-cluster/lifecycle-management/upgrading-talos): "this migration is only tested between adjacent minor releases. Thus the recommended upgrade path is to always upgrade to the latest patch release of all intermediate minor releases." The code allows a skip within machinery's bounds, which is wider than the docs' recommendation. The picker offers only the adjacent minor (see "the version picker"); a typed skip still passes the rule.
- Kubernetes, the version skew policy (kubernetes.io/releases/version-skew-policy), as cited by `pkg/upgrade/kubernetes_skew.go`: an upgrade moves at most one minor version at a time.

Both rules are applied by the server's plan (`checkUpgradeTarget` in `uiapi` `planOperation`, for a newer target only; a target that is the same or older keeps the no-op and downgrade rules), and by the executors (ADR 0043, 0057), which check them again and check the Kubernetes pairing of the Talos target. The CLI checks only that a VERSION is a semantic version and leaves the rules to the server's plan. The candidate list is these rules applied to the candidates, so a target the list offers passes them.

### Talos candidate source

The Talos 1.14 docs say that from Talos 1.14 the `ghcr.io/siderolabs/installer` image is no longer published, and that installers are served by the Image Factory as `factory.talos.dev/metal-installer/<schematic-id>:<version>`. The Talos candidate list therefore reads the tags of `ghcr.io/siderolabs/installer-base`, which carries a tag per release: v1.14.3 returns 200 there, and 404 on `ghcr.io/siderolabs/installer`. The host is ghcr.io, which the release check's egress policy already allows (ADR 0114), so no new host is added. Image Factory is not a ghcr.io host, and the controller does not pull installers, so it needs no egress for this. The Kubernetes list reads `ghcr.io/siderolabs/kubelet`.

### The compatibility table

Compatibility is enforced by one typed table: `pkg/release/compat.go`, maintained with each Ichor release. Each entry has its source URL in a comment.

- **Cilium** (`ciliumKubernetes`): the Kubernetes minors each Cilium minor supports. 1.18: 1.30 to 1.33 (docs.cilium.io/en/v1.18/network/kubernetes/requirements/). 1.19: 1.31 to 1.34 (docs.cilium.io/en/v1.19/network/kubernetes/requirements/). 1.20: 1.33 to 1.36 (docs.cilium.io/en/v1.20/network/kubernetes/requirements/). A Cilium minor not in the table is unknown, and nothing is compatible with it.
- **Talos** (`talosKubernetes`): Kubernetes ranges for Talos minors, from the Talos support matrix. Talos 1.14: 1.33 to 1.37 (docs.siderolabs.com/talos/v1.14/getting-started/support-matrix). Where the table has a Talos minor, it decides the pair, and machinery must still accept the pair (it bounds the table). Machinery decides the Talos minors the table lacks (amendment 2026-11-03, below).

Two functions read the table:

- `release.TalosKubernetesError(talos, kubernetes)`: machinery's pairing first, then the Talos table, else the pair is not known and not compatible. The error names the rule.
- `release.CiliumKubernetesError(cilium, kubernetes)`: nil when the Cilium minor runs the Kubernetes.

`release.ErrUnknownVersion` means neither machinery nor the table knows the version. The wording of each refusal names a step the user can take (`pkg/release/steps.go`):

- A Kubernetes step whose installed Cilium cannot run the target: "upgrade Cilium to <embedded> first, the chart this Ichor embeds, which runs Kubernetes <k8s>", when the embedded Cilium runs it. Cilium moves only to the chart embedded in the running Ichor (ADR 0098), so when that chart does not run the target either, the refusal says "this Ichor ships Cilium <embedded>, which does not run Kubernetes <k8s>: a newer Ichor release is needed".
- A Talos or Kubernetes pair the table marks incompatible: the error from the table, which names the supported range.

Compatibility is enforced, and it is not a note. The override verb (ADR 0110) is the only way past a compatibility refusal:

- The operation path (`PlanOperation`, `CreateOperation`) takes the `override` request field (field 7 on both requests; the same request shape node actions and etcd restores use). Without it the refusal stands. With it, the caller must hold the override verb on the cluster (`authorizeOverride`). The plan carries the override as a warning, and `CreateOperation` writes a Warning Event with the reason "Override" on the TalosCluster, with the reason the caller gave.
- The webhook path passes a refused pair when the caller holds the override verb (the RightCheck), and logs the override.
- A version that neither machinery nor the table knows is refused on every path, and no override passes it.
- The CLI's operation start (`ichor upgrade talos|kubernetes`) sends the override when `--override` and `--override-reason` are given, the same flags node actions and restores take (`overrideOf(apiOpts)` into `startSpec.Override`), on both the plan and the create.

In the UI, the picker offers only compatible versions, so the override is used by the plan refusal path. A refused target shows its reason, with an override checkbox; otherwise the checkbox is disabled with the reason.

### The version picker

The server builds the choices; the browser does not decide compatibility.

- **Candidates.** The Talos and Kubernetes tags are read from the repositories in "Talos candidate source" through the ADR 0114 egress policy and registry client. The controller's release check (ADR 0114 switch) writes them to `IchorInstallation.status.upgradeTargets`: `checkedAt`, `talos[]`, `kubernetes[]` and `error`. The check covers the three repository hosts (the chart's, Talos's and Kubernetes's). A failed listing keeps its last good list and sets `error`.
- **`ListUpgradeTargets`** (one cluster, one kind: talos or kubernetes) filters the recorded final releases against the cluster's Talos and Kubernetes from its spec:
  - Talos (`release.TalosChoices`): newer, `upgrade.TalosPath` accepts it, and `TalosKubernetesError` accepts it with the cluster's Kubernetes. Cilium is not part of the Talos rule.
  - Kubernetes (`release.KubernetesChoices`): newer, `upgrade.KubernetesSkew` accepts it, `TalosKubernetesError` accepts it with the cluster's Talos, and `CiliumKubernetesError` accepts it with the Cilium the status shows. An unknown Cilium offers nothing. The plan itself checks Cilium only when one is observed.
- **Groups**, newest first:
  - "Recommended": the release set's version, only when it is in the recorded list, newer than the running version, and valid.
  - "Patch": the newer patches of the running minor.
  - "Next minor": releases of the next minor only. Talos may skip a minor (the path rule allows it), but the offer is the adjacent minor; a typed skip still passes the plan's rule. Kubernetes is at most one minor up.
- **Default.** "Recommended" if present, else the newest patch of the running minor, else none.
- **Availability.** The response says whether the list can be used and why not: the check is off, no list has been recorded yet, or the last listing failed (the last good list is still used, and the reason says it may be stale).
- **CLI.** `ichor upgrade talos|kubernetes --list` prints the same groups and the default and exits. Without VERSION the command uses the same default. A VERSION is validated by the server's plan.
- **A typed version** that is not in the list is valid when the plan's rules accept it (newer, path, pairing, compatibility). The UI offers a "Use vX.Y.Z" entry for a typed version, and a typed fallback when the list cannot be fetched.

### The Cilium upgrade

The Cilium upgrade is not in `ListUpgradeTargets`, which lists only talos and kubernetes. Cilium moves only to the chart embedded in the running Ichor (ADR 0098), with its own operation (`StartCniUpgrade`, ADR 0099). A Kubernetes plan refuses a target the installed Cilium cannot run, and names the order (`CheckKubernetesStep`).

### The UI

The picker (a stock preactcn combobox over the grouped `ListUpgradeTargets` lists, filtered in the frontend, with a "Use vX.Y.Z" entry and the offline fallback to a plain input with the reason as a muted note), the override checkbox in the plan view, the Patch-available hint (a newer compatible patch of the running minor, on the Talos, Kubernetes and Cilium rows) and one attention item ("Patch updates are available for Talos, Kubernetes") are the UI's part of this decision. The Cilium row keeps its state badge and links to the Cilium page for its upgrade; its Upgrade button does not open the kube-proxy dialog.

### Adopt

Adopt judges the versions it takes over (`checkAdoptCompat`, after the cluster is loaded):

- Refused, before anything is written, when a version the cluster runs is unknown to both machinery and the table (`ErrUnknownVersion`): Ichor could not operate the cluster. The Talos floor v1.12 (`adoptMinTalos`, ADR 0103) is still refused by its own gate.
- Warned, and adopted as it runs, when the pair is incompatible (Talos with Kubernetes, or the Cilium that adopt installs, the embedded chart, with Kubernetes). The warning says that an upgrade that would make the pair compatible is refused until it is.
- Older or newer than the set is not a reason to refuse or warn.

`--version-contract` is accepted for any contract the Talos supports, as before 0113. The contract is probed from the running Talos.

### CLI

- `ichor upgrade talos [VERSION]` and `ichor upgrade kubernetes [VERSION]`: without VERSION they use the default (see the version picker); with VERSION they move to it. `--list` prints the groups and exits.
- `ichor upgrade cluster` is removed.

### Proto

- `PlanClusterUpgrade` and `StartClusterUpgrade` stay in the proto, marked deprecated. They are unused and answer `CodeUnimplemented` (`internal/uiapi/operation_upgrade_cluster.go` delegates to the generated Unimplemented handler). Removing them fails `make proto-breaking`.
- `ListUpgradeTargets` is added: the request names a cluster and a kind (talos or kubernetes), and the response carries `recommended`, `patch`, `next_minor`, `default`, `available` and `unavailable_reason`.
- `override` (field 7, `Override`) is added to `PlanOperationRequest` and `CreateOperationRequest`.

## Why this shape

- The first install is the one place the build's matrix is the right answer, so the set stays there. Later versions are the user's choice, so the set does not bind them.
- Compatibility is a table in Go, not a per-cluster setting: the webhook, the plan, the CLI, adopt and the picker read one table, so they cannot disagree.
- Refusals are overridable only by the override verb, as every other safety guard is (ADR 0110), so an admin can act in an incident without a new mechanism, and the Event keeps the record.
- Upgrades are per component, as ADR 0043 and ADR 0057 intended. A rollout that moves three components in sequence is a choice the user can make by running three operations, not a built-in step.

## Consequences

- `pkg/release` keeps `Pinned`, `Compare`, `CheckUpdate`, `CheckTalosTarget`, `CheckKubernetesTarget`, `CheckTalosStep`, `CheckKubernetesStep`, the compatibility functions of `compat.go`, and the candidate functions of `targets.go` (`Choose`, `TalosChoices`, `KubernetesChoices`, `AtLeast`). `CheckCreate`, `CheckTarget`, `Adoptable` and the rollout `Steps` are removed.
- `ichor upgrade cluster` is removed. `PlanClusterUpgrade` and `StartClusterUpgrade` remain in the proto as deprecated RPCs that answer `CodeUnimplemented`.
- The CRDs gained one status field, `IchorInstallation.status.upgradeTargets`. No spec field changed, and the append-only rule holds (ADR 0026 kinds). The override request field is added to the operation protos. The Versions card's proto state field is documented as no longer carrying the set comparison.
- The Versions card and its states change accordingly; see the UI change of the same release.
- A cluster that runs an incompatible pair is reported as incompatible (the upgrade path names it) and is not blocked from other edits.

## Open points

- **The Talos 1.14 Kubernetes range (decided 2026-11-03: the support matrix wins).** The typed table holds Talos 1.14 with Kubernetes 1.33 to 1.37, and the table decides that minor; machinery bounds it. See the amendment below.
- **The embedded set.** The table must hold the pinned set together: `Pinned` (Talos, Kubernetes and the embedded Cilium) must be consistent with machinery. When it is not, the first install is refused by `checkInitCompat`, so the set and the chart move together in one change.
- **Talos 1.15 and later** are not in machinery's table of this build, so they are refused as unknown until a build that knows them.

## Amendment 2026-11-03: the Talos 1.14 range follows the support matrix, and Cilium patches are verified

The user decided two points on 2026-11-03.

### 1. The Talos 1.14 Kubernetes range

The Kubernetes range of a Talos minor follows the official Talos support matrix, not machinery's wider table. Talos 1.14 supports Kubernetes 1.33 to 1.37 (docs.siderolabs.com/talos/v1.14/getting-started/support-matrix). Machinery v1.14.5 accepts 1.32.0 to 1.37.99.

- `pkg/release/compat.go` holds `talosKubernetes`, keyed by Talos minor, with `"1.14": {min: "1.33", max: "1.37"}`.
- `TalosKubernetesError`: when the table has the Talos minor, the table decides. A Kubernetes outside the range is refused. A Kubernetes inside it must still pass `upgrade.KubernetesPairing`, which bounds the table. A Talos minor the table lacks is decided by machinery as before.
- Kubernetes 1.32.0 is refused with Talos 1.14, although machinery accepts it; 1.33.0 and 1.36.6 are accepted.

### 2. Cilium security patches before an Ichor release (option A)

Superseded by amendment 2026-11-04 (3) below, which removes this path; kept for its reasoning.

A Cilium patch of the embedded chart's minor is offered and run before an Ichor release carries it. The release keeps the embedded chart and runs the patch's agent, operator and hubble-relay images, pinned by digest, after each image's cosign signature verifies.

**Candidates.** The controller lists `quay.io/cilium/cilium` tags (`releasecheck.DefaultCiliumRepository`). It keeps the final releases of the embedded chart's minor that are newer than the chart, at most five per check (newest first). Each candidate's three images must resolve and verify, then the patch is listed in `IchorUpgradeTargets.cilium` (new status field, append-only). A patch whose signature does not verify is not listed.

**Verification (`internal/imagesig`; the Cilium check described here was removed by the amendment of 2026-11-04 (3)).** Cilium signs its images with cosign keyless since 1.13 (docs.cilium.io/en/latest/configuration/verify-image-signatures/). The signatures are OCI referrers of the image digest, stored as Sigstore bundles (v0.3). The check uses the OIDC issuer `https://token.actions.githubusercontent.com` and an anchored SAN regular expression for `https://github.com/cilium/cilium/.github/workflows/build-images-releases.yaml@refs/tags/<tag>`, the identity the docs name for that tag. Every referrer with the cosign sign predicate is checked with sigstore-go against the embedded trusted root (`internal/imagesig/trusted_root.json`, fetched from the Sigstore TUF repository). The check is offline: the bundles carry their transparency-log inclusion proofs.

**Dependency.** `github.com/sigstore/sigstore-go` v1.3.0 (released 2026-07-30). It is the official Sigstore verifier and the smallest one that verifies bundles offline; the cosign library pulls in the whole CLI, and the cosign binary is not run. `github.com/google/go-containerregistry` was already in the module graph and is now a direct requirement, for the referrers and digests.

**Egress.** The release check's controller policy (ADR 0114) gets one host: `quay.io` on 443/TCP, written only while the check is on and only when the Cilium candidates are checked. The Sigstore checks need no other host: the trusted root is embedded, and the bundles carry their proofs.

**Applying (a cni operation with `spec.cni.imageVersion`).** `StartCniUpgrade` takes an optional `version`. It must be one the server offers as a Cilium patch (in `IchorUpgradeTargets.cilium`, newer than the running Cilium, not reached by the chart, and compatible with the cluster's Kubernetes through `release.CiliumKubernetesError`). The operation then runs in `upgradeChart`:

1. the controller resolves each image's tag to its digest and verifies its signature again (the controller holds the quay egress; ichor-ui and the CLI do not, so the verification happens at apply, not in the plan);
2. the Helm Job runs with the chart's `image.override`, `operator.image.override` and `hubble.relay.image.override` set to `repository:tag@sha256:digest`, passed as the job's `--image-version` and `--image-override key=reference` flags;
3. after the rollout, the override is written to `status.cni.imageOverride` (new status field, controller-written; the spec is not touched).

Every later Job keeps the override: `EffectiveValuesForMode` reads it, and `CNIValuesHash` includes it, so an unrelated values change does not drop the patch. A deployed release counts as done for a patch only when its values hash matches the patch's.

**Dropping.** `cilium.ImageOverrideActive` is nil once the embedded chart reaches or passes the patch. The reconciler clears `status.cni.imageOverride` then, and `CiliumChoices` offers nothing at or below the chart. A newer Ichor's chart therefore replaces the patch without an operation.

**UI and CLI.** The Cilium row shows "Patch available" from the same list as Talos and Kubernetes, and the attention list names Cilium. The Cilium page's upgrade card holds the stock combobox (`VersionPicker`) over the verified patches; an empty choice is the embedded chart upgrade, as before. `ichor upgrade cni [VERSION] [--list]` is the existing command with the optional version and `--list`; there is no new `ichor upgrade cilium` verb. `ListUpgradeTargets` accepts the `cni` kind (the patch group only).

**Proto.** `StartCniUpgradeRequest.version` (field 2). `ListUpgradeTargets` documents the `cni` kind. Both are additive.

**Open points.**

- Envoy and certgen are not overridden. The 1.20.4 chart pins `cilium-envoy` (v1.39.5-…) and `certgen` by digest, and a patch that bumps Envoy would not get it. The Envoy tag of a patch cannot be read from the Cilium tag. Decided 2026-11-03 (user): Envoy and certgen stay at the chart's digest; an Envoy fix reaches a cluster through an Ichor patch release, because reading a patch's unsigned chart from a further host costs more than Envoy's exposure in Ichor's default configuration (on the data path only for L7 policy, Ingress or Gateway API).
- The trusted root is a snapshot of the public-good Sigstore root, embedded. It must be refreshed when Sigstore rotates its keys; `trusted_root.json` and the `sigstore-go` version in go.mod are refreshed together.
- Cost: each candidate's agent signature is an 8.5 MB referrer. The verified images are remembered in memory only, so a controller restart verifies again. Five candidates at most per check.
- The candidates follow the embedded chart's minor. A cluster on Cilium 1.19 takes the chart upgrade first (one minor at a time, ADR 0099).
- A Cilium release that changes its signing workflow's file name stops verifying: its patches are refused and not offered, which is the intended failure.

## Amendment 2026-11-03 (3): the Cilium rule applies to a Cilium CNI only

A Kubernetes step is checked against Cilium's table only when the cluster's CNI is Cilium: managed (`spec.cni.preset: cilium`) and observed as `status.cni.detected: cilium`. A cluster observed on another CNI (flannel, calico, ...) has no Cilium release to match, so the Cilium half is skipped for it. Reading the detected version whatever the CNI took a flannel cluster's version for a Cilium release and refused its Kubernetes plan as "cilium 0.28.9 is not known".

- `release.CiliumRelease(detected, detectedVersion)` returns the Cilium release only when `detected` is `cilium`. The webhook (`ValidateUpdate`), the uiapi plan (`installedCilium`) and the Cilium status (`ciliumVersion`) use it, so no consumer reads `status.cni.detectedVersion` of another CNI.
- An observed non-Cilium CNI leaves the Kubernetes target list bounded by the Talos pairing alone (`release.KubernetesChoices(..., ciliumConstrains)`). An unobserved CNI still offers nothing until Cilium is seen, as before.
- `ichor adopt` in unmanaged mode (`--cni-unmanaged` or `spec.cni.preset: unmanaged`) installs no Cilium, so `checkAdoptCompat` judges only the Talos pair. A managed adopt and `ichor init` still judge the chart this Ichor embeds.
The Cilium Kubernetes range comes from each release's CI matrix, not the docs (`pkg/release/compat.go`, `ciliumKubernetes`). The matrices, read from the release tags: Cilium 1.18.16 runs Kubernetes 1.30 to 1.33 (`.github/actions/ginkgo/main-k8s-versions.yaml`); 1.19.10 runs 1.32 to 1.35; 1.20.4 runs 1.33 to 1.36. The 1.19 entry was 1.31 to 1.34, which came from the requirements page, and that page is stale at v1.19.10: `compatibility.rst` there lists 1.32 to 1.35, the same as the matrix. Since a Cilium minor is only compatible with a Kubernetes minor its CI tests, the matrix decides. For 1.19, 1.32 and 1.35 are accepted; 1.31 and 1.36 are refused. Consequence: a cluster on Cilium 1.19 can move Kubernetes to 1.35 (the CNI step is refused for 1.36 until the chart reaches 1.20).

## Amendment 2026-11-04: a Cilium patch can be started under the override when only the compatibility check refuses it

The Talos and Kubernetes dialogs lift an incompatible step with the override verb (ADR 0110); the Cilium upgrade could not, because `StartCniUpgradeRequest` carried no override. It now does (field 3, `Override override`, the message the operation requests use).

- A patch the server does not offer is refused. If it is verified, newer than the embedded chart and of a known Cilium minor, and only `release.CiliumKubernetesError` against the cluster's Kubernetes fails, the refusal is overridable: it carries the response header `Ichor-Overridable: true`, and the same request with `override` set is accepted when the caller holds the `override` verb on the TalosCluster (a SelfSubjectAccessReview as the caller; without a user, or without the verb, it is refused). A Warning Event `Override` on the cluster records "cilium <patch> is incompatible (...), overridden under override: <reason or no reason given>".
- Never overridable, with or without the verb: a patch whose images are not verified (not in the verified list), a patch that is not newer than the embedded chart or not of the running minor, a Cilium minor the table does not know (`ErrUnknownVersion`), and an unusable release list. The controller still verifies the signatures before the Job runs the images.
- There is no plan RPC for the cni upgrade: `StartCniUpgrade` is the dry run (the refusal), and the dialog shows the override checkbox and the optional reason only after a refusal marked overridable. `ichor upgrade cni` takes the existing `--override` and `--override-reason`.
## Amendment 2026-11-04 (2): the release check lists v-prefixed tags, and each list reports its own failure

The release check offered no Talos, Kubernetes or Cilium patch because `releasecheck.HelmLister` used Helm's `registry.Client.Tags`, which keeps only strict semantic versions without a leading `v` (`semver.StrictNewVersion`). The chart's tags have none; the Talos (`installer-base`), Kubernetes (`kubelet`) and Cilium repositories tag `v1.14.5`, `v1.37.2`, `v1.20.4`. Every image listing therefore returned no tags and no error, and `status.upgradeTargets` held only `checkedAt`.

- `releasecheck.RegistryLister` replaces `HelmLister`: it lists tags over the OCI distribution API with oras-go (`remote.Repository.Tags`, anonymous, every page) and returns them unfiltered; `Finals` already selects the final releases.
- Each list records its own failure: `status.upgradeTargets.talosError`, `kubernetesError` and `ciliumError` (appended fields; `error` stays and joins them). A repository that fails keeps its last good list, as before, and does not mark the other lists unavailable: `ListUpgradeTargets` reports the error of the kind asked for. A status written by an earlier controller has only `error`, which counts against every list until the next check.
- Patch offers stay online-only. The compatibility table holds Talos, Kubernetes and Cilium minors, not their patch versions, and a Cilium patch is offered only after its images verify, so there is no offline fallback to offer. The reason is shown instead: the Versions card has a muted line per cause, "Couldn't check for Cilium patches: <reason>", for each list that could not be given (`unavailableNotes`).
- No Cilium patch is offered while the embedded chart is the newest patch of its minor: that is an empty list, not a failure.
## Amendment 2026-11-04 (3): Cilium moves only to the chart this Ichor embeds; the image-only patch path is removed

The user decided on 2026-11-04 that Ichor pins the Cilium version: the only Cilium a cluster moves to is the chart embedded in the running Ichor. A newer upstream patch reaches a cluster with an Ichor release (ADR 0119 opens the pull request that bumps the chart), after which the chart upgrade of ADR 0099 is offered as `CiliumUpgradeAvailable`. This supersedes section 2 of the amendment 2026-11-03 (the verified patches, option A) and the override of the amendment 2026-11-04 for a Cilium patch. Talos and Kubernetes keep their per-component upgrades.

**Why.** Cilium ships its chart and its images as one set. A patch run as the embedded chart with other images is a combination no release ships, and it made the detected version lie (the agent image said one version, the chart another). With one chart per Ichor release there is one pinned combination per release. The cost the 2026-11-03 amendment accepted, the Envoy and certgen digests that a patch could not move, also disappears.

**Removed.**

- The release check lists no Cilium repository and verifies no image. `ReleaseCheck` has no `CiliumRepository` or `Images`, the egress policy no longer carries `quay.io`, and the Cilium identity of `internal/imagesig` and its check are deleted. The package itself, with the embedded Sigstore trusted root and the `sigstore-go` dependency, stays: it resolves a tag to a digest and verifies the cosign keyless signature of Ichor's own installer image (`imagesig.IchorIdentity`, ADR 0122, and `internal/tools/verifyimage`). `upgradeTargetsOf` writes neither `cilium` nor `ciliumError` and clears what an earlier controller wrote. There is no "a newer Cilium exists" hint: without a reachable Ichor release listing it would claim something Ichor cannot act on, and with one the Ichor row already says a newer Ichor exists.
- `release.CiliumChoices`, the `cni` kind of `ListUpgradeTargets` (answers InvalidArgument), `ciliumPatchOffer`, the compatibility override for a patch, and `StartCniUpgrade`'s `version` (answers InvalidArgument naming the reason; `override` is ignored).
- `cilium.WithImageOverrides` and `ImageOverrideActive`: the effective values never render `status.cni.imageOverride`. The Helm Job has no `--image-version` or `--image-override` flags. `ichor upgrade cni` takes no version and has no `--list`.
- The auto-patch decision has no Cilium candidate (ADR 0116). `cilium` stays a valid value of `autoPatch.components` and of the chart and flag defaults, because stored settings and the CRD enum must keep working; it selects nothing. Cilium chart upgrades stay manual, as ADR 0116 already said.
- The UI: the Cilium upgrade dialog has no version picker and no override fields. It names the embedded chart in its title and starts that upgrade; the Versions card and the Cilium page show it only when the release is behind the embedded chart.

**Kept, unused (append-only).** `IchorUpgradeTargets.cilium` and `ciliumError`, `TalosClusterCNIStatus.imageOverride` and `TalosCiliumImageOverride`, `TalosOperationCNISpec.imageVersion`, and the proto fields `StartCniUpgradeRequest.version` and `override`. Their documentation says deprecated and unused.

**A cluster that already runs a patch: a recorded patch is frozen, not dropped.** An Ichor upgrade must never step Cilium back, and the build that shipped the image-only path (v0.1.0) may have left a cluster on, for example, 1.20.5 images over an embedded 1.20.4 chart. Only clusters upgraded by that build carry `status.cni.imageOverride`.

- A recorded `status.cni.imageOverride` newer than the embedded chart keeps being rendered (`cilium.ImageOverrideActive`, `EffectiveValuesForNodes`, so the Helm Job reads it from the cluster's status and needs no flags). The values hash includes it, so it does not move on an Ichor upgrade and no Job runs. No new override is ever set: `StartCniUpgrade` refuses a version, and the executor refuses an operation an earlier build created with `imageVersion`, with a message that says to delete it and run `ichor upgrade cni` again. A Job that an earlier controller created with the removed flags and that is still running when Ichor is upgraded fails on the unknown flag, and the Job's failure handling applies as for any failed Cilium Job.
- A recorded override at or below the embedded chart is cleared by the controller (`dropPatchImages`). The chart carries those images, so the next values apply is no step back, and the normal chart upgrade takes over.
- The frozen case is shown in the `CiliumUpgradeAvailable` condition, not as an Event and not in `CNIValuesApplied`, because a Job's progress rewrites the latter: "Cilium runs patch 1.20.5 over chart 1.20.4 (set by an earlier Ichor); it stays until an Ichor embedding 1.20.5 or newer" (appended to the up-to-date or upgrade-available message). The Warning Event `CiliumPatchDropped` and the `Recorder` on `CNIHandoverReconciler` are removed.
- Files kept, not removed: `cilium.WithImageOverrides`, `ImageOverrideActive`, the `imageOverride` status field and `TalosCiliumImageOverride` (their documentation now says "recorded by an earlier Ichor, none set any more"). Still removed: the Helm Job flags, the release check's Cilium listing, the Cilium identity of `imagesig`, the UI picker, `CiliumChoices`.

**Note (v0.1.0 cleanup).** `releasecheck.DefaultCiliumRepository`, named under Candidates above, was removed in the v0.1.0 cleanup because nothing used it.

**Note (v0.1.0 cleanup).** `release.CheckTalosStep` (Decision and Files lists above) was also removed: it only forwarded to `release.TalosKubernetesError`, which its callers now use.

Amendment 2026-11-05 (ADR 0130): the operation start's override carries no reason. `ichor upgrade talos|kubernetes` and `ichor upgrade cni` take `--override` alone (no `--override-reason`), and the override checkbox of the plan view and of the Cilium dialog has no reason box.

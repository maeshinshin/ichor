# 0113. The cluster follows the release set of its Ichor

Status: Accepted · Date: 2026-11-03 · Design section: Upgrades · Touches: 0043, 0057, 0059, 0098, 0099, 0103

Decided: the Talos release, its version contract, the Kubernetes release and the Cilium chart are one release set embedded in each Ichor build (`pkg/release`); init and adopt use it, every upgrade moves only to it, and `ichor upgrade cluster` (or the Versions card's one button) brings a cluster to it one step at a time.

## Context

Until now the versions were free choices. `ichor init` required `--talos-version` and `--version-contract` and defaulted Kubernetes to whatever the Talos machinery library defaulted to. `ichor upgrade talos|kubernetes VERSION` accepted any semver target. Nothing said which combination Ichor supports, so an Ichor build could be asked to run a Talos, Kubernetes or Cilium combination it had never been tested with, and the Cilium chart is already fixed in the binary (ADR 0098). On 2026-11-03 the user decided that the Kubernetes, Talos and Cilium versions of a managed cluster are bound to the Ichor version: each build pins exactly one supported set, and there is no free choice of arbitrary versions.

The Talos docs (v1.14, "Upgrading Talos Linux") say an OS upgrade does not upgrade Kubernetes, that Kubernetes upgrades are managed separately, and that the Kubernetes version must be one the target Talos supports (the support matrix; machinery's compatibility table gives 1.32.0 to 1.37.99 for Talos 1.14). Cilium's upgrade guide allows only one minor release at a time and asks for the latest patch first (the chart step check, ADR 0099, already enforces the one-minor rule).

## Decision

1. **One set, in Go.** `pkg/release` holds `Pinned`: Talos `v1.14.3`, version contract `v1.14`, Kubernetes `v1.37.1` (machinery's default for Talos 1.14.3; a test checks both that and the pairing with Talos), and the Cilium chart from `pkg/cni/cilium` (`1.18.0`). Init, adopt, the CLI (`ichor version` prints it under `release`), the API (`Cluster.releaseSet`, and `Cluster.versions` with one state per component), the admission webhook and the rollout all read this one value. The set is what the Ichor build is tested with; choosing another version means running another Ichor build.

2. **init takes the set.** `--talos-version`, `--kubernetes-version` and `--version-contract` are kept as deprecated flags whose default is the pinned value and which are refused with any other value (`checkPinnedFlags`). They are no longer required.

3. **adopt judges the running versions.** A cluster running an older Talos or Kubernetes than the set is adopted and reported as "behind"; a newer one is refused before anything is written. The version contract is still probed from the running Talos, so it is never newer than Talos. `--version-contract` is accepted only with the pinned contract, and refused for a cluster that is behind (its contract follows its Talos).

4. **The admission rule.** The TalosCluster webhook (`release.CheckCreate`, `release.CheckUpdate`) accepts a new cluster whose versions are the set's or older. An update that changes `spec.talosVersion`, `spec.kubernetesVersion` or `spec.versionContract` is accepted only when the new value is the pinned one and not lower than the stored one. An unchanged value is always accepted, so a behind cluster can still be edited for something else. The per-component operations refuse any Talos or Kubernetes target other than the pinned one (`ichor upgrade talos|kubernetes` default to it), before anything is written (the server's `planOperation`, the same check the CLI makes).

5. **No override for the set.** The `override` verb of ADR 0065/0110 gates a safety check on a cluster. The release set is Ichor's supported matrix, not a per-cluster check: what an override would allow is a version this build has not been tested with. So the webhook refuses a non-pinned version for every user, including a cluster admin, and the only way to another version is another Ichor build. The CRDs and the operation kinds are unchanged (ADR 0026 kinds, append-only fields).

6. **The rollout is one user action in steps.** `ichor upgrade cluster` (flag `--follow`) and the Versions card's one button call `PlanClusterUpgrade` and `StartClusterUpgrade`. Each start creates the operation of the first step still needed; the next step waits for the previous one to finish (the unfinished-operation rule already refuses a second). The steps, in this order:
   - **talos**: Talos on every machine, one at a time, and the version contract moves to the pinned one in the same write. Talos goes first because Kubernetes must be supported by the Talos that runs it (the Kubernetes executor checks the pairing, ADR 0057), and because Talos does not upgrade Kubernetes by itself.
   - **config**: only when a machine has a configuration change the new contract needs (`upgrade.ConfigPending`) and the Talos step is done and Kubernetes is still behind. The Kubernetes executor refuses while a machine has a pending change it does not apply, so the change is applied first. The plan shows what it reboots.
   - **kubernetes**: the control planes, then kube-proxy, then the kubelets, without reboots (ADR 0087).
   - **cilium**: the Cilium release last (`StartCniUpgrade`, ADR 0099). The new chart meets the Kubernetes the set was built for, and Cilium's own rule of one minor at a time is enforced by the chart step check.

   It is never automatic: Ichor does not start a step on its own, and a step that starts a second time in one `--follow` run stops the run rather than loop.

7. **Ichor's own version is not in the set.** `ichor upgrade ichor` (IchorInstallation.spec.version, ADR 0059) is unchanged. A new Ichor build brings its own set, so after an Ichor upgrade the cluster reports "behind" until the rollout is run.

8. **The Versions card** shows the state of each component against the set ("Up to date" or "Behind"), with the Ichor row unchanged, and the kube-proxy switch unchanged. The Talos and Kubernetes rows lose their free-version upgrade buttons; the rollout button replaces them.

## Why this shape

- One value in Go is the only place the set can disagree with itself: the webhook, the CLI, the API and the controller cannot each hold a different idea of the matrix.
- Steps instead of one operation: the existing operation kinds each finish before the next starts, and each has its own plan and confirmation (ADR 0068). A rollout that created all of them at once would be refused by the unfinished-operation rule, so the rollout is a sequence the user confirms, not a new operation kind.
- No automation: moving three components on a cluster is a change to the datapath and to every machine. The user chooses the moment, reads the plan and confirms each step. `--follow` is the same choice made once for a run.

## Open points

- **Cilium's Kubernetes range.** The Cilium documentation I could retrieve covers newer releases (1.33 to 1.36 e2e tested for the current stable line). I could not confirm from the docs which Kubernetes releases Cilium 1.18.0 is tested with, so the pinned Kubernetes `v1.37.1` may be newer than Cilium 1.18.0's tested range. Before this set ships, check the Cilium 1.18 compatibility page and, if 1.37 is not in it, move the set (Kubernetes or the Cilium chart) in this file's code, not per cluster.
- **The version contract moves with Talos.** A contract change rewrites rendered configuration for machines that have not yet rebooted; the config step applies it once Talos is done. If a future contract changes more than the configuration the Kubernetes step can tolerate, the plan will show a config step and the rollout refuses to hide it.

## Consequences

- `pkg/release` is the one place to change a version. Moving the set is a code change, a test run, and a new Ichor build.
- The CRDs are unchanged. The API gained `PlanClusterUpgrade`, `StartClusterUpgrade`, `Cluster.versions` and `Cluster.releaseSet`; the existing fields are untouched.
- An existing cluster built with another Talos or Kubernetes version is adopted or kept as it is, and reports "behind" until it is rolled. The webhook refuses a version outside the set on every later change to it.
- The per-component commands and their operation kinds remain for one step at a time; their targets are the set's version only.

## Amendments to earlier decisions

- **ADR 0043 (Talos upgrade executor).** The target is the release set's Talos, and the version contract moves with it in the same write (see the rollout's Talos step). The executor itself is unchanged.
- **ADR 0057 (Kubernetes upgrade executor).** The target is the release set's Kubernetes. The executor's pairing and skew checks are unchanged, and it runs after the Talos step.
- **ADR 0059 (Ichor installation and self-upgrade).** An Ichor upgrade does not move the cluster. The cluster follows the new build's set through the rollout.

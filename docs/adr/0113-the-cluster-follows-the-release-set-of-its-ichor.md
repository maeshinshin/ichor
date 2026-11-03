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

- **Cilium's Kubernetes range (resolved by the amendment below: the set is now Kubernetes 1.36.6 with Cilium 1.20.4).** The Cilium documentation I could retrieve covers newer releases (1.33 to 1.36 e2e tested for the current stable line). I could not confirm from the docs which Kubernetes releases Cilium 1.18.0 is tested with, so the pinned Kubernetes `v1.37.1` may be newer than Cilium 1.18.0's tested range. Before this set ships, check the Cilium 1.18 compatibility page and, if 1.37 is not in it, move the set (Kubernetes or the Cilium chart) in this file's code, not per cluster.
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

## Amendment 2026-11-03: the set moves to Talos v1.14.5, Kubernetes v1.36.6 and Cilium 1.20.4

The set in `pkg/release` (`Pinned`) is now:

| Component | Was | Now |
| --- | --- | --- |
| Talos (machinery module and `Pinned.Talos`) | v1.14.3 | v1.14.5 |
| Version contract | v1.14 | v1.14 (unchanged) |
| Kubernetes (`Pinned.Kubernetes`) | v1.37.1 | v1.36.6 |
| Cilium chart (`cilium.ChartVersion`, `pkg/cni/cilium/chart/cilium-1.20.4.tgz`) | 1.18.0 | 1.20.4 |

The Decision above is unchanged in form; these values replace the numbers it names.

**Why these versions.** No released Cilium tests Kubernetes 1.37 yet, so a 1.37 set would be untested with the CNI. The compatibility sources are:

- Cilium 1.20 compatibility page (`docs.cilium.io/en/v1.20/network/kubernetes/compatibility/`, read 2026-11-03): "All Kubernetes versions listed are e2e tested and guaranteed to be compatible with Cilium", and the table lists 1.33, 1.34, 1.35, 1.36. It does not list 1.37. Kubernetes 1.36.6 is inside this range.
- For Cilium 1.18 (tested with 1.30 to 1.33) and 1.19 (1.31 to 1.34), the figures come from the lead's research, not from a page I could read in this session; they are not needed for the set, because the set pins 1.20.
- Talos v1.14 support matrix (`docs.siderolabs.com/talos/v1.14/getting-started/support-matrix`, read 2026-11-03): Kubernetes "1.37, 1.36, 1.35, 1.34, 1.33". Machinery v1.14.5's compatibility table is wider (`compatibility/talos114`: minimum 1.32.0, maximum 1.37.99); the set uses the documented range. Kubernetes 1.36.6 is inside both. The `pkg/release` comment that said 1.32 was wrong and is fixed.
- Machinery's `constants.DefaultKubernetesVersion` for v1.14.5 is 1.37.1. The set is deliberately older than that default, so the release test checks that the set is not newer than the default and that `upgrade.KubernetesPairing` accepts the set's Kubernetes with its Talos. It no longer checks equality.

**Cilium values.** The Talos v1.14 Cilium guide (`docs.siderolabs.com/kubernetes-guides/cni/deploying-cilium`, read 2026-11-03) still names chart 1.18.0, but its values are the ones Ichor renders: `ipam.mode: kubernetes`, `kubeProxyReplacement` (false, or true with `k8sServiceHost: localhost` and `k8sServicePort: 7445`), the `securityContext.capabilities` lists, `cgroup.autoMount.enabled: false` and `cgroup.hostRoot: /sys/fs/cgroup`. In chart 1.20.4 every one of these keys still exists with the same meaning, and `certgen.tolerations` is still read by the hubble TLS CronJob. The chart's own default capability list adds `SYSLOG`; Ichor keeps the guide's list because a Helm list replaces the chart's list rather than merging with it. The 1.19 and 1.20 upgrade notes (`docs.cilium.io/en/v1.20/operations/upgrade/`) remove or rename Helm values and agent flags that Ichor does not set (`clustermesh.enableMCSAPISupport`, `encryption.ipsec.interface`, `--k8s-api-server`, `--node-port-*`, `dnsProxy.preCache`, `preflight.tofqdnsPreCache`). The notes on `toServices` with `toPorts`, the NodePort egress change and the empty-policy admission rule do not match any policy Ichor renders; Ichor's own CiliumNetworkPolicies use `toEntities`, `toEndpoints` (DNS through the DNS proxy) and the controller's `toFQDNs` and `toCIDRSet` egress. No values change is needed for the Ichor defaults. The chart's new defaults (`configDriftDetection`, `policy-default-local-cluster=true`, `datapathMode` still `veth`) are accepted. The goldens in `pkg/cni/cilium/testdata` are regenerated from 1.20.4.

**One minor at a time.** A future Ichor release may move Cilium by at most one minor. Cilium's own upgrade notes require one minor at a time, and the chart step check enforces it. Adopt's rule (`pkg/cni/cilium/takeover.go` `CheckVersion`, ADR 0103 section 1.2) is unchanged in form: the live Cilium minor equals the chart's minor or is exactly one lower. With chart 1.20.4 that means live 1.20 or 1.19. A live 1.18 is refused and must be upgraded to 1.19 with the tool that installed it first.

**Open for the user.**
- `upgradeCompatibility` (Cilium's value for the first install's version) is not set. Cilium recommends setting it to the version the cluster was first installed with; Ichor does not know that version for an adopted cluster, so it stays the chart default. Decide before an adopted cluster is upgraded.
- `pkg/cni/cilium/chart/cilium-1.18.0.tgz` is still in the repository. The embedded chart is 1.20.4 only; the foreign-cluster hardware tests (ADR 0103 and 0104, `test/e2e/foreign`, `test/internal/foreign`, `test/harness/foreign`) still install Cilium 1.18.0 from that file, and adopt now refuses a live 1.18. Those tests need a 1.19 foreign chart or a rewrite; they were not run for this amendment.

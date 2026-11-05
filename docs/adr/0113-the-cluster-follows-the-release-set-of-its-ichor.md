# 0113. The cluster follows the release set of its Ichor

Status: Accepted · Superseded in part by 0115 · Date: 2026-11-03 · Design section: Upgrades · Touches: 0043, 0057, 0059, 0098, 0099, 0103

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

**Cilium values.** The Talos v1.14 Cilium guide (`docs.siderolabs.com/kubernetes-guides/cni/deploying-cilium`, read 2026-11-03) still names chart 1.18.0, but its values are the ones Ichor renders: `ipam.mode: kubernetes`, `kubeProxyReplacement` (false, or true with `k8sServiceHost: localhost` and `k8sServicePort: 7445`), the `securityContext.capabilities` lists, `cgroup.autoMount.enabled: false` and `cgroup.hostRoot: /sys/fs/cgroup`. In chart 1.20.4 every one of these keys still exists with the same meaning, and `certgen.tolerations` is still read by the hubble TLS CronJob. The chart's own default capability list adds `SYSLOG`; Ichor keeps the guide's list because a Helm list replaces the chart's list rather than merging with it. The 1.19 and 1.20 upgrade notes (`docs.cilium.io/en/v1.20/operations/upgrade/`) remove or rename Helm values and agent flags that Ichor does not set (`clustermesh.enableMCSAPISupport`, `encryption.ipsec.interface`, `--k8s-api-server`, `--node-port-*`, `dnsProxy.preCache`, `preflight.tofqdnsPreCache`). The notes on `toServices` with `toPorts`, the NodePort egress change and the empty-policy admission rule do not match any policy Ichor renders; Ichor's own CiliumNetworkPolicies use `toEntities`, `toEndpoints` (DNS through the DNS proxy) and the controller's `toFQDNs` and `toCIDRSet` egress. No values change is needed for the Ichor defaults. The chart's new defaults (`configDriftDetection`, `policy-default-local-cluster=true`, `datapathMode` still `veth`) are accepted. The goldens in `pkg/cni/cilium/testdata` are regenerated from 1.20.4. The chart renders one role Ichor did not name before, `cilium-operator-ztunnel`, under its default values; the Cilium Job's role (`config/rbac/ichor-cni`, mirrored in `dist/chart/templates/rbac/ichor-cni.yaml`) now names it (ADR 0108's guard). It needs only daemonset and serviceaccount reads, which that role already holds, so no permission is added.

**Render and operator replicas.** The bootstrap renders for the set's Kubernetes (`cilium.RenderKubernetesVersion`, checked against `Pinned.Kubernetes`), and a one-control-plane cluster runs one Cilium operator replica. The hand-over stalled on the rs2 run without this; the decision and its evidence are in ADR 0098's amendment of 2026-11-03.

**One minor at a time.** A future Ichor release may move Cilium by at most one minor. Cilium's own upgrade notes require one minor at a time, and the chart step check enforces it. Adopt's rule (`pkg/cni/cilium/takeover.go` `CheckVersion`, ADR 0103 section 1.2) is unchanged in form: the live Cilium minor equals the chart's minor or is exactly one lower. With chart 1.20.4 that means live 1.20 or 1.19. A live 1.18 is refused and must be upgraded to 1.19 with the tool that installed it first.

**Open for the user.**
- `upgradeCompatibility` (Cilium's value for the first install's version) is not set. Cilium recommends setting it to the version the cluster was first installed with; Ichor does not know that version for an adopted cluster, so it stays the chart default. Decide before an adopted cluster is upgraded.
- `pkg/cni/cilium/chart/cilium-1.18.0.tgz` is still in the repository. The embedded chart is 1.20.4 only; the foreign-cluster hardware tests (ADR 0103 and 0104, `test/e2e/foreign`, `test/internal/foreign`, `test/harness/foreign`) still install Cilium 1.18.0 from that file, and adopt now refuses a live 1.18. Those tests need a 1.19 foreign chart or a rewrite; they were not run for this amendment.

## Hardware verification (rs2, 2026-11-03)

One throwaway cluster on the outer `ichor-e2e` namespace, prefix `ichor-e2e-rs2`: one control plane (3Gi) and one worker (1536Mi). The set is Talos v1.14.5, Kubernetes v1.36.6, Cilium 1.20.4. The result: the set installs and passes every check below. One stall on the first attempt was a product bug (the operator rollout), fixed before the passing run (ADR 0098 amendment).

**Setup.** The Talos v1.14.5 ISO DataVolume `ichor-e2e-talos-v1-14-5-37656798-base` was created with the harness's cache manifest (the schematic and version are the only changes; the v1.14.3 DataVolume was not touched) and stays in place. Images `v0.0.2-g7f445d62` (the fixed tree) were built with `make demo-images-build` and pushed with `make demo-images-push REGISTRY=localhost:5102` through a port-forward to the registry Service. VMs: `ICHOR_UI_DEMO_PREFIX=ichor-e2e-rs2 ICHOR_UI_DEMO_CP_MEMORY=3Gi ICHOR_UI_DEMO_WORKER_MEMORY=1536Mi ./bin/kubevirt-harness up-demo`, control plane at 10.244.1.238 and worker at 10.244.1.99, both in maintenance mode. `ichor init` ran from a busybox tools pod with no version flags (the set's defaults), `--preset single --cni cilium --cni-tunnel geneve --endpoint-type node --discover-address 10.244.1.99 --registry-mirror ghcr.io=http://<registry>:5000`, and no UI users (init warned that ichor-ui runs with `--allow-unauthenticated`, which is expected without users).

**Runs.**
- Run 1 (image `v0.0.1-gf3282230`, before the fix): init reached the Cilium hand-over. The Helm job rolled the operator Deployment because the bootstrap and the hand-over rendered different pod templates (`hostUsers`, see ADR 0098). On one node the second operator replica could never schedule (the host ports 9234 and 9963 were held by the old pod, and the chart requires one operator per node), and the hand-over stalled. The cluster was torn down after diagnosis. This is the first of the two failures the run allows.
- Run 2 (image `v0.0.2-g7f445d62`, the fix): init started 08:29:09Z, the control plane was Ready, and the Cilium hand-over completed at 08:33:17Z ("Cilium is now a Helm release owned by Ichor"), about 4 minutes after start. The Cilium image pull took about 54 seconds. Init exited after the hand-over with no error line. The worker was approved by hand with `ichor machine approve <name> --role worker --install-disk /dev/vda --hostname ichor-e2e-rs2-w --yes --local` after init had finished, and it joined at 14:05:25Z.

**Checks (run 2, read directly).**

| check | command | result |
| --- | --- | --- |
| node versions | `kubectl get nodes -o wide` | control plane and worker Ready; Talos v1.14.5, kubelet v1.36.6 |
| TalosCluster versions | `kubectl get taloscluster ichor-e2e-rs2 -o jsonpath=...` | spec v1.14.5 / v1.36.6 / contract v1.14; status `cni.detectedVersion` v1.20.4; `CNIHandover` Complete; `CiliumUpgradeAvailable` False |
| Cilium DaemonSet | `kubectl -n kube-system get ds cilium` | 2 of 2 ready, image `quay.io/cilium/cilium:v1.20.4@sha256:20fe92…` |
| Cilium status | `cilium-dbg status --brief` in the agent on each node | `OK` on the control plane and on the worker |
| operator | `kubectl -n kube-system get pods` | one replica, 1/1 Running, no Pending pod |
| Hubble certgen Job | `hubble-generate-certs-732172e311` | Completed |
| pod-to-pod across nodes | busybox `nc` from the control plane to a listener on the worker (10.248.1.133:8080) | `ok`, rc=0 |
| pod-to-Service across nodes | same listener through a ClusterIP Service | `ok`, rc=0 |
| Ichor CiliumNetworkPolicies | `kubectl -n ichor-system get ciliumnetworkpolicies` | 6 of 6 `Valid` (controller-manager, controller-release-check, controller-talos-egress, default-deny, installer, ui) |
| ichor-ui in cluster | HTTPS `GET https://ichor-ichor-ui-service.ichor-system.svc:8443/` from a client pod | `HTTP/1.1 200 OK` |
| dynamic egress | `ichor-controller-talos-egress` `toCIDRSet` | 10.244.1.238/32 and 10.244.1.99/32 on ports 50000, 6443 and 2380; the worker's address is present |
| release-check policy | `ichor-controller-release-check` | `Valid` |

**Not checked.** The egress policy's history (it was read after the worker joined, not before), a negative test that a non-allowed flow is dropped, and the full `cilium-dbg status` output.

**Observations.**
- A single-node cluster runs one Cilium operator replica (ADR 0098 amendment). The second replica would never have scheduled.
- The chart's `hubble-generate-certs` Job is a one-shot pod; it is Completed, and the Pending state it showed while the node was NotReady was transient.
- ichor-ui runs unauthenticated in this throwaway cluster, as init reported. Users were not enabled.

## Amendment 2026-11-04: the Cilium operator runs as many replicas as the cluster has nodes, and a failing Helm Job backs off

Found on hardware, root cause verified by experiment. It is the same bug class as the operator rule in ADR 0098's amendment of 2026-11-03, which covered only `spec.controlPlane.count == 1`.

**The bug.** An HA cluster (`spec.controlPlane.count == 3`) is bootstrapped with one node and the others join later. `EffectiveValuesForMode` passed the desired count to the preset, which set `operator.replicas: 1` only for a count of 1, so with a count of 3 the chart default of 2 replicas applied. The operator Deployment is `hostNetwork` with host ports 9234 and 9963 and a required pod anti-affinity per hostname, and its strategy is `maxSurge: 25%`, `maxUnavailable: 50%` (chart 1.20.4 `values.yaml`, `operator.replicas: 2`, `operator.updateStrategy`). On a one-node cluster the second replica stays Pending ("didn't have free ports"). Any later change to the operator's pod template (a Cilium values edit, a chart upgrade such as 1.20.3 to 1.20.4, an image override) then cannot roll: the new pod cannot schedule and the old one cannot be killed. Helm `--wait --atomic` (`pkg/cni/cilium/helm.go`, 10 minutes) times out and rolls back.

**The circular wait.** The Helm Job holds the cluster lock `ichor-controller/cni-helm` for its whole run, and control-plane joins are deferred while that lock is held (`ClusterLockHeld` in the TalosMachine reconciler). The rollout needs those very nodes. When the Job fails, `runJob` releases the lock and `retryFailedJob` recreated the identical Job one minute later, for ever; control-plane joins got through only in the gap between two Jobs.

**Decision.**
- **Replicas follow the nodes that exist.** `cilium.EffectiveValuesForNodes(cluster, mode, nodes)` takes the number of Node objects the cluster has now. One node, or `spec.controlPlane.count == 1` (unchanged, workers included), gives `operator.replicas: 1`; two or more nodes leave the chart default of 2, so the values (and the hash) of a cluster that already had two or more nodes do not change. Zero means unknown and falls back to the desired count, which is what the UI preview and the adopt readback still pass. Every Node counts, Ready or not: the first node is NotReady until Cilium is up, and the operator tolerates every taint. The controller counts them with `clusterNodeCount` (uncached read, metadata only), hashes the values with that count (`CNIValuesHash(cluster, mode, nodes)`) and hands the Job `--nodes=N` (`ichor cni helm apply`), so the Job applies the values that were hashed even if a node joins in between. The hand-over, the kube-proxy switch and the chart upgrade all do this. `Bootstrap` (CLI) counts the nodes it sees. When the count crosses from one to two the hash changes once, and a Job scales the operator up; for that the hand-over controller also watches Node creation and deletion (not updates) and enqueues every cluster. No `operator.updateStrategy` is set: the chart already renders `maxUnavailable: 100%` for exactly one replica (`templates/cilium-operator/deployment.yaml`), which is what lets a one-replica operator roll.
- **A failing Job backs off.** `retryFailedJob` waits `cniRetryBackoff(interval, failures)`: the retry interval (one minute), doubled for each failure of the same values hash, capped at 30 minutes. The count is `status.cni.jobFailures` with `status.cni.jobFailuresHash` (two appended status fields; nothing was removed), updated when the failed Job is removed for the next attempt. A different hash is another Job and starts again at once (the count is ignored for it); a Job that completes clears the count. The `CNIValuesApplied` condition message now ends "failed N times in a row with these values, next attempt in D". The `NoPod` path (a Job whose pod was refused) is unchanged.

**Evidence.** The hardware observation above (Pending operator replica on a one-node HA cluster, Helm timeout and rollback, repeated identical Jobs). The rendered chart is the source for the rest: replicas 2 by default, strategy 25% and 50%, and the one-replica `maxUnavailable: 100%` template branch.

**Tests.** `TestOperatorReplicasFollowTheNodesThatExist` (pkg/cni/cilium: 1 node gives 1, 2 or more give 2, an unknown count falls back to the desired count, count 1 stays 1), `TestBootstrap_OperatorFollowsTheNodesThatExist`, `TestCNIHelmApplyOperatorFollowsTheNodes`, `TestCNIValuesHashFollowsTheNodeCount`, `TestClusterNodeCountCountsEveryNode`, `TestANodeEventEnqueuesEveryCluster`, `TestCNIRetryBackoffDoublesUpToACap`, and in the hand-over suite "waits longer after each failure of the same values" and "counts the failures of one set of values, starts over for other values and clears them on success".

**Known limits.** The UI's values preview still uses the desired count, so for an HA cluster that has one node it shows the chart default of 2 replicas while the cluster runs 1; both sides of its before/after diff use the same count, so the diff is correct. A cluster whose release was deployed with the chart default before this change and has only one node keeps its stuck operator until a Job runs; the changed hash starts that Job once the controller is upgraded.

**Follow-up (2026-11-04): the preview follows the nodes too.** The first limit above is gone. The UI backend (and so `ichor cni values|diff|set`, which run the same handlers) counts the Nodes live, with a list of Node metadata, and renders the layers, the diff and the result of a write with `cilium.EffectiveValuesForNodes`, the function the controller hashes and applies. A preview is therefore what the next Job applies, and it now also carries what that function adds beyond the chart defaults (a Cilium patch's images, `spec.security.wireGuard`). Chosen over recording the count in `status.cni` because `ichor-ui` already has `get;list` on Nodes (`GetNode`), so no RBAC is added, no CRD field is added, and the count is right before the first Job has run, which is the window where an HA cluster has one Node and nothing in status yet. A reader that may not list Nodes (a CLI user without the right) falls back to the desired control-plane count, as before. `ichor adopt`'s takeover preflight keeps the desired count: it is the count of the control planes it just discovered. Tests: the "on an HA cluster" specs of the CniService suite (one Node gives one operator replica in the layers, the diff and a write; three Nodes give the chart default; no Nodes falls back to the desired count).

**Follow-up (2026-11-04): the bootstrap render counts one node.** On hardware, right after `ichor init` of an HA cluster (`controlPlane.count` 3) the `cilium-operator` Deployment had two replicas with one Node until the first Helm Job set it to one: the inline manifest `ichor init` renders into the first control plane's machine config (`buildCNIBootstrap`, `internal/cli/init_cni.go`) was the one render left with an unknown node count, which falls back to the desired count. It is rendered with `Preset.WithNodes(1)` now, because it only takes effect on the first control plane, when that is the only Node. `cilium.Bootstrap` (used by `ichor cni bootstrap` and `ichor adopt` on a cluster with no CNI) already lists the Nodes and counts the real ones, so an existing multi-node cluster keeps its real count; adopt's takeover preflight keeps the desired count as above. No hash is involved: the hand-over compares nothing with the inline manifest's bytes (the Job adopts the live objects into the Helm release and the controller hashes the effective values with the live node count), and the manifest lives in a TalosConfigPatch that `ichor init` writes once and `pkg/render` stops rendering once `CNIHandover` is true. A cluster that is already handed over therefore sees no machine config change, and one that is not yet keeps the patch it was created with. Test: `TestBuildCNIBootstrap_HAOperatorStartsWithOneReplica`.

Amendment 2026-11-06 (hardware run I, M1 and M4): a failed `ichor-cni-*` Job of values the user then moved on from stayed in kube-system until its 24 h `ttlSecondsAfterFinished` (the TTL of every Ichor Job, `jobTTL`, already applies; the retry above deletes a failed Job only while its own values are the cluster's), and when the same values were set again the old failed Job of that name was reported as `JobFailed ... next attempt in 40s` for up to 45 s before a new one started. The hand-over now deletes the failed values Jobs whose values hash is not the cluster's (`pruneSupersededFailedJobs`): before it advances a Job, under the cluster lock, and in the early path where Cilium already runs the values (a revert), where it keeps none. It forgets the failure count of the values left behind, so a later return to them starts at the base interval. Only Jobs named for a plain values hash are touched; the salted Jobs of a kube-proxy switch or a chart upgrade are not. The latest failure of the current values is kept, as before, for the backoff and the condition. Tests: "removes the failed Job of values the user moved on from..." and "removes the failed Job of an edit that was reverted..." in `cnihandover_controller_test.go`.

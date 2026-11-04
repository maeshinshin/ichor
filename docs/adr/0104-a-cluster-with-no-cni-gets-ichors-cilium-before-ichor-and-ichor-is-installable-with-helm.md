# 0104. A cluster with no CNI gets Ichor's Cilium before Ichor, and Ichor is installable with Helm

Status: Accepted; built; hardware-verified, E5 and E6 included (E5 passed on 2026-10-30 after the pending-record repair; see Hardware results) · Date: 2026-10-29 · Design section: Bootstrap (`ichor adopt`), Cilium · Builds on: 0098, 0099, 0101, 0103 · Amends: 0103 (the "no CNI" row of 1.1, 1.1a's gate wiring, test D5, open question 3)

Decided: `ichor adopt` on a Talos cluster with no CNI installs Ichor's pinned Cilium itself, in the adopt process with the Helm SDK as Ichor's own release record in `ichor-system`, before Ichor's own pods are installed, and adopts the cluster as the managed `cilium` preset (so ADR 0103's "no CNI becomes unmanaged `talosCNI: none`" row is replaced, and `--cni-unmanaged` is refused there); a cluster that already has a CNI keeps ADR 0103's rules and its network-policy gate follows live detection; every wait for Ichor in `adopt` and `init` is bounded and names why the pods are Pending; and Ichor can be installed with `helm install` of `dist/chart` (published as an OCI chart), after which `ichor adopt` detects the existing install, skips its own install step and registers the cluster, with a new `ichor cni bootstrap` for the one case Helm cannot cover, a cluster with no pod network.

In this ADR, **adopt** means: bring an existing, running Talos cluster under Ichor's management without rebuilding it.

## Context

Hardware testing of ADR 0103 found two defects.

- **D5, no-CNI cluster hangs.** ADR 0103 adopts a cluster with `cluster.network.cni.name: none` and no CNI as `preset: unmanaged, talosCNI: none`. But that mode installs Ichor into the cluster (open question 4 was answered "adopt installs Ichor"), and without a CNI the nodes are `NotReady`, so Ichor's controller, webhook and UI pods stay Pending. The adopt write phase then waits for the admission webhooks (`createWhenWebhooksUp`, 30 minutes by default, retrying silently) and gets nowhere. The unmanaged mode was never able to work for this input; ADR 0103 left "installing Cilium onto it" as open question 3.
- **D3, policies skipped on a hands-off Cilium cluster.** ADR 0103 1.1a says Ichor's `CiliumNetworkPolicy` objects follow live detection (`installer.IncludeCiliumPolicies(detected, crdPresent)`). `adopt_run.go` computes that gate correctly, but `adoptWriter.installIchor` then calls `applyNetworkPolicies(..., preset, ...)` with the cluster's CNI preset, and that function returns early when the preset is not `cilium` (`init_netpol.go`, "does not enforce CiliumNetworkPolicies"). For `preset: unmanaged` the policies are never applied, whatever the gate said.

The user decided (2026-10-29, in Japanese): for a cluster with no CNI, install Cilium; for a cluster that has a CNI, install with CNI management off (the existing unmanaged mode); and make Ichor installable with Helm too.

Constraints that stay: Cilium is Ichor's only CNI and Ichor's own Helm release (CLAUDE.md, ADR 0099/0101); the controller is the only writer to nodes and never mutates `spec`; `adopt` itself never writes to a node (ADR 0014, 0103); Ichor does not install storage, monitoring or GitOps tooling (ADR 0101); machine config is typed only; clean-room (nothing from Sidero Omni was read for this ADR).

### Facts this ADR relies on (read from the tree at 9873f59)

- `cilium.Apply(ctx, cfg, vals, opts)` (`pkg/cni/cilium/helm.go`) is the one place that installs or upgrades the release `cilium` in `kube-system`; with no Ichor record it runs `action.Install` with `TakeOwnership`, otherwise `Upgrade`. The `ichor cni helm apply` Job (`internal/cli/cni_helm.go`, `runCNIHelmApply`) is a thin caller: it builds a Helm `action.Configuration` whose storage namespace is `ichor-system` (`newHelmConfig`) and calls `Apply`. The Job pod has `hostNetwork`, tolerates everything and reaches the API through `localhost:7445` (KubePrism) (`internal/controller/cni_helm.go`).
- Effective values come from `cilium.EffectiveValuesForMode(cluster, mode)`; `k8sServiceHost`/`k8sServicePort` (localhost:7445), `kubeProxyReplacement` and `tunnelProtocol` are Ichor-owned (ADR 0099 decision 3).
- The hand-over reconciler's signal is "Helm release `cilium` is `deployed` at the effective-values hash and the DaemonSet is Ready" (ADR 0099 decision 2), not "the Job ran". It sets `CNIHandover=True`, and render drops the inline `cni` entry (or the recorded `takenOverInlineManifest`) only then.
- Talos enables KubePrism on 7445 by default (ADR 0099 relies on it); a no-CNI Talos cluster still has a working control plane, because the control-plane static pods, kubelet, kube-proxy (if enabled) and Cilium itself use the host network.
- `dist/chart` is a hand-maintained chart (`Chart.yaml` `version: 0.1.0`, `appVersion: "v0.1.0"`, `kubeVersion >=1.30.0-0`) with the CRDs under `templates/crd` (kept on uninstall: `crd.keep`, `helm.sh/resource-policy: keep`), the controller, webhook, UI, RBAC, `ichor-cni` RBAC (`cniHelm.enabled: true`), the optional installer identity (`installer.enabled: false`, "a chart-installed Ichor is upgraded through the chart") and the Cilium policies behind `networkPolicy.cilium.enabled` (null/true/false). It is **not published anywhere today**: no workflow pushes it, the only chart workflow is `helm lint`/a kind test (`.github/workflows/test-chart.yml`), and `values.yaml` image repositories are the placeholders `controller` and `ichor-ui`. The chart does not create the `IchorInstallation` object; `ichor init` and the adopt install step do (`createIchorInstallation`).
- Adopt's "Ichor is running" test is `ichorRunning`: whether the `IchorInstallation` singleton exists. A Helm-installed Ichor has none, so today `adopt` would apply the install manifest on top of the chart's objects.

## Decision

### 1. A cluster with no CNI: adopt installs Cilium first, then Ichor

**1.1 What "no CNI" means.** All of these hold, read by the inspection (unit 2 of ADR 0103) with nothing written:

- Talos says so: `cluster.network.cni.name: none` (or the CNI selection resolves to none).
- Kubernetes finds no CNI workload: no Cilium, Calico or other CNI DaemonSet in any namespace, no `CiliumNode` CRD, no CNI config on the nodes that Ichor can read (the existing mixed/other detection of 1.1).
- Every node is `NotReady` with a Ready condition whose message says the network plugin is not ready (`cni plugin not initialized` / reason `NetworkPluginNotReady`).

Partly true is not "no CNI": a cluster where some nodes are Ready, or where `cni.name: none` and a CNI was applied by the user by hand, is the **existing CNI** case (decision 2) and, for a CNI Ichor cannot classify, is refused as ADR 0103 already says. `talosCNI: none` stays a valid value of `spec.cni.unmanaged`, because it describes exactly that second kind of cluster (a user-applied Calico or Cilium with `cni.name: none`); it is no longer what a CNI-less cluster becomes.

**1.2 The mode.** The cluster is adopted with `spec.cni.preset: cilium` (managed). `--cni-unmanaged` on a no-CNI cluster is **refused**, with nothing written: "this cluster has no CNI, so no pod can run on it, including Ichor's. Run again without --cni-unmanaged and Ichor installs Cilium, or install a CNI yourself first." The table row of ADR 0103 1.1 becomes:

| Found | Result |
|---|---|
| No CNI installed (1.1 above) | **Managed Cilium.** Ichor installs its pinned Cilium (this ADR), then adopts. `--cni-unmanaged` is refused. |

**1.3 Preconditions** (checked read-only, before the plan asks for consent; each failure refuses with nothing written):

1. Talos v1.12 or later on every node and the other adopt gates, unchanged.
2. **KubePrism is enabled on port 7445 on every node** (`machine.features.kubePrism` or `KubePrismConfig`). It is Talos's default, so this is normally true. If it is not, adopt refuses and says to enable it first: with no pod network Ichor has nothing it can safely re-order, and Cilium reaches the API through `localhost:7445` (ADR 0099). This is stricter than ADR 0103 1.6 (which lets the first render turn KubePrism on) on purpose: KubePrism has to exist before Cilium can start, and the render is held until after Cilium.
3. Every node has a `spec.podCIDR` (kube-controller-manager allocates node CIDRs, the Talos default), because Cilium runs with `ipam.mode=kubernetes`.
4. The pod and service subnets are read from the Talos config (class A, already mapped to `spec.network`).

**1.4 What Cilium is installed with.** Everything is chosen, nothing is detected from an existing Cilium:

- `spec.cni.tunnel`: the API default (ADR 0098/0099); `--tunnel vxlan|geneve` on adopt overrides it before consent (immutable afterwards, as for `init`).
- `spec.cni.kubeProxy`: from the Talos side alone. Talos kube-proxy enabled (the `kube-proxy` DaemonSet exists; hostNetwork, so it runs without a CNI) gives `enabled` and `kubeProxyReplacement=false`; Talos kube-proxy disabled gives `replaced` and `kubeProxyReplacement=true` (KubePrism is already required by 1.3). There is no Cilium side to disagree with, so the refusals of ADR 0103 1.5 do not apply.
- No `spec.cni.cilium.values` unless `--cilium-values FILE` is given later by another ADR; adopt uses Ichor's defaults. The `TalosCluster` object is built in memory first (preset, tunnel, kubeProxy, annotation `ichor.maesh.dev/adopted`), and the Cilium values are `EffectiveValuesForMode(thatObject, mode)`; the write phase writes that same object. A test asserts the values installed in step 2 below equal the values the controller derives from the written object, so the controller sees a release at the right hash.

**1.5 The bootstrap order. The adopt CLI installs Cilium itself, in process, before Ichor.** The no-CNI path of the adopt phases (ADR 0103 1.8) is:

1. **Plan** (read-only; prints "no CNI found: Ichor will install Cilium <chart version> (tunnel X, kube-proxy Y) as its own Helm release, then install Ichor, then adopt"). Consent is required, as for a takeover.
2. **Cilium.** The CLI creates the `ichor-system` namespace if it is absent (Helm's record storage needs it; the install manifest later creates it as an `AlreadyExists` no-op), builds the same `action.Configuration` as `runCNIHelmApply` (records in `ichor-system`, the operator's kubeconfig), and calls `cilium.Apply` with the values of 1.4. It then waits, bounded (decision 3: 10 minutes), for the Cilium DaemonSet and operator to be Ready and for every node to report Ready. Nothing in this step needs a pod network: Cilium's agent and operator are `hostNetwork` and tolerate the not-ready taint, and they reach the API server through KubePrism.
3. **Ichor.** The install manifest, the `IchorInstallation`, and (now that Cilium is Ready and its CRD exists) the Cilium policies, with the gate of decision 2. The controller and webhook pods schedule because the nodes are Ready.
4. **Write CRs**: bundle Secret, `TalosCluster`, held `TalosMachine`s and patches, as in ADR 0103 1.8 phase 2. The webhook wait now succeeds.
5. **Hand-over.** The controller sees the release `deployed` at the effective-values hash and the DaemonSet Ready and sets `CNIHandover=True`. There is no inline entry (the machine config never had one), so nothing is removed, and no `ichor-cni` Job is created because the hash already matches (the unit that builds this asserts it; the Job is only a way to reach `Apply`, not part of the signal). Adopt waits for it with `waitHandover`, bounded.
6. **Release the holds** machine by machine, exactly as ADR 0103 phase 4.

**1.6 The render matches; no class C.** The live machine config of a no-CNI cluster is `cluster.network.cni.name: none`, no inline Cilium entry, KubePrism on, kube-proxy as recorded. The `cilium` preset renders `cni.name: none`, a `$patch: delete` of `KubeFlannelCNIConfig` (which deletes nothing that is there), `KubeProxyConfig enabled:false` only for `replaced`, KubePrism, and the inline `cni` entry only until `CNIHandover=True`. Because step 5 sets `CNIHandover=True` before step 6, the first render has no inline entry and equals the live config for every CNI-relevant path. A render test with a `cni.name: none` fixture asserts byte-equality of those paths (unit 3); hardware case E1 verifies it. If a node does differ, the existing class A/B/C machinery reports it and the hold keeps it unapplied.

**1.7 The hold.** Unchanged and now load-bearing in two places. The `TalosMachine`s are created held (step 4), so the controller computes drift and applies nothing until step 6. The Cilium install in step 2 happens before any `TalosMachine` exists, so the controller cannot race it with a config apply. A rerun after a failure is safe: `cilium.Apply` goes to `Upgrade` when its record exists (and to the `pending-install` repair of ADR 0103 unit 5 when the CLI died mid-install), the install manifest and CR creates tolerate `AlreadyExists`, and the plan on the rerun sees a Cilium with an Ichor record in `ichor-system`, which is the first row of ADR 0103 1.1 ("already handled; adopt only checks the release exists").

**1.8 Options considered and rejected.**

- **Cilium as a Talos inline manifest through the machine config, as `ichor init` does.** Rejected. `init` can do it because it writes the config before the nodes exist. Here the cluster is already running, `adopt` never writes to a node (ADR 0014, 0103), and the only other writer, the controller, cannot run without a pod network. Letting the CLI apply config to a live cluster just for this would add a node-writing exception to adopt. It would also leave an inline entry in a running cluster's machine config that has to be removed again by a second config change after the hand-over, and Talos's documented behaviour is that inline manifests are created and never updated (the Talos v1.14 Cilium guide), so the dependence of a post-bootstrap config change on a Kubernetes upgrade would have to be verified on hardware first. It would be two machine-config changes on a live cluster to reach the state step 2 reaches with none.
- **The CLI creates the `ichor-cni` Job.** Rejected. The Job is the controller's way to run `Apply` with cluster-admin-equivalent rights without making the long-lived controller cluster-admin (ADR 0099). The adopt CLI already holds the operator's credentials (it applies Ichor's CRDs and RBAC), so a Job adds a second place that builds the pod spec, a dependency on the installer image being pullable and on `ichor-cni`'s RBAC being applied first, and no extra safety. The cost of running in process is that a lost connection ends the wait; `Apply` is idempotent and the rerun continues, as in 1.7.
- **Adopt unmanaged and tell the operator to install a CNI.** Rejected by the user's decision; and it would make Ichor refuse its most common "just give me a cluster" input.
- **Cilium as a subchart of Ichor's own chart.** Rejected for the Helm path as well (decision 4.4): it would put Cilium inside Ichor's chart release and so under two owners.

### 2. A cluster that already has a CNI

ADR 0103 stays as written: Cilium is taken over (1.2) or left alone with `--cni-unmanaged`; flannel, Calico and other CNIs are adopted only with `--cni-unmanaged`, as the unmanaged preset; a mixed CNI is refused. "Install with CNI management off" is that unmanaged mode and needs no change.

**2.1 The D3 fix.** The network-policy decision is made once, by `installer.IncludeCiliumPolicies(detected, crdPresent)` in `writeInput`, and nothing else may veto it. `adoptWriter.installIchor` stops passing the cluster's CNI preset to `applyNetworkPolicies`: when `IncludeCiliumPolicies` is true it calls it as a Cilium cluster (`cniCilium`), so the preset-based early return, which exists for `ichor init --cni <other>`, cannot skip the policies on a hands-off Cilium. When the gate is false adopt prints which input failed ("detected CNI is calico" or "the CiliumNetworkPolicy CRD is not Established"), as the installer already does. Tests: unmanaged-on-Cilium applies policies; unmanaged-on-flannel and -on-Calico do not; managed Cilium (takeover and the new no-CNI path) does. For the no-CNI path the gate is evaluated after step 2, when Cilium is the detected CNI.

**2.2 `status.cni.detected` for the no-CNI path** is `cilium` from the moment step 2 ends, so the controller's own gate (installer Job on an upgrade) agrees with adopt's.

### 3. Waits are bounded and say why

Three waits in `adopt` and `init` can currently go on until a 30-minute default (`defaultWebhookWait`) with only the last transport error printed: the webhook wait of `createWhenWebhooksUp`, the Cilium Ready wait before the policies, and adopt's `waitHandover`. They share one helper that:

- has a hard timeout per wait: webhooks 15 minutes in `adopt` (30 in `init`, unchanged, because a first install pulls images over a slow link), Cilium Ready 10, hand-over 10; the flag `--wait-timeout` on `adopt` sets the webhook wait. On timeout the command exits non-zero and says what is left, never "waiting" forever;
- every 30 seconds while waiting lists the pods in `ichor-system` and, for each one that is Pending, prints its scheduler condition message (for example "0/3 nodes are available: 3 node(s) had untolerated taint {node.kubernetes.io/not-ready}"), and for a pod in `ImagePullBackOff` or `CrashLoopBackOff` the waiting reason;
- **fails early** when every Ichor pod has been unschedulable for two minutes and every node is NotReady with the network-plugin message: "Ichor's pods cannot start because this cluster has no pod network (its nodes are NotReady: cni plugin not initialized). Install a CNI, or let `ichor adopt` install Cilium (a cluster with no CNI)". After decision 1 adopt cannot reach this state (it installs Cilium first and its inspection refuses to proceed on the other mixed states), so this guards `ichor init` against a broken Cilium, an adopt rerun against a cluster whose Cilium was removed, and the Helm path.

### 4. Installing Ichor with Helm

**4.1 What the chart does and does not do.** `helm install` of `dist/chart` installs **Ichor, not a cluster**: CRDs (kept on uninstall), the controller and its RBAC, the webhooks and their certificate, the UI, the optional Cilium policies, the `ichor-cni` identity. It cannot register a cluster, because that needs what only the operator's machine has and only the CLI does:

| Needs | Where |
|---|---|
| Running Ichor, CRDs, RBAC, UI, webhooks | **Helm** |
| Reading each node with the operator's talosconfig (versions, KubePrism, kube-proxy, install, drift) | **CLI** (`ichor adopt`) |
| Recovering the secrets bundle (`--recover-secrets`) | **CLI** |
| The plan, the consent, writing the `TalosCluster`, `TalosMachine`s, held patches, and releasing the holds | **CLI** (the controller applies after release) |
| The CNI choice (Cilium takeover, unmanaged, or install Cilium on a no-CNI cluster) | **CLI** (`ichor adopt`, the same rules as decision 1 and 2; the chart has no CNI value for it) |

Registering a cluster from chart values (a `TalosCluster` templated in the chart) is **rejected**: it needs the node reads and the bundle, and a typed `TalosCluster` that was not checked against the live cluster is exactly the unreviewed drift the zero-drift rule exists to stop.

**4.2 The sequence.**

```
helm install ichor oci://ghcr.io/maeshinshin/charts/ichor --version X.Y.Z \
    --namespace ichor-system --create-namespace [--set ...]
ichor adopt --cluster NAME --talosconfig FILE (--recover-secrets --recover-from ADDR | --secrets-bundle FILE) [--cni-unmanaged]
```

**4.3 `ichor adopt` after a Helm install.** `ichorRunning` becomes `ichorPresent` and answers true when the `IchorInstallation` singleton exists **or** a Helm release of the chart `ichor` is `deployed` (found with the Helm SDK's `List` over the release Secrets, any namespace) **or** the controller Deployment carrying the chart's selector label exists. If the install is Helm-managed:

- the install step is skipped entirely: no install manifest, no `IchorInstallation` created (a chart-installed Ichor is upgraded through the chart, `installer.enabled: false`; an `IchorInstallation` would invite the installer to fight Helm), and the plan says "Ichor is installed by Helm (release X, version Y); not touching it";
- adopt checks that Ichor's version equals the CLI's (the existing skew rule) and refuses a mismatch with the `helm upgrade` command;
- the network-policy gate is evaluated and, since Helm owns `ichor-system`, adopt does **not** apply the policies itself: when the gate is true and the release has `networkPolicy.cilium.enabled` unset or false it prints "set `networkPolicy.cilium.enabled=true` and run `helm upgrade`". (With `null` the chart renders none and nothing applies them, because there is no installer.)
- A new flag `--require-ichor` (default off) makes a missing Ichor an error ("install Ichor with Helm first") instead of installing it, for operators who want adopt never to install anything.

A controller or UI that needs the `IchorInstallation` object to exist must tolerate its absence; unit 5 checks this and, where something does need it, fixes that code rather than having the chart create the object.

**4.4 The CNI rule on the Helm path is the same, and a no-CNI cluster cannot use the chart alone.** The chart's pods need a pod network, so on a cluster with no CNI `helm install --wait` would hang the same way adopt did. Decided:

- A chart **preflight** (a named template using `lookup` on Nodes, `fail`s the install with a message; skipped by `helm template`/`--dry-run`, where `lookup` returns nothing, and by `preflight.cni: false`) refuses to install when every node is NotReady with the network-plugin message: "this cluster has no pod network, so Ichor's pods cannot start. Run `ichor cni bootstrap` first (it installs Ichor's Cilium), then `helm install` again."
- A new command **`ichor cni bootstrap --kubeconfig FILE --kube-proxy enabled|replaced [--tunnel vxlan|geneve]`** runs exactly the step 2 of 1.5 (the same function as adopt's): creates `ichor-system` if needed, installs the pinned Cilium as Ichor's release record there, waits bounded for the nodes to be Ready. The kube-proxy mode is explicit because the command has no Talos credential to read it, and KubePrism on 7445 is stated in its help as a requirement (adopt's check 1.3.2 still runs later and catches a mistake). The values are the same function of (tunnel, kube-proxy) as in 1.4.
- `ichor adopt`, run after that, finds a Cilium with an Ichor record in `ichor-system` (row 1 of ADR 0103 1.1), reads tunnel and kube-proxy from the record and the Talos side, and agrees or refuses on a mismatch (the 1.5 refusals now apply, as for any existing Cilium).
- The chart **does not ship Cilium** (no subchart, no pre-install hook that runs Helm): a Cilium inside the `ichor` release would have two owners and Ichor's `Upgrade` would fight `helm upgrade ichor`. This keeps "Ichor owns Cilium as a Helm release" (CLAUDE.md): the release is `cilium` in `kube-system` with its record in `ichor-system`, whichever path made it.
- For a cluster that has a CNI, the chart path is unchanged: `helm install`, then `ichor adopt` with takeover or `--cni-unmanaged`. The chart never decides.

**4.5 Publishing the chart.** The chart is published as an OCI artifact `oci://ghcr.io/maeshinshin/charts/ichor`, next to the images (`ghcr.io/maeshinshin/ichor-controller`, `ichor-ui`, `ichor-installer`), by a release workflow on a version tag: `helm package --version V --app-version V` and `helm push`. This is the only publishing target (no Helm repository index to host). Prerequisites in the same change: `values.yaml` image defaults become the real repositories (`ghcr.io/maeshinshin/ichor-controller`, `ghcr.io/maeshinshin/ichor-ui`; `appVersion` already supplies the tag), and the chart test installs from the packaged archive. Until the first release is pushed, `helm install ichor ./dist/chart` from a checkout with `--set manager.image.repository=... --set ichorUI.image.repository=...` is the only path, and the guide says so. Helm itself is installed by the user (CLAUDE.md: tools come from `nix develop`; `flake.nix` gets `helm` if missing for CI and the e2e cases).

**4.6 Upgrade and uninstall.** A Helm-installed Ichor is upgraded with `helm upgrade` (CRDs are in `templates/`, so they upgrade with it); `ichor` commands that would run the installer Job refuse on a Helm-managed install and print the Helm command. `helm uninstall` keeps the CRDs and therefore every cluster object (`crd.keep: true`); it does not touch Cilium (a separate release).

## What must change (work units)

Sizes: S under a day, M one to two days, L three or more. Rows run in parallel unless "Needs" says otherwise. No CRD field is added or removed by this ADR.

| # | Unit | Files | Size | Needs |
|---|---|---|---|---|
| 1 | Bounded waits: the shared helper (timeouts, pod diagnostics, early failure), used by `createWhenWebhooksUp`, the Cilium Ready wait, `waitHandover`; `--wait-timeout`; fake-clock tests with a Pending pod | `internal/cli/init_talos.go`, `init_netpol.go`, `adopt_run.go`, `adopt_write.go`, `adopt.go`, new `internal/cli/wait_diag.go`, tests | M | none |
| 2 | D3 fix: `installIchor` no longer passes the cluster preset to `applyNetworkPolicies`; the gate decides; the skip message names the failed input | `internal/cli/adopt_write.go`, `init_netpol.go`, `adopt_write_test.go` | S | none |
| 3 | No-CNI detection and preconditions (1.1, 1.3, 1.4), refusal of `--cni-unmanaged` there, plan text, mapping of kube-proxy from the Talos side; `cni.name: none` render byte-equality fixture | `internal/cli/adopt_cni.go`, `adopt_k8s_facts.go`, `adopt_run.go`, `adopt_plan.go`, `adopt_cni_test.go`, `pkg/render` test | M | none |
| 4 | Cilium-first install in the adopt CLI: extract the body of `runCNIHelmApply` into `cilium.Bootstrap(ctx, restConfig, cluster, mode, wait)` (namespace create, `Apply`, DaemonSet/node Ready wait); adopt order 1.5 (Cilium, Ichor, CRs, hand-over, release); the values-equality test; `--tunnel`; rerun idempotence; hand-over with no inline entry and no `ichor-cni` Job when the hash matches | `pkg/cni/cilium/helm.go` or new `bootstrap.go`, `internal/cli/cni_helm.go`, `adopt_run.go`, `adopt_write.go`, `internal/controller/cnihandover_controller.go` and tests | L | 1, 2, 3 |
| 5 | Helm-aware adopt: `ichorPresent` (IchorInstallation, Helm release of chart `ichor`, controller Deployment), skip install and `IchorInstallation`, version-skew refusal, policy advice, `--require-ichor`; check that controller and UI tolerate no `IchorInstallation`; installer-Job commands refuse on Helm-managed installs | `internal/cli/adopt_write.go`, `adopt_run.go`, `internal/controller/ichorinstallation_controller.go` (read only paths), `internal/uiapi`, tests | M | 1 |
| 6 | `ichor cni bootstrap` command (calls unit 4's function); chart preflight template with `lookup` and `preflight.cni` value; chart test for both | new `internal/cli/cni_bootstrap.go`, `dist/chart/templates/_preflight.tpl`, `dist/chart/templates/NOTES.txt`, `dist/chart/values.yaml`, `dist/chart/chart_test.go` | S | 4 |
| 7 | Chart publishing: image defaults, release workflow (`helm package`/`helm push` to GHCR on a tag), chart install-from-archive test, `helm` in `flake.nix` if missing | `dist/chart/values.yaml`, `.github/workflows/release-chart.yml`, `flake.nix`, `Makefile` | M | none |
| 8 | Docs: architecture Bootstrap and CNI paragraphs, `docs/guides/adopt-and-reclaim.md` (no-CNI row, Helm path), new Helm install guide section, ADR 0103 amendment line, `make adr-index` | `docs/design/architecture.md`, `docs/guides/`, `docs/adr/0103-*.md` | S | all |
| 9 | Hardware cases E1-E7 below and the cluster generators (a `cni.name: none` cluster; a Helm-installed Ichor) | `test/harness/kubevirt`, `test/e2e` | L | 1-7 as they land |

Unit 7, as built (images): `.github/workflows/release-images.yml` runs on the same `v*` tag and calls `make demo-images VERSION=<tag> REGISTRY=ghcr.io CRANE_INSECURE=`, the crane build that already produced the controller, UI and installer images, so the release images are `ghcr.io/maeshinshin/{ichor-controller,ichor-ui,ichor-installer}:<tag>`: the chart defaults, the install manifest and the `IchorInstallation` `installerRepository` default needed no change. linux/amd64 only, as the Makefile builds today; multi-arch is deferred. `images.sh` now reads `CRANE_INSECURE` (default `--insecure`, for the local demo registry).

Parallelism: 1, 2, 3 and 7 start together (disjoint files except `adopt_run.go`, which units 1 and 3 touch in different functions; land 1 first). 4 follows them; 5 starts after 1 and runs beside 4; 6 follows 4. Per the batch rule one hardware run covers units 1-6 integrated.

## Hardware test plan

**Status: run (2026-10-30), see "Hardware results".** Same harness, budget and recording rules as ADR 0103 (control plane 2560Mi-3Gi, worker 1536Mi, at most 10 VMs; fixtures built with `talosctl`, one run per integrated batch in its own worktree; failing test names recorded even when a rerun passes). This group **replaces D5** of ADR 0103.

**E. No-CNI adoption**
- E1 (replaces D5) 1 control plane plus 1 worker, `cluster.network.cni.name: none`, KubePrism default, kube-proxy enabled. `ichor adopt` (managed): Cilium installed with its record in `ichor-system`, nodes Ready, Ichor pods Running, `CNIHandover=True` with no inline entry and no `ichor-cni` Job, CNI-relevant machine-config paths byte-identical before and after the first render, holds released, `ichor cluster status` says managed Cilium.
- E2 Same with kube-proxy disabled in Talos: `replaced`, `kubeProxyReplacement=true`, Services work.
- E3 `--cni-unmanaged` on a no-CNI cluster: refused, config hashes and `ichor-system` unchanged.
- E4 KubePrism disabled: refused with the "enable KubePrism" message, nothing written. A node without a pod CIDR: refused.
- E5 Kill the CLI during the Helm install and again after the Cilium wait; rerun; assert idempotent finish (and the `pending-install` repair).
- E6 Bounded wait: break the Ichor image (bad tag) on an otherwise good cluster and a cluster whose Cilium is removed after step 2; assert the command exits within the timeout with the Pending reason printed, and that the "no pod network" early failure fires in the second.
- E7 The D3 regression: flannel (no policies, notice printed), Calico (same) and Cilium adopted with `--cni-unmanaged` (policies applied) and managed Cilium takeover (policies applied).

**F. Helm path**
- F1 On a cluster with Cilium: `helm install` of the chart (from the packaged archive, then from GHCR after the first release); `ichor adopt` skips the install step, creates no `IchorInstallation`, registers the cluster, hold released; `helm upgrade` then works and `helm uninstall` keeps the CRDs and the cluster objects.
- F2 On a flannel cluster: `helm install`, `ichor adopt --cni-unmanaged`; the policy advice is printed and nothing is applied to `ichor-system`.
- F3 On a no-CNI cluster: `helm install` is refused by the preflight with the message; `ichor cni bootstrap --kube-proxy enabled`, `helm install`, `ichor adopt`: Cilium row 1 is recognised, tunnel and kube-proxy read back, adoption completes.
- F4 Chart/CLI version skew refused with the `helm upgrade` command; `--require-ichor` with no Ichor refused.
- F5 `helm template` and `helm install --dry-run` on a no-CNI cluster do not fail on the preflight.

Record the exact refusal and wait texts and the time of each phase, and write down what worked.

## Hardware results (2026-10-30)

- E1, E3, E4, E7 passed. E2 passed on rerun: the first run failed with EPERM, fixed by the bounded webhook wait.
- Rerun of 2026-10-30 (images `v0.0.1284-g46458e2`, Talos v1.14, 1 control plane + 1 worker, Cilium 1.18.0, `--tunnel geneve`):
  - E2 passed again end to end (adopt 4m32s; kube-proxy `replaced`, Services resolve, `KubeProxyReplacement: True`).
  - E6 bad image passed: adopt exited 4m12s after it started waiting with `Ichor's webhooks did not finish within 4m0s (pod ... Pending; ... container manager waiting: ImagePullBackOff ...)`. The first run (Cilium installed 04:49:14, wait began 04:49:28) was cut off by the test's own 9m timeout, not by adopt; the rerun on that cluster printed the same refusal, and `TestE6BadImageBoundedWait` failed that rerun only on its "got past the Cilium step" assertion, because Cilium was already there.
  - E6 no pod network passed: Cilium wiped at 05:05:57, adopt failed 05:07:57 with `the pods of Ichor cannot start because this cluster has no pod network (its nodes are NotReady: cni plugin not initialized). Install a CNI, or let `ichor adopt` install Cilium`.
  - E5 kill during the Helm install (killed 22s after "installing Cilium") is only partly fixed. The record is left `pending-install`. The rerun with `--recover-secrets --tunnel geneve` is now accepted (no bundle refusal, no tunnel refusal), installs Ichor and creates the TalosCluster. But `ichor adopt` does not touch the record when Cilium is already there, and the controller's `ichor-cni` Job fails with `cilium: upgrading Helm release cilium: another operation (install/upgrade/rollback) is in progress`: `repairPending` repairs only a record older than the apply timeout (10 minutes) because nothing sets `ApplyOptions.Exclusive`. After the Job fails the controller does not requeue (`CNIHandover=False JobFailed`, no new Job for 21 minutes), so the record never gets repaired and the hand-over never completes. Open: set `Exclusive` where the CNI lock is held (the Job), and/or repair in adopt's rerun path, and requeue after a failed Job.
  - Failing tests seen: `TestE5KillDuringHelmInstall` (pending record, `CNIHandover` False), `TestE6BadImageBoundedWait` (the aborted first run, then the Cilium-step assertion on a reused cluster); the harness's `noCNIJob` helper recursed into itself and is fixed.
- Third run of 2026-10-30 (images `v0.0.1292-g26da779`, with the pending-record repair; Talos v1.14.3, 1 control plane + 1 worker, Cilium 1.18.0, `--tunnel geneve`; each case on its own fresh no-CNI cluster, a cluster takes about 4m30s to come up):
  - **E5 passed** (`TestE5KillDuringHelmInstall`, 530s). Killed 20s after "adopt: installing Cilium" (05:42:56): record `ichor-system/sh.helm.release.v1.cilium.v1 status=pending-install`, Cilium DaemonSets 0 ready. The rerun with `--recover-secrets --tunnel geneve` was accepted and printed `adopt: Ichor's Cilium release record was left pending by an interrupted install; repairing it`, then `installing Cilium 1.18.0`, `Cilium is installed and the nodes are Ready` 2m15s later, created the Secret, TalosCluster and both TalosMachines and released both holds; the rerun took 4m34s. Records afterwards: `cilium.v1 superseded`, `cilium.v2 deployed` (an upgrade, no uninstall). `CNIHandover` True, nodes Ready, controller Running.
  - **E6 bad image passed** (`TestE6BadImageBoundedWait`, 464s, one run on a fresh cluster): adopt took 7m18s in all (Cilium install, then the 4m wait) and exited with `Ichor's webhooks did not finish within 4m0s (pod ichor-system/ichor-controller-manager-... Pending; ... container manager waiting: ImagePullBackOff; pod ichor-system/ichor-ichor-ui-... Pending; ... container ichor-ui waiting: ImagePullBackOff)`.
  - Failing tests seen in this run: none.
- E8 and E9 do not exist: the plan defines E1 to E7 only.
- F1 to F5 passed (F3 after the read-back fix a848ed73).

## Consequences

- ADR 0103's no-CNI row, test D5 and open question 3 are superseded; `talosCNI: none` stays valid for clusters whose own CNI is applied by hand.
- A new path writes to a cluster before any Ichor object exists: the adopt CLI installs Cilium with the operator's credential, announced in the plan and part of consent. It adds one node-visible change (Cilium becomes the cluster's CNI) but no machine-config change, and no write to a node.
- Adopt now has two orders: Cilium-first for a no-CNI cluster, install-first otherwise. The shared code is `cilium.Bootstrap`.
- `ichor init` and adopt can no longer hang silently on a missing pod network.
- A hands-off Cilium cluster gets Ichor's policies at adopt, as ADR 0103 said it should.
- Ichor becomes installable by Helm, with the chart published to GHCR; the install manifest and `IchorInstallation` stay the `init` path. Two ways to install means two upgrade stories (Helm vs the installer Job), kept apart by `ichorPresent`; the CLI refuses to mix them.
- No CRD change. New CLI surface: `ichor cni bootstrap`, `ichor adopt --wait-timeout --tunnel --require-ichor`.

## Risks

- **Cilium installed by a laptop process.** A dropped connection leaves a `pending-install` record or half-ready DaemonSet. Mitigated by idempotent `Apply`, the repair path (ADR 0103 unit 5) and case E5; the user still has to rerun.
- **The values the CLI installs differ from the ones the controller derives**, so the controller starts an `ichor-cni` Job that restarts agents on a live network. Mitigated by building the `TalosCluster` first, the equality test and case E1's "no Job" assertion.
- **KubePrism assumed.** A no-CNI cluster with KubePrism off is refused rather than fixed; an operator on an old config has one extra manual step. The refusal text gives it.
- **Detection of "no CNI" misreads a cluster whose CNI is broken** (all nodes NotReady because the user's Calico crashed). The rule requires no CNI workload to exist; a crashed Calico DaemonSet exists, so that cluster is the Calico (unmanaged) case, not this one.
- **A Helm-installed Ichor with no `IchorInstallation`** may hit code that assumes it. Unit 5 audits and fixes; case F1 covers it.
- **The chart has never been published**, and its values defaults were placeholders. Until unit 7 lands the Helm path is checkout-only; this ADR does not hide that.
- **Hand-over with no inline entry** is a path the hand-over controller has not run on hardware. Case E1 is its first run.

## As built (2026-10-29)

Units 1 to 7 are built; unit 9 (hardware cases) was run on 2026-10-30, see "Hardware results". Where the build differs from the text above, the build is right:

- **The Cilium Helm Job uses `ApplyForCluster`, not `Bootstrap`.** `cilium.Bootstrap` is the CLI's entry (namespace, `Apply`, Ready wait); the `ichor-cni` Job and the CLI share `ApplyForCluster` underneath. The Job also runs without an `IchorInstallation`, which a Helm install does not have.
- **`ichor cni bootstrap` cannot check KubePrism.** It has no Talos credential, so it states the requirement in its help and relies on adopt's later check (1.3.2). `--kube-proxy enabled|replaced` is required for the same reason.
- **Strict version equality for Helm installs.** Adopt on a Helm-installed Ichor refuses any version different from the CLI's, not only a skew beyond the usual rule, and prints the `helm upgrade` command.
- **Chart version has no leading `v`; `appVersion` and image tags have it** (for example chart `0.1.0`, appVersion and tag `v0.1.0`).
- **Images are linux/amd64 only.** They are built by the existing crane target (`make demo-images`) on a `v*` tag, as `ghcr.io/maeshinshin/{ichor-controller,ichor-ui,ichor-installer}`.
- **The UI server's Helm detection is limited.** The Ichor upgrade path in the UI points to `helm upgrade` when it can tell the install is Helm-managed, but it cannot detect every Helm install, so some prompts may still describe the installer Job.
- **No release exists yet.** `oci://ghcr.io/maeshinshin/charts/ichor` and the image repositories are empty until the first `v*` tag is pushed.
- **Added flags beyond the text:** `ichor adopt --cilium-values FILE` (open question 1) and `ichor cni bootstrap --cilium-values FILE`, `--tunnel`.

Guides: `docs/guides/install-with-helm.md`, and the no-CNI and Helm sections of `docs/guides/adopt-and-reclaim.md`.

## Open questions

All three are resolved.

1. `--cilium-values FILE` for the no-CNI path: **accepted and built**, on `ichor adopt` and `ichor cni bootstrap`. Ichor-owned keys are refused.
2. `ichor cni bootstrap` for `ichor init`: **no.** `init` keeps the inline-manifest bootstrap; `ichor cni bootstrap` is for an existing cluster with no pod network.
3. Default timeouts: **kept as built** (15 minutes for the webhooks in adopt, 30 in `init`, 10 for Cilium Ready, 10 for hand-over). Hardware cases E6 and the first real runs tune them.

Amendment 2026-10-29 (ADR 0105): a Helm install does get an `IchorInstallation`: the controller creates it once when absent (`--deployed-by helm`, set by the chart), with `spec.deployment.manager: helm`; the chart still never renders it. `ichorPresent` keeps its three signals, but `spec.deployment.manager` is authoritative once the object exists. Adopt skips the install step on `manager: helm` and does not wait for the object; installer-Job commands and a `TalosOperation` of kind `ichor` are refused on Helm. The chart adds `ichorUI.port` (8443), `ichorUI.plaintextPort` (opt-in, deprecated), `ichorUI.tls.caConfigMap/caSecret/caKey` and `settings.readOnly`.

## Amendment (2026-11-04): release artifacts and the version stamp

`appVersion` carries the leading v (`v0.1.0`) because the chart's default image tag is `.Chart.AppVersion` and images are tagged with the git tag; `version` stays without the v. `make release VERSION=vX.Y.Z` is the local equivalent of the release workflow's build job and writes the CLI, the install manifest and the chart to `bin/release/`. The goreleaser build uses `-tags installembed` with the install manifest generated in a `before` hook for the tag being released, and stamps `{{ .Tag }}` (not `{{ .Version }}`) so the version keeps its v and passes `semver.IsValid`.

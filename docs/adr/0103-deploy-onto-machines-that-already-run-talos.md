# 0103. Deploy onto machines that already run Talos

Status: Proposed · Date: 2026-10-29 · Design section: Bootstrap (`ichor adopt`), Nodes, Cilium · Builds on: 0014, 0042, 0066, 0075, 0076, 0077, 0099, 0101

Decided: Ichor adopts a running Talos cluster only when its CNI is Cilium (any other CNI is refused with nothing written, no migration), takes an existing Cilium over as its own Helm release through a diff-checked `TakeOwnership` Job, recovers the cluster's secrets bundle from a control plane's machine config, holds every adopted machine until zero drift is verified, and refuses to join an already-configured node unless the operator runs an explicit CLI reclaim that resets only STATE and EPHEMERAL after typed confirmation

## Context

The user asked to think about deploying onto machines that already have Talos installed. Two situations are meant, and Ichor handles neither well today.

1. **A running cluster Ichor did not create**, brought under management (`ichor adopt`).
2. **One machine that already runs Talos, configured, added to an Ichor cluster.** Ichor's join flow starts from maintenance mode (ADR 0076); a configured node never reaches it.

Constraints from the repo: Cilium is the only CNI and Ichor's own Helm release (ADR 0099, kept by 0101); Argo CD, Kokumi and every GitOps tool are out of scope (0101); machine config is typed only, so a difference that is not a `SysctlConfig`/`KernelModuleConfig` document cannot become a patch (0014, 0075, 0077); the controller is the only writer to nodes, except that `init`, `adopt` and full-loss recovery predate the cluster and run in the CLI (architecture, "CLI and UI"); a reset names `STATE` and `EPHEMERAL` explicitly (0042).

### What `ichor adopt` does today (read from `internal/cli/adopt*.go` at this commit)

- It needs `--cluster`, `--secrets-bundle FILE` (a `talosctl gen secrets` file; the flag is required) and the operator's talosconfig. It dials nodes read-only.
- It **creates nothing but `TalosConfigPatch` objects**. `runAdopt` Gets the `TalosCluster`, lists `TalosMachine`s and patches, and Creates patches. The `TalosCluster`, the bundle Secret, the `TalosMachine`s, the approvals and Ichor itself must already exist. No command creates them for a cluster Ichor did not build.
- Per approved machine it reads the live config, renders a baseline, and diffs (`controller.ChangedPaths`). Only `SysctlConfig` and `KernelModuleConfig` are captured; a patch is returned only if render plus patch equals the live bytes. Everything else is reported by path with a hint (`specFieldHints`, ADR 0077). Unapproved machines are skipped (0075). A machine with JWT authenticators is refused (0094).
- It knows nothing about the CNI beyond a hint that flannel is unsupported. It does not read Kubernetes state, kube-proxy, KubePrism or inline manifests, and it never touches Cilium.
- The render drops only an inline manifest named `cni` (`pkg/cni.InlineManifestName`, `dropHandedOverInlineManifest`). An existing cluster's Cilium entry has whatever name its author chose.

## Findings

### Helm SDK v3.21.4 (`helm.sh/helm/v3`, the version in `go.mod`, read from the module cache)

- `Install` with `TakeOwnership` calls `requireAdoption` instead of `existingResourceConflict` (`pkg/action/install.go:353`, `validate.go:41`). `requireAdoption` only checks that each object exists. `checkOwnership` (the `app.kubernetes.io/managed-by: Helm` label and the two `meta.helm.sh/release-*` annotations) is skipped. So **`TakeOwnership` works on objects with no Helm metadata (Talos inline manifest, `helm template | kubectl apply`) and on objects owned by a different release name or namespace.** Without it those installs fail with "invalid ownership metadata".
- Before that, `setMetadataVisitor(..., force=true)` rewrites the ownership label and annotations on the rendered objects, so they carry Ichor's release afterwards (`install.go:339`).
- The objects are then updated with `UpdateThreeWayMerge(toBeAdopted, resources)` (`install.go:461`). In `requireAdoption` the infos appended are the *rendered* ones (the live object is read and thrown away), so "original" equals "target". Reading `createPatch` (`pkg/kube/client.go:640`) the consequence is: fields that differ are overwritten by the chart's values, but **live-only fields are kept and live-only objects are never deleted**, because nothing in the new release's manifest records them. Adoption converges the rendered fields and silently keeps the rest. This is read from source, not yet observed; hardware test A5 checks it.
- `availableName` looks for a release of that name **in the storage backend `cfg` points at** (`install.go:533`). Ichor stores records in `ichor-system` (ADR 0099, departure 2). A user's `sh.helm.release.v1.cilium.vN` in `kube-system` is therefore invisible: Install does not fail on a name clash, it creates a second, independent revision 1. Two Helm histories would then describe one set of objects, and a later `helm upgrade` by the user would fight Ichor.
- `Upgrade` also has `TakeOwnership` (`upgrade.go:121,347`), but Ichor's `Apply` only runs it after an Ichor-side record exists.
- Chart `crds/` handling (`installCRDs`) runs on Install; the pinned Cilium 1.18 chart is checked for a `crds/` directory in unit 5.

### Talos v1.14 (docs.siderolabs.com via Context7, and ADR 0099's earlier reading)

- Inline manifests are created and never updated or deleted by Talos; a values change needs `upgrade-k8s`. Therefore an inline-manifest Cilium cannot be left in the machine config once Ichor owns the release (ADR 0099, decision 2): the entry has to go, or a Kubernetes upgrade can re-apply it over the Helm release.
- `talosctl reset` accepts `--system-labels-to-wipe`, `--graceful` (cordon, drain, leave etcd; default true) and `--reboot`; ADR 0042 already fixed the request Ichor sends.
- `secrets.NewBundleFromConfig(clock, config.Config)` exists in machinery v1.14.3 (`config/generate/secrets/bundle.go:203`). It rebuilds the bundle (OS, Kubernetes, aggregator and etcd CAs, service-account key, cluster ID and secret, trustd token, bootstrap token, secretbox secret) from a control plane's loaded config, tolerating the v1.14 multi-document form.

## Decision

### 1. Adopting a running cluster

**1.1 The CNI decides, and the answer for anything but Cilium is refusal.** `ichor adopt` first runs a read-only inspection and prints a report. Provenance is classified from the node config and the Kubernetes API, not from names alone:

| Found | Result |
|---|---|
| Cilium, Ichor-bootstrapped | Already handled; adopt only checks the release exists. |
| Cilium from a Helm release (`helm`, `cilium install` of the Cilium CLI, which also writes a Helm release) | Take over (1.2, case B). |
| Cilium from a Talos inline manifest or an applied `helm template` | Take over (1.2, case C). |
| Talos's own flannel (`KubeFlannelCNIConfig`, `cluster.network.cni.name: flannel` or unset) | **Refuse.** |
| Calico, another CNI, or `cni.name: custom` | **Refuse.** |
| Mixed (Cilium DaemonSet plus a second CNI's DaemonSet, or two CNI configs under `/etc/cni/net.d`) | **Refuse**, naming both. |
| No CNI installed (`cluster.network.cni.name: none`, nodes NotReady) | Allowed only as an open item (below); not in the first release. |

Refusal writes nothing: no Secret, no CR, no node call beyond reads. The message names the CNI found and what to do (stand up a new Cilium cluster and move workloads, or migrate the CNI by the operator's own means and run adopt again).

*Why refuse rather than migrate.* A CNI swap takes pod networking down; the `TalosCluster` CRD already says so and makes `cni.preset` and the tunnel immutable for that reason. A migration cannot be rolled back by Ichor once the old datapath is gone (the new CNI owns the pod CIDR and node routes), cannot be verified before it is done, and needs per-workload knowledge Ichor does not have: NetworkPolicy semantics differ between CNIs, Service routing changes if kube-proxy is also switched, and the pod CIDR and IPAM mode of the old CNI constrain Cilium's (`ipam.mode` is Ichor-owned, `kubernetes`). Cilium's own documented migration is a node-by-node dual-CNI procedure with a different overlay CIDR; wrapping it would make Ichor the operator of a rolling production network outage, which is what ADR 0099's "Ichor bootstraps CNI once" avoided. The cost of refusing is that a flannel cluster (Talos's default) cannot be adopted. That is acceptable for a first release and reversible: a migration ADR can follow, and nothing here forecloses it.

**1.2 Taking over an existing Cilium.**

Preconditions, checked before anything is written:

1. **Version.** The live Cilium minor equals the embedded chart's minor, or is exactly one lower (the existing one-minor-at-a-time rule for `cni` operations, architecture "Upgrades"). Otherwise refuse and say to upgrade with the tool that installed it first. Adoption of an older minor is an upgrade; it is announced as one.
2. **Ichor-owned keys.** The live configuration is compared with the keys Ichor owns (ADR 0099, decision 3): `ipam.mode` (must be `kubernetes`; a `cluster-pool` Cilium is **refused**, changing IPAM renumbers pods), `kubeProxyReplacement` (maps to `spec.cni.kubeProxy`, 1.5), `tunnelProtocol` (maps to `spec.cni.tunnel`; immutable afterwards), `k8sServiceHost`/`k8sServicePort`, the Talos capability and cgroup keys. A value that Ichor would change on a running data plane (other than the capability and cgroup keys, which Ichor makes match the Talos guide) is reported and requires `--accept-cilium-changes`; IPAM and tunnel protocol are never accepted.
3. **Values.** Case B: the user values are decoded from the newest release record (`sh.helm.release.v1.cilium.vN`, base64, gzip, JSON, field `config`) and become `spec.cni.cilium.values` after removing owned keys, which are then checked as in step 2. Case C has no record, so the operator supplies `--cilium-values FILE` (what they gave `helm template`); without it adopt uses the Ichor defaults and the diff in step 4 will show what is missing.
4. **Diff gate.** Ichor renders the pinned chart with the effective values (the same `EffectiveValuesFor` path as bootstrap) and compares every rendered object with the live one, with the volatile fields removed. An empty difference proceeds. A non-empty one is printed per object and path and needs `--accept-cilium-diff`. This is the Cilium counterpart of adopt's zero-drift rule. It is needed because of the live-only-fields finding above: `TakeOwnership` would otherwise leave an unreviewed mix of chart and user state that Ichor's later `Upgrade` (`ResetValues`) interprets differently.
5. **Health.** The DaemonSet and operator are Ready on all nodes, and KubePrism answers on every node (1.6), because the Job reaches the API through `localhost:7445` while agents restart.

The takeover itself:

- It reuses the `ichor-cni` Job and `cilium.Apply` (ADR 0099 unit 7). With no Ichor record, `Apply` runs `action.Install` with `TakeOwnership: true`, which is correct for both cases: B (objects carry another release's metadata, possibly another name or namespace) and C (no Helm metadata). Only the diff gate and the foreign-record handling are new.
- **Foreign release record.** After the Ichor record is written and the rollout is Ready, a foreign `cilium` record in `kube-system` (or elsewhere) is copied into a Secret `ichor-cilium-prior-release` in `ichor-system`, then deleted (the record Secrets only; never `helm uninstall`, which would delete the CNI). Deleting it is announced in the plan and part of the consent. Leaving it would give two histories for one object set (see the `availableName` finding). Rejected alternative: point Ichor's storage at the foreign record's namespace and `Upgrade`; it keeps the user's history but breaks ADR 0099's namespaced-Role decision and the hand-over reader.
- **Inline entry.** In case C and in any cluster whose Cilium also sits in `cluster.inlineManifests`, the entry is found by content (a manifest that contains the `cilium` DaemonSet in `kube-system`), not by name, because `dropHandedOverInlineManifest` matches only the name `cni`. Its name is recorded (additive status field on the TalosCluster, unit 6) so the render drops it. **Ordering:** the entry is removed from the machine config only after the Helm record is `deployed` and the DaemonSet Ready (`CNIHandover`, unchanged in shape). If takeover fails nothing on the nodes has changed. Other inline or external manifests are not touched and make the machine's drift unrepresentable (1.7).
- No rollback of an adoption (Atomic would uninstall a running CNI, ADR 0099). A failed Install leaves a `failed` record; the next run goes to `Upgrade` (as `Apply` already does) and Helm's `pending-install` state needs the explicit handling listed in Risks.

**1.3 The cluster's secrets bundle.** The `--secrets-bundle` flag becomes optional. If a file is given it is used (as today). Otherwise `--recover-secrets` reads one control plane's machine config over the Talos API with the operator's talosconfig and builds the bundle with `secrets.NewBundleFromConfig`, then `Validate`s it against the chosen version contract. Recovery is read-only on the node. The result is written under `~/.ichor/clusters/<name>/` with mode 0600, as `init` does, and the operator is told to back it up; nothing is printed. A wrong or partial bundle cannot get through: the render uses it, so any secret field that differs from the live config appears in the zero-drift diff (1.7) and no patch is produced. The three-CA PKI (Talos, Kubernetes, etcd) is unchanged; Ichor does not rotate anything during adoption.

**1.4 The talosconfig.** It is the operator's own and is used only by the CLI, read-only, never stored, never sent to the controller (as ADR 0014 decided). `--talosconfig` gains `--context` (the file may hold several clusters). The node list comes from the control plane's discovery membership, not from typing every address. After adoption the controller uses a certificate minted from the recovered bundle (its short-lived `os:admin` identity, architecture "Trust"). The operator's old client certificate stays valid until it expires or a CA rotation (ADR 0063, 0091); adopt prints that fact and offers `ichor cluster rotate-ca` as an explicit next step, not as part of adoption.

**1.5 kube-proxy mode detection.** Both sides are read and must agree:

- Talos side: `KubeProxyConfig.enabled` (v1.14) or `cluster.proxy.disabled` (legacy), plus whether the `kube-proxy` DaemonSet exists.
- Cilium side: the `kube-proxy-replacement` key of the `cilium-config` ConfigMap.

Mapping to `spec.cni.kubeProxy`: Talos proxy enabled and replacement off gives `enabled`; Talos proxy disabled and replacement on gives `replaced`. Disabled with replacement off (no Service routing) and enabled with replacement on (works, two owners) are **refused** with the reason, because Ichor's consistency check (ADR 0066) would mark the cluster unhealthy from its first minute and the fix is a switch the operator should make deliberately. The CRD default is `enabled`, so adopt always writes the detected value; it never relies on the default.

**1.6 KubePrism.** Ichor's Cilium Job and its replaced-mode values require KubePrism on port 7445 on every node (ADR 0099). Detected from `machine.features.kubePrism` or the `KubePrismConfig` document. Disabled, or on another port: reported as an Ichor-owned difference (1.7, class B) that the first render will change, with consent; in `replaced` mode with KubePrism off, adoption is **refused**, since turning it on and moving Cilium onto it cannot be ordered safely in one step.

**1.7 Machine config drift on adoption.** The single "zero drift or nothing" rule of ADR 0014 stays for what patches can express. Three classes replace the two (patch, unrepresentable):

- **A. Representable**: sysctl and kernel-module documents, and values that now have spec fields (subnets, install disk, ADR 0077). Captured or mapped as today.
- **B. Ichor-owned**: the Cilium inline entry (1.2), KubePrism, kube-proxy (when `replaced`), flannel removal. The render will change these on its first apply. They are listed, need the plan's consent, and do not block.
- **C. Unrepresentable**: everything else in the legacy `Config` document and any other inline or extra manifest. Blocks that machine, as today, with the path list and hint.

Two additions make this workable:

- **Version contract probing.** The `TalosCluster.spec.versionContract` is chosen by rendering at each contract from the nodes' running version downwards and taking the one with the fewest class C paths, not assumed equal to the running Talos version. A cluster generated on an old `talosctl` and upgraded in place has a legacy-form config; forcing v1.14 turns every field into drift (the ADR 0075 failure). The result is shown and can be overridden.
- **Hold.** The controller applies drift on every approved machine as soon as it exists. So `adopt` creates each `TalosMachine` with the annotation `ichor.maesh.dev/hold-apply: "true"`, which the machine reconciler reads: it computes and reports drift and never applies. Adopt removes the annotation, machine by machine, only after the live zero-drift check passes and the operator confirmed class B. No CRD field is added.

**1.8 Order and what is written.** `ichor adopt` becomes four phases; each ends at a state in which nothing has been applied to a node.

1. `--plan` (default without `--yes`): inspection and report, writes nothing anywhere.
2. Write: the bundle Secret, the `TalosCluster` (contract, subnets, `cni.preset: cilium`, `kubeProxy`), `TalosMachine`s (identity read over the authenticated API, role, install, held), `TalosConfigPatch`es, Ichor's install manifest if Ichor is not yet running (the same step `init` does). All held.
3. Cilium takeover (1.2).
4. Release the hold per machine: first render is applied by the controller, control planes one at a time under the cluster lock like any config rollout (ADR 0080).

### 2. Adding a machine that already runs Talos

Four states must be told apart: (i) maintenance (existing flow, ADR 0076); (ii) configured by a foreign cluster; (iii) configured by this Ichor cluster (a returning machine; recognised by identity and cluster ID, handled by the authenticated path); (iv) installed but STATE wiped, which is maintenance.

For (ii), discovery cannot read identity (a configured node's API requires a client certificate), so today it fails with a TLS error and, per ADR 0011, registers nothing. Discovery now reports **`AlreadyConfigured`** as an Event and in the add-by-IP response when it sees a Talos API that completes a TLS handshake but rejects an insecure call. Nothing is created.

**Decision: refuse by default; reclaim only through an explicit CLI command run by the operator with that node's own credential.**

```
ichor machine reclaim ADDRESS --talosconfig FILE [--context C] [--confirm UUID]
```

- It is a CLI-only, in-process Talos write, the same kind of exception as `init` and `adopt` (the node is not yet part of any Ichor cluster, and the credential is a foreign admin certificate). The controller never holds a credential for another cluster's nodes, and Ichor never stores one; that is why this is not a controller operation or a UI button. The UI shows the state and the command to run.
- Steps: authenticated read of identity, hostname, role, the foreign cluster's name and ID, disks, user volumes and, for a control plane, etcd members and health. It prints this, then:
  - **Refuses** if the node's identity or cluster ID is already an Ichor `TalosMachine` or cluster (state iii).
  - **Refuses** a control plane whose removal would break its cluster's etcd quorum, or the last control plane of a reachable cluster, unless `--force` and a second typed confirmation.
  - Uses `Graceful: true` (cordon, drain, leave etcd) when the foreign cluster is healthy and reachable from the node; `Graceful: false` only with `--force-ungraceful` when it is dead.
  - Sends exactly the request of ADR 0042 (`STATE` and `EPHEMERAL`, `Reboot: true`), built by the same code, so one place defines what a reset wipes. User disks and non-system volumes are not touched, but they are listed as orphaned, and encrypted ones are called out: their keys derived from STATE or META are gone, so the data is unreadable.
  - Requires **typed consent**: the operator types the node's UUID (or passes `--confirm UUID`; `--yes` does not satisfy it). Consent is the credential plus the typed identity, shown beside the cluster name that will lose the node.
  - Waits for the node to answer the maintenance API with the same UUID, then registers it through the normal add-by-IP path as a pending `TalosMachine`. A changed DHCP address after the reboot is reported with the scan hint instead of guessed.
- Without the node's credential there is no API route; the documented out-of-band routes are reinstall from the ISO or a wipe by kernel argument. The kernel-argument route is not verified here (test C6).

Rejected: *joining without a reset.* Applying a new config with new secrets to a node that has the old cluster's etcd data and state does not give a clean member and the outcome differs by role; Ichor would also have to hold the old admin credential in the cluster. Rejected: *automatic reset by the controller* on approval; ADR 0042 made reset opt-in per object because it is irreversible, and a foreign node's data is the one thing Ichor has no record of.

## What must change (work units)

Sizes: S under a day, M one to two days, L three or more. Units on the same row can run in parallel. Each lists files; a unit adds its own tests (names noted in the brief, per the rule on failing test names).

| # | Unit | Files | Size | Needs |
|---|---|---|---|---|
| 1 | Talos-side facts: contract probing, KubePrism, kube-proxy, CNI document, inline manifests, install, roles from membership; `--context` | new `internal/cli/adopt_talos_facts.go`; `adopt.go`, `talosdial.go`, `adopt_plan.go` (contract loop) | M | none |
| 2 | Kubernetes-side facts: CNI detection and provenance (DaemonSets, `cilium-config`, CiliumNode CRD, Helm release Secrets, kube-proxy DaemonSet) and the refusals of 1.1 and 1.5 | new `internal/cli/adopt_k8s_facts.go`, `adopt_cni.go`; `adopt_run.go` (replace the flannel hint) | M | none |
| 3 | Secrets recovery: `--recover-secrets`, optional `--secrets-bundle`, validate, 0600 store | new `internal/cli/adopt_secrets.go`; `adopt.go`, `bundlefile.go` | S | none |
| 4 | Hold: `ichor.maesh.dev/hold-apply` annotation constant, reconciler reports drift and never applies, condition `AdoptionHeld` | `api/v1alpha1/talosmachine_types.go` (constant), `internal/controller/talosmachine_controller.go`, `talosmachine_controller_test.go` | S | none |
| 5 | Cilium takeover library: release-record decode, version rule, owned-key conflicts, live-object diff gate, foreign-record backup and removal, `crds/` check | new `pkg/cni/cilium/takeover.go`, `takeover_test.go`; `helm.go`, `validate.go` | L | none |
| 6 | Takeover wiring: Job flag and plan input, content-based inline-entry detection, recorded entry name (additive `TalosCluster.status.cni` field), render drops it, handover ordering | `internal/cli/cni_helm.go`, `internal/controller/cni_helm.go`, `cnihandover_controller.go`, `api/v1alpha1/taloscluster_types.go`, `pkg/render/render.go`, regenerate CRDs (`make manifests generate`) | L | 5, 4 |
| 7 | Three-class drift (A/B/C), consent list, release of the hold, the four phases | `internal/cli/adopt_plan.go`, `adopt_run.go`, `adopt_test.go` | M | 1, 2, 4 |
| 8 | Write phase: create bundle Secret, `TalosCluster`, held `TalosMachine`s (authenticated `Identity`), Ichor install step | new `internal/cli/adopt_write.go`; `internal/controller/talosclient.go`, `talosclient_machinery.go` (an `Identity` read, fakes updated) | M | 3, 4 |
| 9 | `ichor machine reclaim` and the shared reset request | new `internal/cli/machine_reclaim.go`; `internal/controller/removal.go` (export the request builder), `talosclient_machinery.go` (etcd members, volumes read) | M | none |
| 10 | Discovery `AlreadyConfigured` classification, add-by-IP response, UI string | `internal/controller/discovery.go`, `maintenanceclient_machinery.go`, `internal/uiapi/discovery_add.go`, `ui/` machine list message | S | none |
| 11 | Docs: architecture Bootstrap and Nodes paragraphs, CLI help, ADR status; `make adr-index` | `docs/design/architecture.md`, `docs/adr/` | S | all |
| 12 | Hardware harness cases and cluster generators (below) | `test/harness/kubevirt`, `test/e2e` | L | 1-10 as they land |

Parallelism: units 1, 2, 3, 4, 5, 9 and 10 touch disjoint files and can start together; 6 starts on a stub of 5's interface; 7 and 8 follow 1/2/4; 12 starts with generators and the refusal cases immediately.

## Risks

- **Data-plane disturbance on takeover.** The pod template changes (checksums, image, args) and agents restart on every node; eBPF programs persist but a version jump or changed values can drop connections. Mitigated by the minor rule, the diff gate and the continuity measurement in test A4; the amount of loss is not yet measured.
- **Wrong diff semantics.** The live-only-fields conclusion comes from reading `createPatch`. If a later Helm version changes it, the diff gate still protects, but the gate itself needs live-object normalisation that can miss defaulted fields (false positives are safe, false negatives are not).
- **Helm half-states.** A killed Job can leave `pending-install` or a failed record; Upgrade then refuses ("another operation is in progress"). Needs an explicit repair path in unit 5.
- **Config applied too early.** If the hold is missing the controller would apply a render that differs from the live config. Unit 4 is a hard prerequisite of unit 8; a test must assert no apply call while held.
- **Contract probing picks a wrong contract** and hides drift as class C. It is shown, overridable, and class C blocks.
- **Reclaim wipes the wrong node or a live quorum.** Mitigated by typed UUID, cluster name on screen, the etcd checks and the Ichor-cluster refusal; the residual risk is a stale or copied talosconfig pointing at a different node than the operator believes, which the identity print is there to catch.
- **Deleting the foreign release record** removes a user's `helm rollback` path. Backed up to `ichor-system`; part of the consent.
- **Recovered bundle is incomplete** (for example, an etcd-encryption config in a non-standard shape: machinery states it extracts "our best" of the secretbox key). Caught by the zero-drift diff and by a Secret-decryption check in test B3.
- **Unexpected version support**: very old Talos contracts may have no render path at all in the pinned machinery. The lowest adoptable Talos version is an open question.

## Open questions (need the user)

1. Refuse non-Cilium CNIs in the first release (recommended), or fund the migration ADR now?
2. Reclaim in the CLI with the operator's own credential (recommended) or a controller operation fed by a short-lived Secret holding the foreign talosconfig?
3. Delete the foreign Helm release record after takeover (recommended, with a backup) or keep two histories?
4. Non-Cilium inline or external manifests in a node's config: refuse the machine (this ADR) or add a patch kind for `KubeInlineManifestConfig` so they can be carried?
5. Support clusters with no CNI at all (`cni: none`) by letting adopt install Cilium directly?
6. Offer CA rotation at the end of adoption by default to retire the operator's old Talos client certificates, or keep it manual?
7. Does `ichor adopt` install Ichor into the foreign cluster itself, or do we require `ichor install` first? This ADR assumes it does, as `init` does.
8. The lowest Talos and Kubernetes version adopt will accept.

## Hardware test plan

Run on the KubeVirt harness (ADR 0007, 0102) within the homelab budget: control plane 2560Mi-3Gi, worker 1536Mi, at most 10 VMs. Build fixtures with `talosctl` in `test/harness`, not by Ichor, so the clusters are foreign. One run per integrated batch, in its own worktree; failing test names are recorded even when a rerun passes. Cases below are the minimum; the first of each group is 1 control plane plus 1 worker, 3 control planes only where stated.

**A. Cilium takeover** (one cluster per row)
- A1 Cilium in a Talos inline manifest, values from the Talos guide, same minor. Expect: diff gate empty or only listed items, takeover, inline entry removed, `CNIHandover` true, a Kubernetes patch upgrade does not roll Cilium back.
- A2 `helm install cilium` in `kube-system`, user values. Expect: values lifted, foreign record backed up and removed.
- A3 Cilium CLI install. Same expectations as A2 (release created by the CLI).
- A4 Data-plane continuity: continuous pod-to-pod and pod-to-Service traffic across A1 and A2; record loss and connection resets; the pass criterion is set after the first run.
- A5 Live-only residue: add an extra annotation, an extra arg and an extra ConfigMap before takeover; assert the diff gate reports the first two and that the extra object stays after takeover (verifies the source reading).
- A6 Older minor (1.17) is refused with no write; one lower than the embedded minor is accepted if the embedded chart is a minor above it; a newer minor is refused.
- A7 `ipam.mode: cluster-pool` and native-routing/tunnel variants are refused or mapped as specified.
- A8 Kill the Job after the update and before the record write; re-run; assert idempotent. Kill after a `pending-install` record; assert the repair path.

**B. Whole-cluster adoption**
- B1 Flannel cluster (Talos default) and a Calico cluster: refused, asserted by identical machine-config hashes and no new objects in `ichor-system`.
- B2 kube-proxy matrix: enabled with replacement off; disabled with replacement on; the two inconsistent combinations are refused.
- B3 Secrets: cluster created without a saved bundle; recover; zero-drift diff clean; a deliberately wrong bundle produces secret-field drift and no patch; the recovered bundle mints a certificate the nodes accept and decrypts a Secret created before adoption.
- B4 KubePrism disabled in kube-proxy-enabled and replaced modes.
- B5 Config generated at an older contract and upgraded in place: contract probing result and drift class counts.
- B6 Hold: assert no `ApplyConfiguration` while the annotation is set; assert control planes roll one at a time on release.
- B7 3 control planes, one worker, with a user inline manifest of a non-Cilium app: that machine is blocked class C, nothing applied.

**C. Reclaim**
- C1 Worker of another cluster: reset, reappears in maintenance with the same UUID, approve into an Ichor cluster, joins.
- C2 Control plane of a 3-member cluster (graceful: the old cluster's etcd has 2 healthy members afterwards) and of a 2-member cluster (refused).
- C3 Single-node cluster's only control plane: refused; with `--force` ungraceful path.
- C4 Machine already in the Ichor cluster: refused. Wrong typed UUID: nothing sent.
- C5 Node with a user volume and an encrypted volume: both listed; user disks survive, encrypted data is unreadable.
- C6 Without a credential: record whether the kernel-argument wipe works on this Talos version.
- C7 Address changes after the reboot: the hint, not a guess.

Record, per case, the time taken, the Talos and Cilium versions and the exact refusal text, and write down what worked, not only conclusions.

## Consequences

- `adopt` stops being a patch-only step and becomes the four-phase adoption of 1.8; its read-only default plan is the first thing a user sees.
- Flannel and other-CNI clusters stay outside Ichor until a migration ADR exists.
- The CRD gains one additive status field (the recorded inline-manifest name); no field is removed. The hold is an annotation.
- `TalosClient` grows an authenticated `Identity` read; the reset request moves to a shared builder.
- ADR 0014's statement that adopt "never writes to a node" still holds: reclaim is a different command, and phase 4 is the controller's.
